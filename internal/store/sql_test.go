package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"nuvio/internal/model"
)

// PRAGMAs are connection-scoped. The store installs a connection hook so a
// replacement connection still enforces foreign keys instead of silently
// falling back to SQLite's default (OFF).
func TestConnectionPragmasApplyToNewConnections(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	defer s.Close()

	check := func() int {
		t.Helper()
		var fk, busy int
		if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if busy != 5000 {
			t.Fatalf("busy_timeout = %d", busy)
		}
		return fk
	}
	if fk := check(); fk != 1 {
		t.Fatalf("foreign_keys = %d on the first connection", fk)
	}
	// Drop the pooled connection and let database/sql open a fresh one:
	// no idle connections are kept and the existing one is expired.
	s.db.SetMaxIdleConns(0)
	s.db.SetConnMaxLifetime(time.Nanosecond)
	time.Sleep(5 * time.Millisecond)
	if fk := check(); fk != 1 {
		t.Fatalf("foreign_keys = %d on a replacement connection", fk)
	}
}

func TestDeleteUserCascades(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	defer s.Close()
	u := mustUser(t, s, "alice")
	tok, err := s.IssueToken(u.ID, SessionMeta{DeviceName: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetProgress(u.ID, "m1", 100, false)
	s.SetFavorite(u.ID, "m1", true)
	s.SaveDisplayPreferences(u.ID, "web", "home", map[string]any{"x": 1})

	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.UserByToken(tok); ok {
		t.Fatal("token survived user deletion")
	}
	var tokens, items, prefs int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM tokens WHERE user_id = ?", u.ID).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM user_items WHERE user_id = ?", u.ID).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM display_preferences WHERE user_id = ?", u.ID).Scan(&prefs); err != nil {
		t.Fatal(err)
	}
	if tokens != 0 || items != 0 || prefs != 0 {
		t.Fatalf("cascade left tokens=%d items=%d prefs=%d", tokens, items, prefs)
	}
}

func seedMovie(t *testing.T, s *Store, libID, id string) *model.Item {
	t.Helper()
	return &model.Item{ID: id, Type: model.TypeMovie, Name: id, SortName: id, LibraryID: libID}
}

// Rows for media that left the catalog must not accumulate, while a normal
// rescan (same items) must keep the progress.
func TestReplaceItemsPrunesOrphanProgressOnly(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	defer s.Close()
	u := mustUser(t, s, "alice")
	lib, err := s.AddLibrary("Movies", model.LibraryMovies, []string{"/media"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceItems([]*model.Item{seedMovie(t, s, lib.ID, "a"), seedMovie(t, s, lib.ID, "b")}); err != nil {
		t.Fatal(err)
	}
	s.SetProgress(u.ID, "a", 111, false)
	s.SetProgress(u.ID, "b", 222, false)

	if err := s.ReplaceItems([]*model.Item{seedMovie(t, s, lib.ID, "a")}); err != nil {
		t.Fatal(err)
	}
	if ud := s.GetUserData(u.ID, "a"); ud.PositionTicks != 111 {
		t.Fatalf("rescan lost progress for a live item: %+v", ud)
	}
	if ud := s.GetUserData(u.ID, "b"); ud.PositionTicks != 0 {
		t.Fatalf("orphan progress was not pruned: %+v", ud)
	}
}

func TestRemoveLibraryPrunesProgressAndReportsMissing(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	defer s.Close()
	u := mustUser(t, s, "alice")
	lib, err := s.AddLibrary("Movies", model.LibraryMovies, []string{"/media"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceItems([]*model.Item{seedMovie(t, s, lib.ID, "a")}); err != nil {
		t.Fatal(err)
	}
	s.SetProgress(u.ID, "a", 333, false)

	if err := s.RemoveLibrary(lib.ID); err != nil {
		t.Fatal(err)
	}
	if ud := s.GetUserData(u.ID, "a"); ud.PositionTicks != 0 {
		t.Fatalf("library removal kept progress: %+v", ud)
	}
	if err := s.RemoveLibrary(lib.ID); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("second remove = %v, want ErrLibraryNotFound", err)
	}
}

// Fixed-width timestamps must sort chronologically as TEXT.
func TestTimestampTextSortsChronologically(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	defer s.Close()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if len(tsText(base)) != len(tsText(base.Add(time.Nanosecond))) {
		t.Fatalf("timestamps are not fixed width: %q vs %q", tsText(base), tsText(base.Add(time.Nanosecond)))
	}
	if _, err := s.db.Exec("CREATE TEMP TABLE ts_check(v TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []string{tsText(base.Add(500 * time.Microsecond)), tsText(base)} {
		if _, err := s.db.Exec("INSERT INTO ts_check VALUES(?)", ts); err != nil {
			t.Fatal(err)
		}
	}
	var first string
	if err := s.db.QueryRow("SELECT v FROM ts_check ORDER BY v LIMIT 1").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first != tsText(base) {
		t.Fatalf("TEXT ordering is not chronological: first = %q", first)
	}
}

// The flag flip happens in SQL, so N concurrent toggles must land on the same
// state as N sequential ones (odd = on, even = off).
func TestToggleFavoriteConcurrent(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	defer s.Close()
	u := mustUser(t, s, "alice")

	const toggles = 40
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < toggles; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s.ToggleFavorite(u.ID, "m1")
		}()
	}
	close(start)
	wg.Wait()
	if got := s.GetUserData(u.ID, "m1").Favorite; got {
		t.Fatalf("after %d toggles favorite = %v, want false", toggles, got)
	}
	if s.ToggleFavorite(u.ID, "m1") != true {
		t.Fatal("one more toggle should turn it on")
	}
	if s.ToggleFavorite(u.ID, "m1") != false {
		t.Fatal("another toggle should turn it off")
	}
}

// A catalog larger than one insert batch still round-trips unchanged.
func TestReplaceItemsBatchesLargeCatalog(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	defer s.Close()
	lib, err := s.AddLibrary("Movies", model.LibraryMovies, []string{"/media"}, false)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]*model.Item, 0, 1200)
	for i := 0; i < 1200; i++ {
		items = append(items, seedMovie(t, s, lib.ID, fmt.Sprintf("m%d", i)))
	}
	if err := s.ReplaceItems(items); err != nil {
		t.Fatal(err)
	}
	got, err := s.Items()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(items) {
		t.Fatalf("items = %d, want %d", len(got), len(items))
	}
}
