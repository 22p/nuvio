// Package hls runs on-demand ffmpeg transcoding sessions that produce HLS.
// Direct play is always preferred; this package is only used when a client
// cannot play the original file.
package hls

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options controls one transcoding session.
type Options struct {
	BitrateKbps int
	AudioIndex  int
	StartTicks  int64
	Height      int
	Width       int
	Preset      string
	Hardware    string
	// HardwareDevice is the VAAPI render node, e.g. /dev/dri/renderD128.
	HardwareDevice string
	// VideoCodec is the source codec, used to pick a QSV decoder.
	VideoCodec string
	Encoder    string
	// Codec is the output codec: h264 (default), hevc, av1 or vp9. Codecs
	// other than H.264 are packaged as fragmented MP4 segments.
	Codec string
	// StartNumber is the sequence number of the first segment. Resuming a
	// suspended session keeps numbering aligned with the client's timeline.
	StartNumber int
	// UserID owns the session; used to stop a user's streams when a client
	// omits PlaySessionId from its stop report.
	UserID string
	// Copy repackages the existing streams (-c copy) instead of encoding.
	Copy bool
}

// Session is a running ffmpeg process writing segments into Dir.
type Session struct {
	ID         string
	ItemID     string
	Dir        string
	StartTicks int64
	Bitrate    int
	Height     int
	Width      int
	Copy       bool
	Codec      string
	UserID     string
	CreatedAt  time.Time

	mu            sync.Mutex
	cmd           *exec.Cmd
	err           string
	stderr        strings.Builder
	done          chan struct{}
	lastAccess    time.Time
	lastSegment   int
	finished      bool
	suspended     bool
	suspendedAt   time.Time
	opts          Options
	itemPath      string
	fallbackTried bool
}

// Manager owns active sessions.
type Manager struct {
	mu          sync.Mutex
	restartMu   sync.Mutex
	dir         string
	sessions    map[string]*Session
	ffmpeg      func() string
	maxAge      time.Duration
	idleTimeout time.Duration
	sweep       time.Duration
}

// New creates a manager rooted at dir. ffmpeg is resolved lazily so the admin
// can change the binary path at runtime.
func New(dir string, ffmpeg func() string) *Manager {
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
	_ = os.MkdirAll(dir, 0o755)
	m := &Manager{
		dir:         dir,
		sessions:    map[string]*Session{},
		ffmpeg:      ffmpeg,
		maxAge:      3 * time.Hour,
		idleTimeout: 90 * time.Second,
		sweep:       15 * time.Second,
	}
	go m.janitor()
	return m
}

// Start launches a new session for itemPath.
func (m *Manager) Start(itemID, itemPath string, opts Options) (*Session, error) {
	return m.start(randomID(), itemID, itemPath, opts)
}

// Ensure starts a session with a caller supplied id, or returns the existing
// one. The id is the Jellyfin PlaySessionId so playlists and progress reports
// can be correlated.
func (m *Manager) Ensure(sessionID, itemID, itemPath string, opts Options) (*Session, error) {
	if s, ok := m.Get(sessionID); ok {
		if s.Err() == "" && !s.Suspended() {
			return s, nil
		}
		m.Stop(sessionID)
	}
	return m.start(sessionID, itemID, itemPath, opts)
}

// segmentSeconds is the target HLS segment duration set in buildArgs.
const segmentSeconds = 4

const segmentTicks = int64(segmentSeconds) * 10_000_000

// Resume restarts a session that the janitor suspended for inactivity. from
// is the first segment number the client needs; a negative value continues
// after the last segment that was served.
func (m *Manager) Resume(id string, from int) (*Session, bool) {
	m.restartMu.Lock()
	defer m.restartMu.Unlock()

	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	if !s.suspended {
		s.mu.Unlock()
		return s, true
	}
	opts := s.opts
	itemID, itemPath := s.ItemID, s.itemPath
	base, baseNumber, last := s.StartTicks, opts.StartNumber, s.lastSegment
	s.mu.Unlock()

	opts.StartTicks, opts.StartNumber = resumeTarget(base, baseNumber, last, from)

	m.Stop(id)
	next, err := m.start(id, itemID, itemPath, opts)
	if err != nil {
		return nil, false
	}
	return next, true
}

// resumeTarget maps a segment number to a seek position. base is the current
// session's start position and baseNumber the first segment number it emits,
// so numbering stays absolute across repeated suspensions.
func resumeTarget(base int64, baseNumber, last, from int) (int64, int) {
	if from < 0 {
		from = last + 1
	}
	if from < baseNumber {
		from = baseNumber
	}
	ticks := base
	if delta := from - baseNumber; delta > 0 {
		ticks += int64(delta) * segmentTicks
	}
	return ticks, from
}

func (m *Manager) start(id, itemID, itemPath string, opts Options) (*Session, error) {
	ff := m.ffmpeg()
	if ff == "" {
		return nil, fmt.Errorf("ffmpeg is not available")
	}
	if _, err := os.Stat(itemPath); err != nil {
		return nil, fmt.Errorf("Media file is not readable: %w", err)
	}
	if opts.BitrateKbps <= 0 {
		opts.BitrateKbps = 8000
	}
	// The client supplies this value; keep a bad request from asking ffmpeg
	// for an absurd bitrate.
	if opts.BitrateKbps > 200_000 {
		opts.BitrateKbps = 200_000
	}
	if opts.Height <= 0 || opts.Height > 2160 {
		opts.Height = 1080
	}
	if !validPreset(opts.Preset) {
		opts.Preset = "veryfast"
	}
	opts.Codec = NormalizeCodec(opts.Codec)
	dir := filepath.Join(m.dir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Session{
		ID:          id,
		ItemID:      itemID,
		Dir:         dir,
		StartTicks:  opts.StartTicks,
		Bitrate:     opts.BitrateKbps,
		Height:      opts.Height,
		Width:       opts.Width,
		Copy:        opts.Copy,
		Codec:       opts.Codec,
		UserID:      opts.UserID,
		CreatedAt:   time.Now(),
		lastAccess:  time.Now(),
		lastSegment: opts.StartNumber - 1,
		done:        make(chan struct{}),
		opts:        opts,
		itemPath:    itemPath,
	}
	args := buildArgs(opts, itemPath, dir)
	cmd := exec.Command(ff, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("Could not start ffmpeg: %w", err)
	}
	s.cmd = cmd
	go func() {
		buf := bufio.NewScanner(stderr)
		for buf.Scan() {
			s.mu.Lock()
			if s.stderr.Len() < 4096 {
				s.stderr.WriteString(buf.Text())
				s.stderr.WriteString("\n")
			}
			s.mu.Unlock()
		}
	}()
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.finished = true
		if err != nil {
			s.err = strings.TrimSpace(s.stderr.String())
			if s.err == "" {
				s.err = err.Error()
			}
		}
		s.mu.Unlock()
		close(s.done)
	}()
	m.mu.Lock()
	if existing, ok := m.sessions[id]; ok {
		m.mu.Unlock()
		s.stop()
		_ = os.RemoveAll(dir)
		return existing, nil
	}
	m.sessions[id] = s
	m.mu.Unlock()
	return s, nil
}

// buildArgs assembles the ffmpeg command line for one session. VAAPI is used
// for both decoding and encoding when the caller selected hardware mode.
func buildArgs(opts Options, itemPath, dir string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	if opts.StartTicks > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", float64(opts.StartTicks)/10_000_000))
	}
	vaapi := !opts.Copy && opts.Hardware == "vaapi" && opts.HardwareDevice != ""
	qsv := !opts.Copy && opts.Hardware == "qsv" && opts.HardwareDevice != ""
	if vaapi {
		args = append(args,
			"-hwaccel", "vaapi",
			"-hwaccel_device", opts.HardwareDevice,
			"-hwaccel_output_format", "vaapi",
		)
	}
	if qsv {
		args = append(args,
			"-hwaccel", "qsv",
			"-qsv_device", opts.HardwareDevice,
			"-hwaccel_output_format", "qsv",
		)
		if decoder := qsvDecoder(opts.VideoCodec); decoder != "" {
			args = append(args, "-c:v", decoder)
		}
	}
	encoder := opts.Encoder
	if encoder == "" {
		encoder = "libx264"
	}
	codec := NormalizeCodec(opts.Codec)
	args = append(args, "-i", itemPath,
		"-map", "0:v:0",
		"-map", fmt.Sprintf("0:a:%d?", max(opts.AudioIndex-1, 0)),
	)
	switch {
	case opts.Copy:
		args = append(args,
			"-c", "copy",
			"-avoid_negative_ts", "make_zero",
		)
	case vaapi:
		args = append(args, "-c:v", codec+"_vaapi")
		args = append(args, profileArgs(codec)...)
		args = append(args,
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds),
			"-vf", fmt.Sprintf("scale_vaapi=w=-2:h=%d:format=nv12", opts.Height),
			"-b:v", fmt.Sprintf("%dk", opts.BitrateKbps*3/4),
			"-maxrate", fmt.Sprintf("%dk", opts.BitrateKbps),
			"-bufsize", fmt.Sprintf("%dk", opts.BitrateKbps*2),
			"-c:a", "aac",
			"-b:a", "160k",
			"-ac", "2",
		)
	case qsv:
		width, height := evenDimensions(opts.Width, opts.Height)
		args = append(args, "-c:v", codec+"_qsv")
		args = append(args, profileArgs(codec)...)
		args = append(args,
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds),
			"-vf", fmt.Sprintf("scale_qsv=w=%d:h=%d:format=nv12", width, height),
			"-b:v", fmt.Sprintf("%dk", opts.BitrateKbps*3/4),
			"-maxrate", fmt.Sprintf("%dk", opts.BitrateKbps),
			"-bufsize", fmt.Sprintf("%dk", opts.BitrateKbps*2),
			"-c:a", "aac",
			"-b:a", "160k",
			"-ac", "2",
		)
	default:
		args = append(args, "-c:v", encoder)
		if encoder == "libx264" || encoder == "libx265" {
			args = append(args, "-preset", opts.Preset)
		}
		args = append(args, profileArgs(codec)...)
		args = append(args,
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds),
			"-pix_fmt", "yuv420p",
			"-vf", fmt.Sprintf("scale=-2:%d", opts.Height),
			"-b:v", fmt.Sprintf("%dk", opts.BitrateKbps*3/4),
		)
		// SVT-AV1 only accepts maxrate together with CRF rate control.
		if encoder != "libsvtav1" {
			args = append(args,
				"-maxrate", fmt.Sprintf("%dk", opts.BitrateKbps),
				"-bufsize", fmt.Sprintf("%dk", opts.BitrateKbps*2),
			)
		}
		args = append(args,
			"-c:a", "aac",
			"-b:a", "160k",
			"-ac", "2",
		)
	}
	if !opts.Copy {
		if tag := objectTag(codec); tag != "" {
			args = append(args, "-tag:v", tag)
		}
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_list_size", "0",
		"-hls_playlist_type", "event",
		"-hls_flags", "independent_segments",
	)
	if opts.StartNumber > 0 {
		args = append(args, "-start_number", strconv.Itoa(opts.StartNumber))
	}
	if opts.Copy || codec == "h264" {
		args = append(args,
			"-hls_segment_filename", filepath.Join(dir, "seg%05d.ts"),
			filepath.Join(dir, "main.m3u8"),
		)
	} else {
		args = append(args,
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init.mp4",
			"-hls_segment_filename", filepath.Join(dir, "seg%05d.m4s"),
			filepath.Join(dir, "main.m3u8"),
		)
	}
	return args
}

// NormalizeCodec maps user/ffprobe spellings to the short names used for
// encoder selection. Unknown values fall back to H.264.
func NormalizeCodec(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "hevc", "h265", "hvc1", "hev1":
		return "hevc"
	case "av1", "av01":
		return "av1"
	case "vp9", "vp09":
		return "vp9"
	default:
		return "h264"
	}
}

// profileArgs keeps encoder profiles valid: AV1/VP9 have no "high" profile and
// their encoders reject the H.264 values.
func profileArgs(codec string) []string {
	switch codec {
	case "h264":
		return []string{"-profile:v", "high"}
	case "hevc":
		return []string{"-profile:v", "main"}
	}
	return nil
}

// objectTag sets the MP4 sample entry so players recognise HEVC and AV1.
func objectTag(codec string) string {
	switch codec {
	case "hevc":
		return "hvc1"
	case "av1":
		return "av01"
	}
	return ""
}

// FallbackSoftware restarts a failed hardware session with software encoding.
// It returns false once the session has already fallen back.
func (m *Manager) FallbackSoftware(id string) (*Session, bool) {
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	if s.opts.Hardware == "" || s.fallbackTried {
		s.mu.Unlock()
		return nil, false
	}
	s.fallbackTried = true
	opts := s.opts
	itemID, itemPath := s.ItemID, s.itemPath
	s.mu.Unlock()

	opts.Hardware = ""
	// A hardware-only codec has no software encoder to fall back to.
	if opts.Codec != "" && opts.Codec != "h264" && opts.Encoder == "" {
		opts.Codec = "h264"
	}
	m.Stop(id)
	next, err := m.start(id, itemID, itemPath, opts)
	if err != nil {
		return nil, false
	}
	return next, true
}

// Get resolves a session id.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// Touch refreshes the idle timer for a session, if it exists.
func (m *Manager) Touch(id string) {
	if s, ok := m.Get(id); ok {
		s.Touch()
	}
}

// Stop terminates the process and removes its directory.
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	s.stop()
	_ = os.RemoveAll(s.Dir)
}

// StopItem terminates sessions for an item.
func (m *Manager) StopItem(itemID string) {
	m.mu.Lock()
	ids := []string{}
	for id, s := range m.sessions {
		if s.ItemID == itemID {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

// StopItemForUser terminates a user's sessions for an item. Some clients omit
// PlaySessionId from their stop report, so the item and owner are the only
// reliable handle.
func (m *Manager) StopItemForUser(itemID, userID string) {
	m.mu.Lock()
	ids := []string{}
	for id, s := range m.sessions {
		if s.ItemID == itemID && s.UserID == userID {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

// suspend stops a session's ffmpeg process but keeps its options so the next
// request can resume transcoding. Keeping the entry also prevents explicit
// stops from leaking processes.
func (m *Manager) suspend(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.suspended || s.finished {
		s.mu.Unlock()
		return
	}
	s.suspended = true
	s.suspendedAt = time.Now()
	s.mu.Unlock()
	s.stop()
	_ = os.RemoveAll(s.Dir)
	_ = os.MkdirAll(s.Dir, 0o755)
}

// StopAll terminates every session (shutdown).
func (m *Manager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

func (s *Session) stop() {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
	}
}

// Err returns the ffmpeg failure, if any.
func (s *Session) Err() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// SegmentPath validates and resolves a file inside the session folder.
func (s *Session) SegmentPath(name string) (string, bool) {
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	if !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".m3u8") &&
		!strings.HasSuffix(name, ".mp4") && !strings.HasSuffix(name, ".m4s") {
		return "", false
	}
	p := filepath.Join(s.Dir, name)
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", false
	}
	return p, true
}

// Touch marks the session as still in use so the janitor leaves it alone.
func (s *Session) Touch() {
	s.mu.Lock()
	s.lastAccess = time.Now()
	s.mu.Unlock()
}

// TouchSegment records the last segment served and refreshes the idle timer.
func (s *Session) TouchSegment(index int) {
	s.mu.Lock()
	s.lastAccess = time.Now()
	if index > s.lastSegment {
		s.lastSegment = index
	}
	s.mu.Unlock()
}

// Suspended reports whether ffmpeg was stopped for inactivity. The session can
// be restarted with Manager.Resume.
func (s *Session) Suspended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.suspended
}

// WaitForSegment blocks until ffmpeg has written name. It is used when a
// suspended session is resumed for a segment the client already asked for.
func (s *Session) WaitForSegment(name string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if p, ok := s.SegmentPath(name); ok {
			if st, err := os.Stat(p); err == nil && st.Size() > 0 {
				return p, true
			}
		}
		if s.Err() != "" {
			return "", false
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// SegmentIndex extracts the sequence number from seg00042.ts / seg00042.m4s.
func SegmentIndex(name string) int {
	if !strings.HasPrefix(name, "seg") {
		return -1
	}
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".ts"), ".m4s")
	n, err := strconv.Atoi(strings.TrimPrefix(stem, "seg"))
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// WaitForPlaylist blocks briefly until ffmpeg has written the media playlist.
func (s *Session) WaitForPlaylist(timeout time.Duration) (string, bool) {
	path := filepath.Join(s.Dir, "main.m3u8")
	deadline := time.Now().Add(timeout)
	for {
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			return path, true
		}
		if s.Err() != "" {
			return "", false
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(120 * time.Millisecond)
	}
}

// Master builds a master playlist pointing at the session's media playlist.
func (s *Session) Master(token string) string {
	query := ""
	if token != "" {
		query = "?api_key=" + token
	}
	width := s.Width
	if width <= 0 {
		width = s.Height * 16 / 9
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	if !s.Copy && s.Codec != "" && s.Codec != "h264" {
		b.WriteString("#EXT-X-VERSION:7\n")
	} else {
		b.WriteString("#EXT-X-VERSION:3\n")
	}
	if s.Copy {
		b.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n",
			s.Bitrate*1000, width, s.Height))
	} else {
		b.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"%s,mp4a.40.2\"\n",
			s.Bitrate*1000, width, s.Height, codecString(s.Codec)))
	}
	b.WriteString("hls/" + s.ID + "/main.m3u8" + query + "\n")
	return b.String()
}

// codecString is the RFC 6381 identifier for the master playlist.
func codecString(codec string) string {
	switch NormalizeCodec(codec) {
	case "hevc":
		return "hvc1.1.6.L93.B0"
	case "av1":
		return "av01.0.05M.08"
	case "vp9":
		return "vp09.00.10.08"
	default:
		return "avc1.4d401f"
	}
}

func (m *Manager) janitor() {
	sweep := m.sweep
	if sweep <= 0 {
		sweep = 15 * time.Second
	}
	t := time.NewTicker(sweep)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		m.mu.Lock()
		stale := []string{}
		idle := []string{}
		for id, s := range m.sessions {
			s.mu.Lock()
			switch {
			case s.suspended:
				if now.Sub(s.suspendedAt) > m.maxAge {
					stale = append(stale, id)
				}
			case s.finished:
				// ffmpeg already exited; only the playlist is left to serve.
				if now.Sub(s.CreatedAt) > m.maxAge {
					stale = append(stale, id)
				}
			case now.Sub(s.lastAccess) > m.idleTimeout:
				idle = append(idle, id)
			}
			s.mu.Unlock()
		}
		m.mu.Unlock()
		for _, id := range stale {
			m.Stop(id)
		}
		for _, id := range idle {
			m.suspend(id)
		}
	}
}

// qsvDecoder maps a source codec to its Intel QSV decoder.
func qsvDecoder(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "h264", "avc", "avc1":
		return "h264_qsv"
	case "hevc", "h265":
		return "hevc_qsv"
	case "vp9":
		return "vp9_qsv"
	case "av1", "av01":
		return "av1_qsv"
	case "mpeg2video", "mpeg2":
		return "mpeg2_qsv"
	case "vc1":
		return "vc1_qsv"
	case "mjpeg", "jpeg":
		return "mjpeg_qsv"
	}
	return ""
}

// evenDimensions keeps QSV/VAAPI scaling on even pixel boundaries.
func evenDimensions(width, height int) (int, int) {
	if height <= 0 {
		height = 1080
	}
	if width <= 0 {
		width = height * 16 / 9
	}
	width &^= 1
	height &^= 1
	if width < 2 {
		width = 2
	}
	if height < 2 {
		height = 2
	}
	return width, height
}

var presets = map[string]bool{
	"ultrafast": true, "superfast": true, "veryfast": true, "faster": true,
	"fast": true, "medium": true, "slow": true, "slower": true,
	"veryslow": true, "placebo": true,
}

func validPreset(name string) bool {
	return presets[strings.ToLower(strings.TrimSpace(name))]
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ParseInt is a small helper for query parameters.
func ParseInt(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
