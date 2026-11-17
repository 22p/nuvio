package store

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nuvio/internal/model"
)

func mustOpen(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir, "Nuvio")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s
}

func mustUser(t *testing.T, s *Store, name string) *model.User {
	t.Helper()
	u, err := s.CreateUser(name, "secret", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

// A normal restart must restore every piece of state the server depends on.
func TestStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	serverID := s.ServerID()
	created := s.StartedAt()

	u := mustUser(t, s, "alice")
	tok, err := s.IssueToken(u.ID, SessionMeta{DeviceName: "tv", Client: "nuvio"})
	if err != nil {
		t.Fatal(err)
	}
	s.UpdateSettings(func(st *model.ServerSettings) {
		st.ServerName = "Living Room"
		st.TMDBAPIKey = "tmdb-key"
	})
	lib, err := s.AddLibrary("Movies", model.LibraryMovies, []string{"/media/movies"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceItems([]*model.Item{{
		ID: "m1", Type: model.TypeMovie, Name: "Heat", SortName: "Heat",
		LibraryID: lib.ID, ParentID: lib.ID, Container: "mkv",
	}}); err != nil {
		t.Fatal(err)
	}
	s.SetProgress(u.ID, "m1", 7_000_000, false)
	s.SetFavorite(u.ID, "m1", true)
	lastScan := s.LastScan()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	next := mustOpen(t, dir)
	defer next.Close()

	if next.ServerID() != serverID {
		t.Fatalf("server id changed: %q -> %q", serverID, next.ServerID())
	}
	if !next.StartedAt().Equal(created) {
		t.Fatalf("creation time changed: %v -> %v", created, next.StartedAt())
	}
	if got := next.Settings().ServerName; got != "Living Room" {
		t.Fatalf("server name = %q", got)
	}
	if got := next.Settings().TMDBAPIKey; got != "tmdb-key" {
		t.Fatalf("tmdb key lost: %q", got)
	}
	if !next.SetupComplete() {
		t.Fatal("users vanished after restart")
	}
	if got, ok := next.UserByToken(tok); !ok || got.ID != u.ID {
		t.Fatal("token no longer resolves after restart")
	}
	if libs := next.Libraries(); len(libs) != 1 || libs[0].ID != lib.ID {
		t.Fatalf("libraries after restart: %+v", libs)
	}
	items, err := next.Items()
	if err != nil || len(items) != 1 || items[0].ID != "m1" {
		t.Fatalf("items after restart: %v %+v", err, items)
	}
	ud := next.GetUserData(u.ID, "m1")
	if ud.PositionTicks != 7_000_000 || !ud.Favorite {
		t.Fatalf("user data lost: %+v", ud)
	}
	if !next.LastScan().Equal(lastScan) {
		t.Fatalf("last scan = %v, want %v", next.LastScan(), lastScan)
	}
}

// Committed transactions live in the WAL until a checkpoint. Copying the
// database and its WAL (the state left behind by a crash) must reopen cleanly
// with no committed data missing.
func TestCommittedWritesSurviveCrash(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	// Keep every commit in the WAL so the copied files represent a real crash
	// rather than an already-checkpointed database.
	if _, err := s.db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	u := mustUser(t, s, "alice")
	s.UpdateSettings(func(st *model.ServerSettings) { st.ServerName = "Crashed" })
	s.SetProgress(u.ID, "m1", 42, false)
	// Deliberately no Close(): emulate a process that died with the WAL live.

	crashDir := t.TempDir()
	for _, name := range []string{"nuvio.db", "nuvio.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(raw) == 0 {
			t.Fatalf("%s is empty; the crash copy would not exercise WAL recovery", name)
		}
		if err := os.WriteFile(filepath.Join(crashDir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defer s.Close()

	recovered := mustOpen(t, crashDir)
	defer recovered.Close()
	if got := recovered.Settings().ServerName; got != "Crashed" {
		t.Fatalf("settings lost in crash recovery: %q", got)
	}
	if _, ok := recovered.FindUser(u.ID); !ok {
		t.Fatal("user lost in crash recovery")
	}
	if ud := recovered.GetUserData(u.ID, "m1"); ud.PositionTicks != 42 {
		t.Fatalf("progress lost in crash recovery: %+v", ud)
	}
}

// A file that is not a database must produce an error, not a panic or a
// half-initialised store.
func TestOpenRejectsCorruptDatabase(t *testing.T) {
	dir := t.TempDir()
	garbage := make([]byte, 8192)
	for i := range garbage {
		garbage[i] = byte(i * 7)
	}
	if err := os.WriteFile(filepath.Join(dir, "nuvio.db"), garbage, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, "Nuvio")
	if err == nil {
		if s != nil {
			s.Close()
		}
		t.Fatal("opening a corrupt database should fail")
	}
}

// One damaged catalog row must not stop the other rows (and the server) from
// loading.
func TestItemsSkipsUnreadableRows(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	defer s.Close()
	lib, err := s.AddLibrary("Movies", model.LibraryMovies, []string{"/media/movies"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceItems([]*model.Item{{
		ID: "good", Type: model.TypeMovie, Name: "Good", SortName: "Good", LibraryID: lib.ID,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO items(id, library_id, data, updated_at) VALUES('bad', ?, '{not json', ?)",
		lib.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	items, err := s.Items()
	if err != nil {
		t.Fatalf("a corrupt row must not fail the whole load: %v", err)
	}
	if len(items) != 1 || items[0].ID != "good" {
		t.Fatalf("items = %+v", items)
	}
}

// A failed catalog write must leave the previous catalog intact.
func TestReplaceItemsRollsBackOnFailure(t *testing.T) {
	s := mustOpen(t, t.TempDir())
	defer s.Close()
	lib, err := s.AddLibrary("Movies", model.LibraryMovies, []string{"/media/movies"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceItems([]*model.Item{{
		ID: "keep", Type: model.TypeMovie, Name: "Keep", SortName: "Keep", LibraryID: lib.ID,
	}}); err != nil {
		t.Fatal(err)
	}

	// NaN cannot be marshalled to JSON, so the insert fails mid-transaction.
	bad := &model.Item{ID: "bad", Type: model.TypeMovie, Name: "Bad", LibraryID: lib.ID, Rating: math.NaN()}
	if err := s.ReplaceItems([]*model.Item{bad}); err == nil {
		t.Fatal("expected the catalog write to fail")
	}
	items, err := s.Items()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "keep" {
		t.Fatalf("failed write destroyed the previous catalog: %+v", items)
	}
}

// Corrupt settings must not make the server unbootable; the bad blob is kept
// for post-mortem and defaults are used.
func TestCorruptSettingsFallBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	u := mustUser(t, s, "alice")
	if _, err := s.db.Exec("UPDATE meta SET value = '{oops' WHERE key = 'settings'"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	next, err := Open(dir, "Nuvio")
	if err != nil {
		t.Fatalf("corrupt settings must not block startup: %v", err)
	}
	defer next.Close()
	if got := next.Settings().ServerName; got != "Nuvio" {
		t.Fatalf("server name = %q, want default", got)
	}
	if !next.SetupComplete() {
		t.Fatal("user rows should survive a settings reset")
	}
	if _, ok := next.FindUser(u.ID); !ok {
		t.Fatal("user lost while recovering settings")
	}
	if raw, ok := next.metaGet("settings_corrupt"); !ok || raw != "{oops" {
		t.Fatalf("corrupt blob was not preserved: %q %v", raw, ok)
	}
}
