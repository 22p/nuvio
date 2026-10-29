package metadata

import (
	"testing"

	"nuvio/internal/model"
)

func TestMergeCachedKeepsTMDBData(t *testing.T) {
	cached := []*model.Item{
		{
			ID: "series", Type: model.TypeSeries, Name: "为美好的世界献上祝福！", SortName: "为美好的世界献上祝福！",
			MetadataSource: "tmdb", MetadataLanguage: "zh-CN", TMDBID: 62715, TMDBVersion: metadataVersion,
			Overview: "家里蹲少年转生到异世界。", PosterURL: "poster", BackdropURL: "backdrop",
			LogoURL: "logo", Rating: 8.1, Year: 2016, Genres: []string{"动画"},
			People: []model.Person{{Name: "福岛润", Role: "和真"}},
		},
		{
			ID: "episode", Type: model.TypeEpisode, Name: "这个家里蹲和异世界！", SortName: "这个家里蹲和异世界！",
			MetadataSource: "tmdb", MetadataLanguage: "zh-CN",
			Overview: "和真转生到异世界。", ThumbURL: "still", PremiereDate: "2016-01-14T00:00:00.0000000Z",
		},
		{
			ID: "movie", Type: model.TypeMovie, Name: "TMDb Movie", Overview: "remote plot",
			MetadataSource: "tmdb", MetadataLanguage: "zh-CN", TMDBID: 42,
		},
	}

	series := &model.Item{ID: "series", Type: model.TypeSeries, Name: "Konosuba", SortName: "Konosuba", LibraryID: "lib"}
	episode := &model.Item{ID: "episode", Type: model.TypeEpisode, Name: "Episode 1", SortName: "Episode 1", SeriesID: "series", LibraryID: "lib"}
	movie := &model.Item{ID: "movie", Type: model.TypeMovie, Name: "My Local Title", Overview: "local plot", MetadataSource: "nfo", LibraryID: "lib"}

	MergeCached([]*model.Item{series, episode, movie}, cached)

	if series.Name != "为美好的世界献上祝福！" || series.SortName != "为美好的世界献上祝福！" {
		t.Fatalf("series title = %q / %q", series.Name, series.SortName)
	}
	if series.TMDBID != 62715 || series.MetadataLanguage != "zh-CN" || series.PosterURL != "poster" || series.LogoURL != "logo" || series.TMDBVersion != metadataVersion {
		t.Fatalf("series metadata: %+v", series)
	}
	if series.Overview == "" || len(series.Genres) != 1 || len(series.People) != 1 {
		t.Fatalf("series details: %+v", series)
	}
	if episode.Name != "这个家里蹲和异世界！" || episode.Overview == "" || episode.ThumbURL != "still" || episode.MetadataLanguage != "zh-CN" {
		t.Fatalf("episode metadata: %+v", episode)
	}
	// An .nfo title wins, but the TMDb id is still useful for artwork lookups.
	if movie.Name != "My Local Title" || movie.Overview != "local plot" || movie.TMDBID != 42 {
		t.Fatalf("nfo item: %+v", movie)
	}
}

func TestMergeCachedIgnoresDifferentPaths(t *testing.T) {
	old := &model.Item{ID: "a", Type: model.TypeMovie, TMDBID: 1, PosterURL: "poster", MetadataSource: "tmdb", MetadataLanguage: "zh-CN"}
	fresh := &model.Item{ID: "b", Type: model.TypeMovie, Name: "Moved Movie"}
	MergeCached([]*model.Item{fresh}, []*model.Item{old})
	if fresh.TMDBID != 0 || fresh.PosterURL != "" {
		t.Fatalf("metadata leaked across ids: %+v", fresh)
	}
}
