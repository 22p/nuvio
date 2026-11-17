package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nuvio/internal/model"
)

// A library whose storage is gone (unmounted share, detached disk) must fail
// the scan so the caller can keep the previous index instead of publishing an
// empty library.
func TestScanFailsWhenLibraryPathIsMissing(t *testing.T) {
	lib := &model.Library{ID: "lib", Name: "Movies", Paths: []string{filepath.Join(t.TempDir(), "unmounted")}}
	items, err := (&Scanner{}).Scan(context.Background(), lib)
	if err == nil {
		t.Fatal("scanning an unavailable path must fail")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d", len(items))
	}
}

func TestScanFailsWhenLibraryPathIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := (&Scanner{}).Scan(context.Background(), &model.Library{ID: "lib", Paths: []string{file}})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error = %v, items = %d", err, len(items))
	}
}

// A library with no paths configured is simply empty, not broken.
func TestScanWithNoPathsIsNotAnError(t *testing.T) {
	items, err := (&Scanner{}).Scan(context.Background(), &model.Library{ID: "lib"})
	if err != nil {
		t.Fatalf("empty library: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d", len(items))
	}
}

// If any configured root is unavailable the whole library scan fails: partial
// results would silently drop everything stored on the missing root.
func TestScanFailsWhenOneOfSeveralRootsIsMissing(t *testing.T) {
	present := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	lib := &model.Library{ID: "lib", Paths: []string{present, missing}}
	if _, err := (&Scanner{}).Scan(context.Background(), lib); err == nil {
		t.Fatal("expected the scan to fail on the missing root")
	}
}
