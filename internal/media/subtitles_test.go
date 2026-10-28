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

func TestASSBytesToVTTKeepsPositioning(t *testing.T) {
	script := `[Script Info]
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,40,1
Style: Sign,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,8,10,10,20,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello\Nworld
Dialogue: 0,0:00:04.00,0:00:06.00,Sign,,0,0,0,,{\pos(960,100)}Top sign
Dialogue: 0,0:00:07.00,0:00:09.00,Default,,0,0,0,,{\an8}Another top
Dialogue: 0,0:00:10.00,0:00:12.00,Default,,0,0,0,,{\p1}m 0 0 l 100 0 100 100 0 100{\p0}
`
	got := string(ASSBytesToVTT([]byte(script)))
	for _, want := range []string{
		"line:96.3% line-align:end position:50.0% align:center",  // bottom, MarginV 40 of 1080
		"line:9.3% line-align:start position:50.0% align:center", // \pos(960,100) on the top-aligned Sign style
		"line:3.7% line-align:start position:50.0% align:center", // \an8 (top centre)
		"Hello\nworld",
		"Top sign",
		"Another top",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("converted VTT missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\\pos") || strings.Contains(got, "\\an8") {
		t.Fatalf("override tags leaked into VTT:\n%s", got)
	}
	if strings.Contains(got, "m 0 0 l 100") {
		t.Fatalf("vector drawing leaked into VTT:\n%s", got)
	}
}

func TestSubtitleLanguageCodes(t *testing.T) {
	cases := []struct{ file, base, want string }{
		{"Konosuba S1E01.chi.ass", "Konosuba S1E01", "zh"},
		{"Konosuba S1E01.zho.ass", "Konosuba S1E01", "zh"},
		{"Konosuba S1E01.chs.ass", "Konosuba S1E01", "zh-cn"},
		{"Konosuba S1E01.cht.ass", "Konosuba S1E01", "zh-tw"},
		{"Konosuba S1E01.eng.ass", "Konosuba S1E01", "en"},
		{"Konosuba S1E01.jpn.ass", "Konosuba S1E01", "ja"},
		{"Konosuba S1E01.ja.ass", "Konosuba S1E01", "ja"},
		{"Konosuba S1E01.zh-Hans.ass", "Konosuba S1E01", "zh-hans"},
		{"Konosuba S1E01.en.forced.ass", "Konosuba S1E01", "en"},
		{"Konosuba S1E01.ass", "Konosuba S1E01", "und"},
	}
	for _, c := range cases {
		if got := DetectSubtitleLanguage(c.file, c.base); got != c.want {
			t.Fatalf("DetectSubtitleLanguage(%q) = %q, want %q", c.file, got, c.want)
		}
	}
	if got := LanguageName("chi"); got != "Chinese" {
		t.Fatalf("LanguageName(chi) = %q", got)
	}
	if got := LanguageName("zho"); got != "Chinese" {
		t.Fatalf("LanguageName(zho) = %q", got)
	}
	if got := LanguageName("ger"); got != "German" {
		t.Fatalf("LanguageName(ger) = %q", got)
	}
}

func TestSubtitleLanguageFromContent(t *testing.T) {
	if got := DetectSubtitleTextLanguage([]byte("在世界的尽头，光仍然记得回来的路。这里还有更多中文对白内容。")); got != "zh" {
		t.Fatalf("chinese text = %q, want zh", got)
	}
	if got := DetectSubtitleTextLanguage([]byte("これは日本語の字幕です。かな文字が含まれているので日本語と判定します。")); got != "ja" {
		t.Fatalf("japanese text = %q, want ja", got)
	}
	if got := DetectSubtitleTextLanguage([]byte("This is simply an English subtitle line.")); got != "" {
		t.Fatalf("latin text = %q, want empty", got)
	}
}

// Sidecars without a language suffix fall back to the script used in the file.
func TestSubtitleLanguageFallbackInScan(t *testing.T) {
	dir := t.TempDir()
	base := "Konosuba S1E01"
	plain := filepath.Join(dir, base+".ass")
	chi := filepath.Join(dir, base+".chi.ass")
	body := "[Script Info]\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,在世界的尽头，光仍然记得回来的路。这里还有更多中文对白内容。\n"
	if err := os.WriteFile(plain, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chi, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	subs := matchSubtitles(dir, base, []string{plain, chi})
	if len(subs) != 2 {
		t.Fatalf("subs = %d, want 2", len(subs))
	}
	for _, s := range subs {
		if s.Language != "zh" {
			t.Fatalf("%s language = %q, want zh", filepath.Base(s.Path), s.Language)
		}
		if s.Title != "Chinese" {
			t.Fatalf("%s title = %q", filepath.Base(s.Path), s.Title)
		}
	}
}
