package hls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hlsTestMedia(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Without ffmpeg the manager must fail the request rather than register a
// session that can never produce segments.
func TestStartWithoutFFmpegFailsCleanly(t *testing.T) {
	root := t.TempDir()
	m := New(root, func() string { return "" })
	defer m.StopAll()

	if _, err := m.Start("item", hlsTestMedia(t), Options{UserID: "u"}); err == nil {
		t.Fatal("expected an error when ffmpeg is unavailable")
	}
	if _, err := m.Ensure("abc", "item", hlsTestMedia(t), Options{UserID: "u"}); err == nil {
		t.Fatal("Ensure should fail as well")
	}
	m.mu.Lock()
	n := len(m.sessions)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("failed starts registered %d sessions", n)
	}
}

// A binary that cannot be executed leaves no half-built session directory.
func TestStartCleansSessionDirectoryWhenExecFails(t *testing.T) {
	root := t.TempDir()
	m := New(root, func() string { return filepath.Join(root, "no-such-ffmpeg") })
	defer m.StopAll()

	if _, err := m.Start("item", hlsTestMedia(t), Options{UserID: "u"}); err == nil {
		t.Fatal("expected exec failure")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return // the root itself was cleaned up, which is fine
	}
	if len(entries) != 0 {
		t.Fatalf("session directory left behind: %v", entries)
	}
}

// ffmpeg dying mid-transcode must be recorded on the session, not crash or
// wedge the manager. The session stays addressable for the playlist and can be
// replaced by a new one afterwards.
func TestFFmpegCrashIsContained(t *testing.T) {
	dir := t.TempDir()
	crashing := filepath.Join(dir, "ffmpeg-crash.sh")
	if err := os.WriteFile(crashing, []byte("#!/bin/sh\necho 'encoder exploded' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(filepath.Join(dir, "transcode"), func() string { return crashing })
	defer m.StopAll()

	sess, err := m.Start("item", media, Options{UserID: "u"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && sess.Err() == "" {
		time.Sleep(10 * time.Millisecond)
	}
	if sess.Err() == "" {
		t.Fatal("crash was not recorded on the session")
	}
	if !strings.Contains(sess.Err(), "encoder exploded") && !strings.Contains(sess.Err(), "exit status") {
		t.Fatalf("error text = %q", sess.Err())
	}
	if _, ok := sess.SegmentPath("index.m3u8"); ok {
		t.Fatal("no playlist should exist after the crash")
	}
	if _, ok := m.Get(sess.ID); !ok {
		t.Fatal("crashed session should remain addressable until cleaned up")
	}

	m.Stop(sess.ID)
	if _, ok := m.Get(sess.ID); ok {
		t.Fatal("Stop did not remove the crashed session")
	}

	// A fresh session must still be startable with a healthy binary.
	healthy := filepath.Join(dir, "ffmpeg-ok.sh")
	if err := os.WriteFile(healthy, []byte("#!/bin/sh\ntrap 'exit 0' TERM\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.ffmpeg = func() string { return healthy }
	if _, err := m.Start("item", media, Options{UserID: "u"}); err != nil {
		t.Fatalf("second start after a crash: %v", err)
	}
}

// Losing the session directory (tmp cleaner, manual rm) must degrade to
// "segment not found" rather than a panic or a path escape.
func TestSegmentPathAfterDirectoryRemoval(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "s")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seg := filepath.Join(sessDir, "seg00000.m4s")
	if err := os.WriteFile(seg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "s", Dir: sessDir, done: make(chan struct{})}
	if p, ok := sess.SegmentPath("seg00000.m4s"); !ok || p != seg {
		t.Fatalf("segment = %q %v", p, ok)
	}
	if err := os.RemoveAll(sessDir); err != nil {
		t.Fatal(err)
	}
	if _, ok := sess.SegmentPath("seg00000.m4s"); ok {
		t.Fatal("missing directory must not resolve a segment")
	}
	if _, ok := sess.SegmentPath("../nuvio.db"); ok {
		t.Fatal("traversal must stay rejected")
	}
}
