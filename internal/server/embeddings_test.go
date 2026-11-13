package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"nuvio/internal/catalog"
	"nuvio/internal/model"
	"nuvio/internal/store"
	"nuvio/internal/vectors"
)

func TestEmbeddingIndexBuildsSeparateDatabase(t *testing.T) {
	var calls int
	embed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		data := make([]map[string]any, len(body.Input))
		for i := range body.Input {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 0, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer embed.Close()

	dir := t.TempDir()
	st, err := store.Open(dir, "Test")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cat := catalog.New()
	cat.Replace(nil, []*model.Item{
		{ID: "movie", Type: model.TypeMovie, Name: "The Matrix", Year: 1999, Overview: "A hacker."},
		{ID: "series", Type: model.TypeSeries, Name: "Severance", Year: 2022, Overview: "Office workers."},
		{ID: "episode", Type: model.TypeEpisode, Name: "Episode 1"},
	})
	vec, err := vectors.Open(filepath.Join(dir, "embeddings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer vec.Close()
	s := &Server{Store: st, Catalog: cat, Vectors: vec}
	st.UpdateSettings(func(settings *model.ServerSettings) {
		settings.AISearchEnabled = true
		settings.AIEmbeddingURL = embed.URL
		settings.AIEmbeddingModel = "test"
	})

	s.StartEmbeddingIndex(false)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status := s.EmbeddingStatus(); !status.Running && status.Indexed == 2 {
			if calls == 0 {
				t.Fatal("embedding endpoint was not called")
			}
			if vec.Path() == st.Path() {
				t.Fatal("embeddings should use a separate database")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("index did not finish: %+v", s.EmbeddingStatus())
}
