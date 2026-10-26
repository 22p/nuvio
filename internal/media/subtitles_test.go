package media

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nuvio/internal/model"
)

func TestSRTBytesToVTT(t *testing.T) {
	in := "1\r\n00:00:01,000 --> 00:00:04,000 X1:10\r\nHello & <world>\r\n\r\n2\r\n00:01:05,500 --> 00:01:07,000\r\nSecond line\r\n"
	out := string(SRTBytesToVTT([]byte(in)))
	if !strings.HasPrefix(out, "WEBVTT") {
		t.Fatalf("missing WEBVTT header: %q", out)
	}
	if !strings.Contains(out, "00:00:01.000 --> 00:00:04.000") {
		t.Fatalf("timestamp not converted: %q", out)
	}
	if !strings.Contains(out, "00:01:05.500 --> 00:01:07.000") {
		t.Fatalf("second timestamp not converted: %q", out)
	}
	if !strings.Contains(out, "Hello &amp; &lt;world&gt;") {
		t.Fatalf("html not escaped: %q", out)
	}
}

func TestASSBytesToVTT(t *testing.T) {
	in := `[Script Info]
Title: test
[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.50,Default,,0,0,0,,{\an8}Hello\Nworld
Dialogue: 0,0:00:04.00,0:00:05.00,Default,,0,0,0,,Second, with comma
`
	out := string(ASSBytesToVTT([]byte(in)))
	if !strings.Contains(out, "00:00:01.000 --> 00:00:03.500") {
		t.Fatalf("ass timing not converted: %q", out)
	}
	if !strings.Contains(out, "Hello\nworld") {
		t.Fatalf("line break not converted: %q", out)
	}
	if strings.Contains(out, "{\\an8}") {
		t.Fatalf("override tags not stripped: %q", out)
	}
	if !strings.Contains(out, "Second, with comma") {
		t.Fatalf("comma in text lost: %q", out)
	}
}

func TestDetectSubtitleLanguage(t *testing.T) {
	cases := []struct{ file, base, want string }{
		{"Movie.2020.en.srt", "Movie.2020", "en"},
		{"Movie.2020.zh-CN.ass", "Movie.2020", "zh-cn"},
		{"Movie.2020.forced.srt", "Movie.2020", "und"},
		{"Show.S01E01.zh.srt", "Show.S01E01", "zh"},
	}
	for _, c := range cases {
		if got := DetectSubtitleLanguage(c.file, c.base); got != c.want {
			t.Errorf("%s: got %q want %q", c.file, got, c.want)
		}
	}
}

func TestMP4Duration(t *testing.T) {
	mvhd := make([]byte, 100)
	mvhd[0] = 0 // version 0
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], 52209)
	data := append(mp4Box("ftyp", []byte("isom0000")), mp4Box("moov", mp4Box("mvhd", mvhd))...)
	got := MP4Duration(newReader(data))
	if got != 522090000 {
		t.Fatalf("duration = %d, want 522090000", got)
	}
}

func TestScanMoviesAndShows(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "movies", "My Movie (2021)")
	if err := os.MkdirAll(movieDir, 0o755); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(movieDir, "My Movie (2021).mp4")
	_ = os.WriteFile(video, []byte("not really a movie"), 0o644)
	_ = os.WriteFile(filepath.Join(movieDir, "My Movie (2021).en.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), 0o644)
	_ = os.WriteFile(filepath.Join(movieDir, "poster.jpg"), []byte("jpg"), 0o644)

	sc := Scanner{}
	items, err := sc.Scan(context.Background(), &model.Library{
		ID: "lib-movies", Name: "Movies", Type: model.LibraryMovies,
		Paths: []string{filepath.Join(root, "movies")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d movie items", len(items))
	}
	m := items[0]
	if m.Name != "My Movie" || m.Year != 2021 {
		t.Fatalf("title/year = %q/%d", m.Name, m.Year)
	}
	if len(m.Media.Subtitles) != 1 || m.Media.Subtitles[0].Language != "en" {
		t.Fatalf("subtitles = %+v", m.Media.Subtitles)
	}
	if m.PosterPath == "" {
		t.Fatal("sidecar poster not detected")
	}

	shows := filepath.Join(root, "shows")
	season := filepath.Join(shows, "Show A", "Season 01")
	_ = os.MkdirAll(season, 0o755)
	for _, name := range []string{"Show A S01E01.mkv", "Show A S01E02 The Beginning.mkv"} {
		_ = os.WriteFile(filepath.Join(season, name), []byte("x"), 0o644)
	}
	// Flat episodes in the library root must not merge into one series.
	_ = os.WriteFile(filepath.Join(shows, "Flat.Show.S01E01.mkv"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(shows, "Other.Show.S01E01.mkv"), []byte("x"), 0o644)

	items, err = sc.Scan(context.Background(), &model.Library{
		ID: "lib-shows", Name: "Shows", Type: model.LibraryShows,
		Paths: []string{shows},
	})
	if err != nil {
		t.Fatal(err)
	}
	series, seasons, episodes := 0, 0, 0
	var showA *model.Item
	for _, it := range items {
		switch it.Type {
		case model.TypeSeries:
			series++
			if it.Name == "Show A" {
				showA = it
			}
		case model.TypeSeason:
			seasons++
		case model.TypeEpisode:
			episodes++
			if it.SeriesName == "Show A" && it.Name != "The Beginning" && !strings.HasPrefix(it.Name, "Episode") {
				t.Errorf("unexpected episode title %q", it.Name)
			}
		}
	}
	if series != 3 {
		t.Fatalf("series = %d, want 3 (flat episodes must be separate series)", series)
	}
	if seasons != 3 {
		t.Fatalf("seasons = %d, want 3", seasons)
	}
	if episodes != 4 {
		t.Fatalf("episodes = %d, want 4", episodes)
	}
	if showA == nil || showA.ChildCount != 1 {
		t.Fatalf("show A not grouped correctly: %+v", showA)
	}
}

func mp4Box(kind string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b[0:4], uint32(len(b)))
	copy(b[4:8], kind)
	copy(b[8:], payload)
	return b
}

type sliceReadSeeker struct {
	data []byte
	pos  int
}

func newReader(b []byte) *sliceReadSeeker { return &sliceReadSeeker{data: b} }

func (r *sliceReadSeeker) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *sliceReadSeeker) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case 0:
		r.pos = int(off)
	case 1:
		r.pos += int(off)
	case 2:
		r.pos = len(r.data) + int(off)
	}
	if r.pos < 0 {
		r.pos = 0
	}
	return int64(r.pos), nil
}
