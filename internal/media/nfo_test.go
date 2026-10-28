package media

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"nuvio/internal/model"
)

func TestMovieNFO(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "movie.nfo"), `<movie>
  <title>The Matrix</title>
  <plot>A hacker discovers reality.</plot>
  <tagline>Free your mind.</tagline>
  <year>1999</year>
  <rating>8.7</rating>
  <mpaa>R</mpaa>
  <genre>Action</genre>
  <genre>Science Fiction</genre>
  <actor><name>Keanu Reeves</name><role>Neo</role><thumb>https://example.com/keanu.jpg</thumb></actor>
</movie>`)

	it := &model.Item{Name: "The.Matrix.1999.1080p", Type: model.TypeMovie}
	applyNFO(it, movieNFO(&videoEntry{dir: dir, base: "The.Matrix.1999.1080p"}))

	if it.MetadataSource != "nfo" || it.Name != "The Matrix" || it.SortName != "Matrix" {
		t.Fatalf("title not applied: %+v", it)
	}
	if it.Overview != "A hacker discovers reality." || it.Year != 1999 || it.Rating != 8.7 {
		t.Fatalf("metadata not applied: %+v", it)
	}
	if it.OfficialRating != "R" || len(it.Genres) != 2 || it.Genres[1] != "Science Fiction" {
		t.Fatalf("genres/rating not applied: %+v", it)
	}
	if len(it.Taglines) != 1 || it.Taglines[0] != "Free your mind." {
		t.Fatalf("tagline not applied: %+v", it.Taglines)
	}
	if len(it.People) != 1 || it.People[0].Name != "Keanu Reeves" || it.People[0].Role != "Neo" {
		t.Fatalf("cast not applied: %+v", it.People)
	}
	if it.People[0].ImageURL != "https://example.com/keanu.jpg" {
		t.Fatalf("actor thumb not applied: %+v", it.People[0])
	}
}

func TestSeriesNFOCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "Severance.NFO"), `<tvshow>
  <title>Severance</title>
  <plot>Office workers sever work memories.</plot>
  <year>2022</year>
  <genre>Drama</genre>
</tvshow>`)

	it := &model.Item{Name: "Severance", Type: model.TypeSeries}
	applyNFO(it, seriesNFO(dir, "Severance"))
	if it.MetadataSource != "nfo" || it.Overview == "" || it.Year != 2022 {
		t.Fatalf("series nfo not applied: %+v", it)
	}
}

func TestNFONestedRatings(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "movie.nfo"), `<movie>
  <title>Nested Rating</title>
  <ratings>
    <rating name="imdb" max="10" default="true">
      <value>7.9</value>
      <votes>1200</votes>
    </rating>
  </ratings>
</movie>`)

	it := &model.Item{Name: "Nested", Type: model.TypeMovie}
	applyNFO(it, movieNFO(&videoEntry{dir: dir, base: "Nested"}))
	if it.Rating != 7.9 {
		t.Fatalf("nested rating not applied: %+v", it)
	}
}

func TestNFOGBK(t *testing.T) {
	title := "\u5343\u4e0e\u5343\u5bfb"
	plot := "\u4e00\u4e2a\u5c11\u5e74\u7684\u5947\u5e7b\u65c5\u7a0b"
	raw := `<?xml version="1.0" encoding="GBK"?><movie><title>` + title + `</title><plot>` + plot + `</plot></movie>`
	gbk, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "movie.nfo"), gbk, 0o644); err != nil {
		t.Fatal(err)
	}

	it := &model.Item{Name: "Spirited Away", Type: model.TypeMovie}
	applyNFO(it, movieNFO(&videoEntry{dir: dir, base: "Spirited.Away"}))
	if it.Name != title || it.Overview != plot {
		t.Fatalf("GBK nfo not decoded: %+v", it)
	}
}

func TestArtKind(t *testing.T) {
	cases := map[string]string{
		"poster.jpg":    "poster",
		"folder.jpg":    "poster",
		"backdrop.jpg":  "backdrop",
		"fanart.jpg":    "backdrop",
		"landscape.jpg": "landscape",
		"logo.png":      "logo",
		"clearlogo.png": "logo",
		"movie.nfo":     "",
		"video.mkv":     "",
	}
	for name, want := range cases {
		if got := artKind(name); got != want {
			t.Errorf("artKind(%q) = %q, want %q", name, got, want)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
