package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"nuvio/internal/model"
)

func TestScanReadsSeriesNFOAndArtwork(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Severance")
	season := filepath.Join(show, "Season 01")
	if err := os.MkdirAll(season, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(season, "Severance.S01E01.mkv"), "")
	writeTestFile(t, filepath.Join(show, "Severance.nfo"), `<tvshow>
  <title>Severance</title>
  <plot>Office workers sever work memories.</plot>
  <year>2022</year>
  <genre>Drama</genre>
  <actor><name>Adam Scott</name><role>Mark Scout</role></actor>
</tvshow>`)
	for _, name := range []string{"folder.jpg", "backdrop.jpg", "logo.png", "landscape.jpg"} {
		writeTestFile(t, filepath.Join(show, name), "image")
	}

	lib := &model.Library{ID: "lib", Type: model.LibraryShows, Paths: []string{root}}
	items, err := (&Scanner{}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}

	var series, episode *model.Item
	for _, it := range items {
		switch it.Type {
		case model.TypeSeries:
			series = it
		case model.TypeEpisode:
			episode = it
		}
	}
	if series == nil || episode == nil {
		t.Fatalf("series or episode missing: %+v", items)
	}
	if series.Name != "Severance" || series.Overview == "" || series.Year != 2022 {
		t.Fatalf("series nfo fields: %+v", series)
	}
	if series.PosterPath == "" || series.BackdropPath == "" || series.LogoPath == "" || series.ThumbPath == "" {
		t.Fatalf("series artwork missing: %+v", series)
	}
	if len(series.People) != 1 || series.People[0].Name != "Adam Scott" {
		t.Fatalf("series cast: %+v", series.People)
	}
	if episode.LogoPath != series.LogoPath || episode.ThumbPath != series.ThumbPath {
		t.Fatalf("episode artwork not inherited: %+v", episode)
	}
	if len(episode.People) != 1 || len(episode.Genres) != 1 {
		t.Fatalf("episode metadata not inherited: %+v", episode)
	}
}

func TestScanReadsEpisodeNFO(t *testing.T) {
	root := t.TempDir()
	season := filepath.Join(root, "Konosuba", "Season 01")
	if err := os.MkdirAll(season, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(season, "Konosuba S01E04.mkv"), "")
	writeTestFile(t, filepath.Join(season, "Konosuba S01E04.nfo"), `<episodedetails>
  <title>The Crimson Village</title>
  <plot>Kazuma and the party visit a strange village.</plot>
  <year>2016</year>
  <rating>8.1</rating>
  <genre>Comedy</genre>
  <actor><name>Jun Fukushima</name><role>Kazuma</role></actor>
</episodedetails>`)

	lib := &model.Library{ID: "lib", Type: model.LibraryShows, Paths: []string{root}}
	items, err := (&Scanner{}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	var episode *model.Item
	for _, it := range items {
		if it.Type == model.TypeEpisode {
			episode = it
		}
	}
	if episode == nil {
		t.Fatalf("episode missing: %+v", items)
	}
	if episode.Name != "The Crimson Village" || episode.Overview == "" || episode.Rating != 8.1 {
		t.Fatalf("episode nfo fields: %+v", episode)
	}
	if len(episode.People) != 1 || episode.People[0].Name != "Jun Fukushima" {
		t.Fatalf("episode cast: %+v", episode.People)
	}
}

func TestScanReadsMovieNFO(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "The Matrix (1999)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "The.Matrix.1999.mkv"), "")
	writeTestFile(t, filepath.Join(dir, "movie.nfo"), `<movie>
  <title>The Matrix</title>
  <plot>A hacker discovers reality.</plot>
  <year>1999</year>
  <genre>Action</genre>
  <actor><name>Keanu Reeves</name><role>Neo</role></actor>
</movie>`)

	lib := &model.Library{ID: "lib", Type: model.LibraryMovies, Paths: []string{root}}
	items, err := (&Scanner{}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.Name != "The Matrix" || it.Overview == "" || it.Year != 1999 {
		t.Fatalf("movie nfo fields: %+v", it)
	}
	if len(it.People) != 1 || it.People[0].Name != "Keanu Reeves" {
		t.Fatalf("movie cast: %+v", it.People)
	}
}

func TestScanReadsPerEpisodeArtwork(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Konosuba")
	season := filepath.Join(show, "S1")
	meta := filepath.Join(season, "metadata")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(season, "Konosuba S1E01.mkv"), "")
	epArt := filepath.Join(meta, "Konosuba S1E01.jpg")
	writeTestFile(t, epArt, "episode image")
	seriesArt := filepath.Join(show, "folder.jpg")
	writeTestFile(t, seriesArt, "series image")

	lib := &model.Library{ID: "lib", Type: model.LibraryShows, Paths: []string{root}}
	items, err := (&Scanner{}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	var series, episode *model.Item
	for _, it := range items {
		switch it.Type {
		case model.TypeSeries:
			series = it
		case model.TypeEpisode:
			episode = it
		}
	}
	if series == nil || episode == nil {
		t.Fatalf("series or episode missing: %+v", items)
	}
	if episode.PosterPath != epArt || episode.ThumbPath != epArt {
		t.Fatalf("episode art = %q/%q, want %q", episode.PosterPath, episode.ThumbPath, epArt)
	}
	if series.PosterPath != seriesArt {
		t.Fatalf("series poster = %q, want %q", series.PosterPath, seriesArt)
	}
}
