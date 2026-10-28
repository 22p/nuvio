package hls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildArgsSoftware(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps: 4000,
		Height:      720,
		Preset:      "veryfast",
		Encoder:     "libx264",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	if !strings.Contains(got, "-c:v libx264") {
		t.Fatalf("software encoder missing: %s", got)
	}
	if !strings.Contains(got, "-preset veryfast") {
		t.Fatalf("software preset missing: %s", got)
	}
	if !strings.Contains(got, "-vf scale=-2:720") {
		t.Fatalf("software scale missing: %s", got)
	}
	if strings.Contains(got, "-hwaccel") {
		t.Fatalf("software session must not use hwaccel: %s", got)
	}
}

func TestBuildArgsVAAPI(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps:    4000,
		Height:         1080,
		Hardware:       "vaapi",
		HardwareDevice: "/dev/dri/renderD128",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	for _, want := range []string{
		"-hwaccel vaapi",
		"-hwaccel_device /dev/dri/renderD128",
		"-hwaccel_output_format vaapi",
		"-c:v h264_vaapi",
		"-vf scale_vaapi=w=-2:h=1080:format=nv12",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "libx264") {
		t.Fatalf("hardware session must not use libx264: %s", got)
	}
}

func TestBuildArgsQSV(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps:    4000,
		Width:          1281,
		Height:         721,
		Hardware:       "qsv",
		HardwareDevice: "/dev/dri/renderD128",
		VideoCodec:     "h264",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	if strings.Index(got, "-c:v h264_qsv") > strings.Index(got, "-i /media/movie.mkv") {
		t.Fatalf("QSV decoder must be selected before the input: %s", got)
	}
	for _, want := range []string{
		"-hwaccel qsv",
		"-qsv_device /dev/dri/renderD128",
		"-hwaccel_output_format qsv",
		"-c:v h264_qsv",
		"-vf scale_qsv=w=1280:h=720:format=nv12",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "libx264") || strings.Contains(got, "vaapi") {
		t.Fatalf("QSV session mixed encoders: %s", got)
	}
}

func TestBuildArgsCopyIgnoresHardware(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps:    4000,
		Height:         720,
		Hardware:       "vaapi",
		HardwareDevice: "/dev/dri/renderD128",
		Copy:           true,
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	if !strings.Contains(got, "-c copy") {
		t.Fatalf("copy mode missing: %s", got)
	}
	if strings.Contains(got, "-hwaccel") {
		t.Fatalf("copy mode must not decode through VAAPI: %s", got)
	}
}

func TestBuildArgsAV1Software(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps: 4000,
		Height:      1080,
		Codec:       "av1",
		Encoder:     "libsvtav1",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	for _, want := range []string{
		"-c:v libsvtav1",
		"-tag:v av01",
		"-hls_segment_type fmp4",
		"-hls_segment_filename /tmp/session/seg%05d.m4s",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "-maxrate") {
		t.Fatalf("SVT-AV1 rejects maxrate without CRF: %s", got)
	}
	if strings.Contains(got, "seg%05d.ts") {
		t.Fatalf("AV1 cannot be muxed into MPEG-TS: %s", got)
	}
}

func TestBuildArgsHEVCVAAPI(t *testing.T) {
	args := buildArgs(Options{
		BitrateKbps:    4000,
		Height:         1080,
		Hardware:       "vaapi",
		HardwareDevice: "/dev/dri/renderD128",
		Codec:          "hevc",
	}, "/media/movie.mkv", "/tmp/session")
	got := strings.Join(args, " ")

	for _, want := range []string{
		"-c:v hevc_vaapi",
		"-profile:v main",
		"-tag:v hvc1",
		"-hls_segment_type fmp4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "profile:v high") {
		t.Fatalf("HEVC must not use the H.264 high profile: %s", got)
	}
}

func TestNormalizeCodec(t *testing.T) {
	cases := map[string]string{
		"":      "h264",
		"H264":  "h264",
		"h265":  "hevc",
		"hvc1":  "hevc",
		"av01":  "av1",
		"VP9":   "vp9",
		"mpeg2": "h264",
	}
	for in, want := range cases {
		if got := NormalizeCodec(in); got != want {
			t.Fatalf("NormalizeCodec(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSegmentIndex(t *testing.T) {
	cases := map[string]int{
		"seg00000.ts":  0,
		"seg00042.ts":  42,
		"seg00317.m4s": 317,
		"init.mp4":     -1,
		"main.m3u8":    -1,
		"segabc.ts":    -1,
	}
	for name, want := range cases {
		if got := SegmentIndex(name); got != want {
			t.Fatalf("SegmentIndex(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestResumeTarget(t *testing.T) {
	// Fresh session: next segment after the last served.
	ticks, number := resumeTarget(0, 0, 5, -1)
	if number != 6 || ticks != 6*segmentTicks {
		t.Fatalf("fresh resume = %d %d", ticks, number)
	}
	// A resumed session keeps absolute numbering: segment 105 is at 20s.
	ticks, number = resumeTarget(0, 100, 105, -1)
	if number != 106 || ticks != 6*segmentTicks {
		t.Fatalf("nested resume = %d %d", ticks, number)
	}
	// Explicit segment requests resume at that segment.
	ticks, number = resumeTarget(0, 0, 5, 42)
	if number != 42 || ticks != 42*segmentTicks {
		t.Fatalf("explicit resume = %d %d", ticks, number)
	}
	// Never move before the session's first segment.
	ticks, number = resumeTarget(30*10_000_000, 100, 105, 3)
	if number != 100 || ticks != 30*10_000_000 {
		t.Fatalf("clamped resume = %d %d", ticks, number)
	}
}

func TestSuspendKeepsSession(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "s")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "seg00000.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{dir: dir, sessions: map[string]*Session{}, ffmpeg: func() string { return "" }}
	s := &Session{ID: "s", ItemID: "i", UserID: "u", Dir: sessDir, done: make(chan struct{})}
	m.sessions["s"] = s

	m.suspend("s")

	if !s.Suspended() {
		t.Fatal("session should be suspended")
	}
	if _, ok := m.Get("s"); !ok {
		t.Fatal("suspended session must stay in the map for resume")
	}
	if _, err := os.Stat(filepath.Join(sessDir, "seg00000.ts")); !os.IsNotExist(err) {
		t.Fatalf("segments should be removed, stat err = %v", err)
	}
}

func TestStopItemForUser(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{dir: dir, sessions: map[string]*Session{}, ffmpeg: func() string { return "" }}
	for _, s := range []*Session{
		{ID: "a", ItemID: "x", UserID: "u", Dir: filepath.Join(dir, "a"), done: make(chan struct{})},
		{ID: "b", ItemID: "x", UserID: "other", Dir: filepath.Join(dir, "b"), done: make(chan struct{})},
		{ID: "c", ItemID: "y", UserID: "u", Dir: filepath.Join(dir, "c"), done: make(chan struct{})},
	} {
		m.sessions[s.ID] = s
	}

	m.StopItemForUser("x", "u")

	if _, ok := m.Get("a"); ok {
		t.Fatal("owner's session should be stopped")
	}
	if _, ok := m.Get("b"); !ok {
		t.Fatal("another user's session must survive")
	}
	if _, ok := m.Get("c"); !ok {
		t.Fatal("other items must survive")
	}
}

func TestJanitorSuspendsIdleSession(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-ffmpeg.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcode := filepath.Join(dir, "transcode")
	if err := os.MkdirAll(transcode, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		dir:         transcode,
		sessions:    map[string]*Session{},
		ffmpeg:      func() string { return script },
		maxAge:      time.Hour,
		idleTimeout: 100 * time.Millisecond,
		sweep:       20 * time.Millisecond,
	}
	go m.janitor()
	defer m.StopAll()

	sess, err := m.start("idle", "item", media, Options{UserID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !sess.Suspended() {
		time.Sleep(20 * time.Millisecond)
	}
	if !sess.Suspended() {
		t.Fatal("idle session was not suspended")
	}
	sess.mu.Lock()
	finished := sess.finished
	sess.mu.Unlock()
	if !finished {
		t.Fatal("ffmpeg process should have been reaped")
	}
	if _, ok := m.Get("idle"); !ok {
		t.Fatal("suspended session should stay for resume")
	}
}

func TestJanitorKeepsActiveSession(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-ffmpeg.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcode := filepath.Join(dir, "transcode")
	if err := os.MkdirAll(transcode, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		dir:         transcode,
		sessions:    map[string]*Session{},
		ffmpeg:      func() string { return script },
		idleTimeout: 300 * time.Millisecond,
		sweep:       20 * time.Millisecond,
	}
	go m.janitor()
	defer m.StopAll()

	sess, err := m.start("active", "item", media, Options{UserID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		time.Sleep(40 * time.Millisecond)
		sess.Touch()
	}
	if sess.Suspended() {
		t.Fatal("touched session must not be suspended")
	}
}

func TestResumeRestartsSuspendedSession(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-ffmpeg.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcode := filepath.Join(dir, "transcode")
	if err := os.MkdirAll(transcode, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{dir: transcode, sessions: map[string]*Session{}, ffmpeg: func() string { return script }}
	defer m.StopAll()

	sess, err := m.start("resume", "item", media, Options{UserID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	m.suspend("resume")
	if !sess.Suspended() {
		t.Fatal("session should be suspended")
	}

	next, ok := m.Resume("resume", 5)
	if !ok {
		t.Fatal("resume failed")
	}
	if next.Suspended() {
		t.Fatal("resumed session must be running")
	}
	if want := int64(5) * segmentTicks; next.StartTicks != want {
		t.Fatalf("resumed StartTicks = %d, want %d", next.StartTicks, want)
	}
	if next.opts.StartNumber != 5 {
		t.Fatalf("resumed StartNumber = %d, want 5", next.opts.StartNumber)
	}
}
