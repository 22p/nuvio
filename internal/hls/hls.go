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
	Encoder     string
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
	CreatedAt  time.Time

	mu     sync.Mutex
	cmd    *exec.Cmd
	err    string
	stderr strings.Builder
	done   chan struct{}
}

// Manager owns active sessions.
type Manager struct {
	mu       sync.Mutex
	dir      string
	sessions map[string]*Session
	ffmpeg   func() string
	maxAge   time.Duration
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
	m := &Manager{dir: dir, sessions: map[string]*Session{}, ffmpeg: ffmpeg, maxAge: 3 * time.Hour}
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
		if s.Err() == "" {
			return s, nil
		}
		m.Stop(sessionID)
	}
	return m.start(sessionID, itemID, itemPath, opts)
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
	if opts.Height <= 0 || opts.Height > 2160 {
		opts.Height = 1080
	}
	if opts.Preset == "" {
		opts.Preset = "veryfast"
	}
	dir := filepath.Join(m.dir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Session{
		ID:         id,
		ItemID:     itemID,
		Dir:        dir,
		StartTicks: opts.StartTicks,
		Bitrate:    opts.BitrateKbps,
		Height:     opts.Height,
		Width:      opts.Width,
		Copy:       opts.Copy,
		CreatedAt:  time.Now(),
		done:       make(chan struct{}),
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	if opts.StartTicks > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", float64(opts.StartTicks)/10_000_000))
	}
	encoder := opts.Encoder
	if encoder == "" {
		encoder = "libx264"
	}
	args = append(args, "-i", itemPath,
		"-map", "0:v:0",
		"-map", fmt.Sprintf("0:a:%d?", max(opts.AudioIndex-1, 0)),
	)
	if opts.Copy {
		args = append(args,
			"-c", "copy",
			"-avoid_negative_ts", "make_zero",
		)
	} else {
		args = append(args, "-c:v", encoder)
		if encoder == "libx264" {
			args = append(args, "-preset", opts.Preset)
		}
		args = append(args,
			"-force_key_frames", "expr:gte(t,n_forced*4)",
			"-profile:v", "high",
			"-pix_fmt", "yuv420p",
			"-vf", fmt.Sprintf("scale=-2:%d", opts.Height),
			"-b:v", fmt.Sprintf("%dk", opts.BitrateKbps*3/4),
			"-maxrate", fmt.Sprintf("%dk", opts.BitrateKbps),
			"-bufsize", fmt.Sprintf("%dk", opts.BitrateKbps*2),
			"-c:a", "aac",
			"-b:a", "160k",
			"-ac", "2",
		)
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", "4",
		"-hls_list_size", "0",
		"-hls_playlist_type", "event",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.ts"),
		filepath.Join(dir, "main.m3u8"),
	)
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

// Get resolves a session id.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
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
	if !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".m3u8") && !strings.HasSuffix(name, ".mp4") {
		return "", false
	}
	p := filepath.Join(s.Dir, name)
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return "", false
	}
	return p, true
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
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	if s.Copy {
		b.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n",
			s.Bitrate*1000, width, s.Height))
	} else {
		b.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.4d401f,mp4a.40.2\"\n",
			s.Bitrate*1000, width, s.Height))
	}
	b.WriteString("hls/" + s.ID + "/main.m3u8" + query + "\n")
	return b.String()
}

func (m *Manager) janitor() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		m.mu.Lock()
		stale := []string{}
		for id, s := range m.sessions {
			if time.Since(s.CreatedAt) > m.maxAge {
				stale = append(stale, id)
			}
		}
		m.mu.Unlock()
		for _, id := range stale {
			m.Stop(id)
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
