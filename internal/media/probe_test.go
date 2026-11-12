package media

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsASSSubtitleCodec(t *testing.T) {
	for _, codec := range []string{"ass", "ASS", " ssa ", "Ass"} {
		if !IsASSSubtitleCodec(codec) {
			t.Fatalf("%q should be treated as ASS", codec)
		}
	}
	for _, codec := range []string{"", "subrip", "webvtt", "hdmv_pgs_subtitle", "mov_text"} {
		if IsASSSubtitleCodec(codec) {
			t.Fatalf("%q should not be treated as ASS", codec)
		}
	}
}

func TestEmbeddedSubtitleToVTTUsesASSForPosition(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffmpeg is a shell script")
	}
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg.sh")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "ass" ]; then
    cat <<'ASS'
[Script Info]
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Alignment, MarginL, MarginR, MarginV
Style: Default,8,10,10,40

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\pos(960,100)}Screen text
ASS
    exit 0
  fi
done
printf 'WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nplain text\n'
`
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := EmbeddedSubtitleToVTT(context.Background(), ffmpeg, "/media/movie.mkv", 2, "ass")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "Screen text") {
		t.Fatalf("screen text missing:\n%s", got)
	}
	if !strings.Contains(got, "line:9.3% line-align:start position:50.0% align:center") {
		t.Fatalf("ASS position was flattened:\n%s", got)
	}

	out, err = EmbeddedSubtitleToVTT(context.Background(), ffmpeg, "/media/movie.mkv", 2, "subrip")
	if err != nil {
		t.Fatal(err)
	}
	got = string(out)
	if !strings.Contains(got, "plain text") || strings.Contains(got, "position:") {
		t.Fatalf("non-ASS stream should use the webvtt path:\n%s", got)
	}
}

func TestEmbeddedSubtitleToVTTFallsBackWhenASSExtractionFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffmpeg is a shell script")
	}
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg.sh")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "ass" ]; then
    echo "ass encoder unavailable" >&2
    exit 1
  fi
done
printf 'WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nfallback text\n'
`
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := EmbeddedSubtitleToVTT(context.Background(), ffmpeg, "/media/movie.mkv", 2, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "fallback text") {
		t.Fatalf("fallback WebVTT missing:\n%s", out)
	}
}
