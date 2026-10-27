package jellyfin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nuvio/internal/catalog"
	"nuvio/internal/demo"
	"nuvio/internal/model"
	"nuvio/internal/store"
)

func newTestAPI(t *testing.T) (*API, http.Handler) {
	t.Helper()
	st, err := store.Open(t.TempDir(), "Test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cat := catalog.New()
	cat.Replace(demo.Libraries(), demo.Items())
	a := &API{Store: st, Catalog: cat}
	mux := http.NewServeMux()
	a.Register(mux)
	return a, mux
}

func testUser(t *testing.T, a *API) (*model.User, string) {
	t.Helper()
	u, err := a.Store.CreateUser("alice", "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := a.Store.IssueToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u, tok
}

// Libraries are addressed as items by Jellyfin clients when a user opens a
// media folder. They must not answer 404.
func TestLibraryItemDetails(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	req := httptest.NewRequest("GET", "/Users/"+u.ID+"/Items/"+demo.MovieLibraryID, nil)
	req.Header.Set("X-Emby-Token", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var dto itemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Id != demo.MovieLibraryID || dto.Type != "CollectionFolder" || dto.CollectionType != model.LibraryMovies {
		t.Fatalf("unexpected library dto: %+v", dto)
	}
	if dto.ImageTags["Primary"] == "" {
		t.Fatal("library has no primary image tag")
	}
}

func TestRootItemDetails(t *testing.T) {
	a, h := newTestAPI(t)
	_, tok := testUser(t, a)

	req := httptest.NewRequest("GET", "/Items/"+rootItemID, nil)
	req.Header.Set("X-Emby-Token", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

// Jellyfin clients fetch artwork without credentials.
func TestImagesAnonymous(t *testing.T) {
	_, h := newTestAPI(t)
	items := demo.Items()
	for _, target := range []string{demo.MovieLibraryID, rootItemID, items[0].ID} {
		req := httptest.NewRequest("GET", "/Items/"+target+"/Images/Primary", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: status = %d: %s", target, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
			t.Fatalf("%s: content type = %q", target, ct)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s: empty image", target)
		}
	}
}

func TestImagesRespectTokenPermissions(t *testing.T) {
	a, h := newTestAPI(t)
	u, err := a.Store.CreateUser("bob", "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store.UpdateUserPolicy(u.ID, []string{demo.MovieLibraryID}, true, true); err != nil {
		t.Fatal(err)
	}
	tok, err := a.Store.IssueToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/Items/"+demo.ShowLibraryID+"/Images/Primary", nil)
	req.Header.Set("X-Emby-Token", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("hidden library image status = %d", rec.Code)
	}
}
