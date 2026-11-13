package jellyfin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"nuvio/internal/demo"
	"nuvio/internal/model"
	"nuvio/internal/vectors"
)

func TestAISearchUsesVectors(t *testing.T) {
	embed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"index": 0, "embedding": []float32{1, 0, 0}}},
		})
	}))
	defer embed.Close()

	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	vec, err := vectors.Open(filepath.Join(t.TempDir(), "embeddings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer vec.Close()
	a.Vectors = vec
	a.Store.UpdateSettings(func(st *model.ServerSettings) {
		st.AISearchEnabled = true
		st.AIEmbeddingURL = embed.URL
		st.AIEmbeddingModel = "test"
	})

	var movie *model.Item
	for _, it := range demo.Items() {
		if it.Type == model.TypeMovie {
			movie = it
			break
		}
	}
	if movie == nil {
		t.Fatal("demo has no movie")
	}
	if err := vec.Put(movie.ID, "test", "zh-CN", "h", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/Items/AI/Search?userId="+u.ID, strings.NewReader(`{"query":"zzzz-not-a-title"}`))
	req.Header.Set("Content-Type", "application/json")
	setToken(req, tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []itemDTO
		Mode  string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "hybrid" || len(out.Items) == 0 || out.Items[0].Id != movie.ID {
		t.Fatalf("search = %+v", out)
	}
}
