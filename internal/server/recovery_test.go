package server

import (
	"path/filepath"
	"strings"
	"testing"

	"nuvio/internal/model"
)

func newRecoveryServer(t *testing.T, dir string) *Server {
	t.Helper()
	s, err := New(Config{DataDir: dir})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func seedItem(t *testing.T, s *Server, libraryID, id, name string) *model.Item {
	t.Helper()
	it := &model.Item{
		ID: id, Type: model.TypeMovie, Name: name, SortName: name,
		LibraryID: libraryID, ParentID: libraryID, Container: "mp4",
	}
	if err := s.Store.ReplaceItems([]*model.Item{it}); err != nil {
		t.Fatalf("save item: %v", err)
	}
	return it
}

// When a library's storage is unavailable the scan must fail and the last
// known-good index must survive in both SQLite and the live catalog. This used
// to silently delete the whole library.
func TestScanFailureKeepsPersistedItems(t *testing.T) {
	dir := t.TempDir()
	s := newRecoveryServer(t, dir)
	lib, err := s.Store.AddLibrary("Media", model.LibraryMovies, []string{filepath.Join(dir, "unmounted")}, false)
	if err != nil {
		t.Fatal(err)
	}
	seedItem(t, s, lib.ID, "keepme", "Keep Me")
	if err := s.reloadCatalog(); err != nil {
		t.Fatal(err)
	}

	s.runScan(false)

	items, err := s.Store.Items()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "keepme" {
		t.Fatalf("failed scan wiped the catalog: %+v", items)
	}
	if _, ok := s.Catalog.Get("keepme"); !ok {
		t.Fatal("failed scan removed the item from the live catalog")
	}
	status := s.Catalog.Status()
	if status.ItemCount != 1 {
		t.Fatalf("catalog item count = %d", status.ItemCount)
	}
	if !strings.Contains(status.LastError, "not available") {
		t.Fatalf("scan error was not reported: %q", status.LastError)
	}
}

// Restarting the process must restore the catalog, its timestamps and user
// state from SQLite without waiting for a rescan.
func TestRestartRestoresCatalogFromDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Store.AddLibrary("Media", model.LibraryMovies, []string{filepath.Join(dir, "media")}, false)
	if err != nil {
		t.Fatal(err)
	}
	seedItem(t, s, lib.ID, "m1", "Heat")
	if err := s.reloadCatalog(); err != nil {
		t.Fatal(err)
	}
	lastScan := s.Store.LastScan()
	s.Close()

	next := newRecoveryServer(t, dir)
	if got := next.Catalog.Status().ItemCount; got != 1 {
		t.Fatalf("catalog after restart = %d items", got)
	}
	if _, ok := next.Catalog.Get("m1"); !ok {
		t.Fatal("item missing after restart")
	}
	if !next.Catalog.Status().LastScan.Equal(lastScan) {
		t.Fatalf("last scan = %v, want %v", next.Catalog.Status().LastScan, lastScan)
	}
	if libs := next.Catalog.Views(); len(libs) != 1 || libs[0].Library.ID != lib.ID {
		t.Fatalf("views after restart: %+v", libs)
	}
}

// Items that belong to a library removed while the server was down must not
// reappear in the catalog (their rows stay in SQLite for auditability).
func TestRestartDropsItemsOfRemovedLibraries(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := s.Store.AddLibrary("Keep", model.LibraryMovies, []string{filepath.Join(dir, "keep")}, false)
	if err != nil {
		t.Fatal(err)
	}
	gone, err := s.Store.AddLibrary("Gone", model.LibraryMovies, []string{filepath.Join(dir, "gone")}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.ReplaceItems([]*model.Item{
		{ID: "keep1", Type: model.TypeMovie, Name: "Keep", SortName: "Keep", LibraryID: keep.ID, ParentID: keep.ID},
		{ID: "gone1", Type: model.TypeMovie, Name: "Gone", SortName: "Gone", LibraryID: gone.ID, ParentID: gone.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RemoveLibrary(gone.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()

	next := newRecoveryServer(t, dir)
	if _, ok := next.Catalog.Get("gone1"); ok {
		t.Fatal("item from a removed library leaked into the catalog")
	}
	if _, ok := next.Catalog.Get("keep1"); !ok {
		t.Fatal("item from a live library is missing")
	}
	if got := next.Catalog.Status().ItemCount; got != 1 {
		t.Fatalf("catalog count = %d", got)
	}
}
