package jellyfin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nuvio/internal/catalog"
	"nuvio/internal/model"
)

func benchHandler(b *testing.B, h http.Handler, path, token string) {
	b.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	setToken(req, token)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkItemsHandlerSmallCatalog(b *testing.B) {
	a, h := newTestAPI(b)
	u, tok := testUser(b, a)
	benchHandler(b, h, "/Items?userId="+u.ID+"&limit=20", tok)
}

func BenchmarkItemsHandlerLargeCatalog(b *testing.B) {
	a, h := newTestAPI(b)
	u, tok := testUser(b, a)
	views, items := buildLargeCatalog(3000, 1000, 2, 4)
	a.Catalog.Replace(views, items)
	benchHandler(b, h, "/Items?userId="+u.ID+"&limit=20", tok)
}

func BenchmarkCatalogQueryLarge(b *testing.B) {
	a, _ := newTestAPI(b)
	views, items := buildLargeCatalog(3000, 1000, 2, 4)
	a.Catalog.Replace(views, items)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, total := a.Catalog.Query(catalog.Query{Start: 0, Limit: 20, SortBy: "SortName"}, nil)
		if len(got) != 20 || total != len(items) {
			b.Fatalf("got %d/%d", len(got), total)
		}
	}
}

func BenchmarkUserByToken(b *testing.B) {
	a, _ := newTestAPI(b)
	_, tok := testUser(b, a)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := a.Store.UserByToken(tok); !ok {
			b.Fatal("token rejected")
		}
	}
}

func BenchmarkProgressWrite(b *testing.B) {
	a, _ := newTestAPI(b)
	u, _ := testUser(b, a)
	var id string
	for _, it := range a.Catalog.All() {
		if it.Type == model.TypeMovie {
			id = it.ID
			break
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Store.SetProgress(u.ID, id, int64(i), false)
	}
}
