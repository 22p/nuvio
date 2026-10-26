package catalog

import (
	"testing"
	"time"

	"nuvio/internal/model"
)

func testItems() []*model.Item {
	lib := "lib1"
	mk := func(id, name, typ, parent string) *model.Item {
		return &model.Item{ID: id, Name: name, SortName: name, Type: typ, LibraryID: lib, ParentID: parent, AddedAt: time.Now()}
	}
	series := mk("s1", "Show", model.TypeSeries, lib)
	season := mk("se1", "Season 1", model.TypeSeason, "s1")
	episode := mk("e1", "Pilot", model.TypeEpisode, "se1")
	episode.SeriesID = "s1"
	episode.SeasonID = "se1"
	episode.SeasonNumber = 1
	episode.EpisodeNumber = 1
	return []*model.Item{
		mk("m1", "Movie One", model.TypeMovie, lib),
		mk("m2", "Movie Two", model.TypeMovie, lib),
		series, season, episode,
	}
}

func TestQueryMatchesAndPaginates(t *testing.T) {
	c := New()
	libs := []*model.Library{{ID: "lib1", Name: "Movies", Type: model.LibraryMovies}}
	c.Replace(libs, testItems())

	items, total := c.Query(Query{ParentID: "lib1", Recursive: true, Types: []string{model.TypeMovie}, SortBy: "SortName"}, nil)
	if total != 2 || len(items) != 2 {
		t.Fatalf("got %d/%d", len(items), total)
	}
	if items[0].Name != "Movie One" {
		t.Fatalf("unexpected order: %v", items[0].Name)
	}

	items, total = c.Query(Query{ParentID: "lib1", Recursive: true, Types: []string{model.TypeMovie}, Start: 1, Limit: 1}, nil)
	if total != 2 || len(items) != 1 || items[0].Name != "Movie Two" {
		t.Fatalf("pagination failed: %+v total=%d", items, total)
	}

	items, total = c.Query(Query{ParentID: "s1", Recursive: true, Types: []string{model.TypeEpisode}}, nil)
	if total != 1 || items[0].Name != "Pilot" {
		t.Fatalf("episode query failed: %+v", items)
	}
}

// Query used to hold its read lock forever, which wedged the server as soon as
// a scan tried to publish results. Guard against regressions.
func TestQueryReleasesLock(t *testing.T) {
	c := New()
	c.Replace([]*model.Library{{ID: "lib1", Name: "Movies"}}, testItems())
	c.Query(Query{ParentID: "lib1", Limit: 10}, nil)

	done := make(chan struct{})
	go func() {
		c.SetViews([]*model.Library{{ID: "lib2", Name: "Shows"}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog lock was not released after Query")
	}
}
