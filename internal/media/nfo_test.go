package media

import (
	"os"
	"path/filepath"
	"testing"

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
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
