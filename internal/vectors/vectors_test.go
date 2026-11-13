package vectors

import (
	"path/filepath"
	"testing"

	"nuvio/internal/model"
)

func TestStorePutSearchAndPrune(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "embeddings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version string
	if err := s.db.QueryRow("SELECT vec_version()").Scan(&version); err != nil {
		t.Fatalf("sqlite-vec is not loaded: %v", err)
	}
	if version == "" {
		t.Fatal("sqlite-vec returned an empty version")
	}

	if err := s.Put("a", "test", "zh-CN", "h1", []float32{2, 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("b", "test", "zh-CN", "h2", []float32{0, 3}); err != nil {
		t.Fatal(err)
	}
	if s.Dims() != 2 || s.Count() != 2 || s.Model() != "test" {
		t.Fatalf("store stats: dims=%d count=%d model=%q", s.Dims(), s.Count(), s.Model())
	}

	hits := s.Search([]float32{1, 0}, 2, nil)
	if len(hits) != 2 || hits[0].ID != "a" || hits[1].ID != "b" {
		t.Fatalf("search hits = %+v", hits)
	}
	filtered := s.Search([]float32{1, 0}, 2, func(id string) bool { return id != "b" })
	if len(filtered) != 1 || filtered[0].ID != "a" {
		t.Fatalf("filtered search hits = %+v", filtered)
	}

	if err := s.Prune(map[string]bool{"a": true}); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 {
		t.Fatalf("count after prune = %d", s.Count())
	}
	if _, ok := s.Get("b"); ok {
		t.Fatal("pruned entry still present")
	}
}

func TestDocumentSkipsOtherTypes(t *testing.T) {
	if got := Document(&model.Item{Type: model.TypeEpisode, Name: "ep"}); got != "" {
		t.Fatalf("episode document = %q", got)
	}
	doc := Document(&model.Item{Type: model.TypeMovie, Name: "The Matrix", Year: 1999, Genres: []string{"Action"}})
	if doc == "" || Hash(doc) == "" {
		t.Fatal("movie document is empty")
	}
}
