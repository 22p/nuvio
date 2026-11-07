package media

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// Unrecognised season folders ("第一季", "SPs", "Disc 1") belong to their show
// instead of becoming separate top-level series.
func TestScanKeepsNonStandardSubfoldersInSeries(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Konosuba")
	writeTestFile(t, filepath.Join(show, "第一季", "Konosuba 01.mkv"), "")
	writeTestFile(t, filepath.Join(show, "第二季", "Konosuba 01.mkv"), "")
	writeTestFile(t, filepath.Join(show, "SPs", "Konosuba SP1.mkv"), "")

	lib := &model.Library{ID: "lib", Type: model.LibraryShows, Paths: []string{root}}
	items, err := (&Scanner{}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}

	var series []*model.Item
	seasons := map[int]string{}
	for _, it := range items {
		switch it.Type {
		case model.TypeSeries:
			series = append(series, it)
		case model.TypeSeason:
			seasons[it.SeasonNumber] = it.Name
		}
	}
	if len(series) != 1 || series[0].Name != "Konosuba" {
		t.Fatalf("series = %+v, want one Konosuba entry", series)
	}
	if _, ok := seasons[1]; !ok {
		t.Fatalf("missing season 1: %v", seasons)
	}
	if _, ok := seasons[2]; !ok {
		t.Fatalf("missing season 2: %v", seasons)
	}
	if _, ok := seasons[0]; !ok {
		t.Fatalf("missing specials season: %v", seasons)
	}
}

// Jellyfin's .ignore: an empty file excludes the directory, a non-empty file
// lists children to skip.
func TestScanHonoursIgnoreFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "Keep", "Season 1", "Keep S01E01.mkv"), "")
	writeTestFile(t, filepath.Join(root, "Skip", "Season 1", "Skip S01E01.mkv"), "")
	writeTestFile(t, filepath.Join(root, "Skip", ".ignore"), "")
	writeTestFile(t, filepath.Join(root, "Patterns", "Season 1", "Patterns S01E01.mkv"), "")
	writeTestFile(t, filepath.Join(root, "Patterns", ".ignore"), "*.mkv\n")
	writeTestFile(t, filepath.Join(root, ".hidden", "Season 1", "Hidden S01E01.mkv"), "")

	lib := &model.Library{ID: "lib", Type: model.LibraryShows, Paths: []string{root}}
	items, err := (&Scanner{}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, it := range items {
		if it.Type == model.TypeSeries {
			names[it.Name] = true
		}
	}
	if !names["Keep"] || len(names) != 1 {
		t.Fatalf("series = %v, want only Keep", names)
	}
}

func TestProviderIDsFromNames(t *testing.T) {
	cases := []struct {
		name string
		tmdb int
		imdb string
	}{
		{"Movie.Name.2020.1080p{tmdb-603}", 603, ""},
		{"[imdb-tt0133093] The Matrix", 0, "tt0133093"},
		{"Show [tmdbid=1399] S01", 1399, ""},
		{"No ids here", 0, ""},
	}
	for _, c := range cases {
		tmdb, imdb := providerIDs(c.name)
		if tmdb != c.tmdb || imdb != c.imdb {
			t.Fatalf("%q -> %d %q", c.name, tmdb, imdb)
		}
	}
}

func TestCleanTitleDropsReleaseNoise(t *testing.T) {
	cases := map[string]string{
		"Show.Name.S01E01.1080p.WEB-DL.x264-GROUP":           "Show Name S01E01",
		"[Nekomoe kissaten] Mushoku Tensei [01][1080p][CHS]": "Mushoku Tensei",
		"无职转生 第1季 简繁双语":                                      "无职转生",
		"The.Matrix.1999.1080p.BluRay.x264":                  "The Matrix",
	}
	for in, want := range cases {
		if got, _ := CleanTitle(in); got != want {
			t.Fatalf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestScanAttachesExtrasToTheirOwner(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "Movie Name (2019)")
	for _, dir := range []string{"trailers", "extras", filepath.Join("featurettes")} {
		if err := os.MkdirAll(filepath.Join(movieDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(movieDir, "Movie Name (2019).mkv"), "")
	writeTestFile(t, filepath.Join(movieDir, "trailers", "Official Trailer.mkv"), "")
	writeTestFile(t, filepath.Join(movieDir, "extras", "Behind The Scenes.mkv"), "")

	lib := &model.Library{ID: "lib", Type: model.LibraryMovies, Paths: []string{root}}
	items, err := (&Scanner{}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}

	var movie *model.Item
	var extras []*model.Item
	for _, it := range items {
		switch it.Type {
		case model.TypeMovie:
			movie = it
		case model.TypeVideo:
			extras = append(extras, it)
		}
	}
	if movie == nil {
		t.Fatalf("movie missing: %+v", items)
	}
	if len(extras) != 2 {
		t.Fatalf("extras = %+v", extras)
	}
	kinds := map[string]string{}
	for _, e := range extras {
		if e.ParentID != movie.ID {
			t.Fatalf("extra parent = %q, want %q", e.ParentID, movie.ID)
		}
		kinds[e.Name] = e.ExtraType
	}
	if kinds["Official Trailer"] != "Trailer" || kinds["Behind The Scenes"] != "Other" {
		t.Fatalf("extra types: %+v", kinds)
	}
}

func TestScanAttachesSeasonExtrasToSeries(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Show Name")
	season := filepath.Join(show, "Season 01")
	for _, dir := range []string{"extras", "trailers"} {
		if err := os.MkdirAll(filepath.Join(season, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(season, "Show Name S01E01.mkv"), "")
	writeTestFile(t, filepath.Join(season, "extras", "Bonus 01 - Tokuten.mkv"), "")
	writeTestFile(t, filepath.Join(season, "trailers", "[Group][Show Name][PV][S01E02][1080P].mkv"), "")

	lib := &model.Library{ID: "lib", Type: model.LibraryShows, Paths: []string{root}}
	items, err := (&Scanner{}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}

	var series *model.Item
	var extras []*model.Item
	for _, it := range items {
		switch it.Type {
		case model.TypeSeries:
			series = it
		case model.TypeVideo:
			extras = append(extras, it)
		}
	}
	if series == nil {
		t.Fatalf("series missing: %+v", items)
	}
	if len(extras) != 2 {
		t.Fatalf("extras = %+v", extras)
	}
	names := map[string]string{}
	for _, e := range extras {
		if e.ParentID != series.ID {
			t.Fatalf("extra %q parent = %q, want %q", e.Name, e.ParentID, series.ID)
		}
		names[e.Name] = e.ExtraType
	}
	if names["PV S01E02"] != "Trailer" {
		t.Fatalf("trailer name/type: %+v", names)
	}
}

func TestScanReusesProbeForUnchangedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffprobe is a shell script")
	}
	root := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "probe.log")
	ffprobe := filepath.Join(t.TempDir(), "ffprobe.sh")
	script := "#!/bin/sh\nprintf 'x\\n' >> " + logPath + "\nexit 1\n"
	if err := os.WriteFile(ffprobe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	movieDir := filepath.Join(root, "Movie Name (2020)")
	if err := os.MkdirAll(movieDir, 0o755); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(movieDir, "Movie Name (2020).mkv")
	writeTestFile(t, videoPath, "")
	writeTestFile(t, filepath.Join(movieDir, "Movie Name (2020).zh.srt"), "1\n00:00:01,000 --> 00:00:02,000\nHello\n")

	lib := &model.Library{ID: "lib", Type: model.LibraryMovies, Paths: []string{root}}
	probes := func() int {
		data, err := os.ReadFile(logPath)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), "x\n")
	}
	findMovie := func(items []*model.Item) *model.Item {
		for _, it := range items {
			if it.Type == model.TypeMovie {
				return it
			}
		}
		t.Fatalf("movie missing: %+v", items)
		return nil
	}

	first, err := (&Scanner{FFprobe: ffprobe}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if got := probes(); got != 1 {
		t.Fatalf("first scan probes = %d, want 1", got)
	}

	cache := NewProbeCache(first)
	second, err := (&Scanner{FFprobe: ffprobe, Cached: cache}).Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if got := probes(); got != 1 {
		t.Fatalf("unchanged scan probed again: %d", got)
	}
	movie := findMovie(second)
	if movie.Media == nil || len(movie.Media.Subtitles) != 1 {
		t.Fatalf("sidecar subtitle was lost or duplicated: %+v", movie.Media)
	}

	// A changed size invalidates the cached entry.
	writeTestFile(t, videoPath, "new content")
	if _, err := (&Scanner{FFprobe: ffprobe, Cached: cache}).Scan(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	if got := probes(); got != 2 {
		t.Fatalf("changed file probes = %d, want 2", got)
	}

	// A full scan ignores the cache even when the file is unchanged.
	if _, err := (&Scanner{FFprobe: ffprobe, Cached: cache, Full: true}).Scan(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	if got := probes(); got != 3 {
		t.Fatalf("full scan probes = %d, want 3", got)
	}
}
