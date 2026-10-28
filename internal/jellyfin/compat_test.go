package jellyfin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	tok, err := a.Store.IssueToken(u.ID, store.SessionMeta{})
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

	req := httptest.NewRequest("GET", "/Items/"+demo.MovieLibraryID+"?userId="+u.ID, nil)
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
	tok, err := a.Store.IssueToken(u.ID, store.SessionMeta{})
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

func doJSON(t *testing.T, h http.Handler, method, path, token, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("X-Emby-Token", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestLatestAPIRoutes(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	rec, _ := doJSON(t, h, "GET", "/UserViews?userId="+u.ID, tok, "")
	if rec.Code != 200 {
		t.Fatalf("UserViews = %d", rec.Code)
	}

	rec, data := doJSON(t, h, "GET", "/Items?userId="+u.ID, tok, "")
	if rec.Code != 200 || int(data["TotalRecordCount"].(float64)) == 0 {
		t.Fatalf("Items = %d %v", rec.Code, data["TotalRecordCount"])
	}

	rec, _ = doJSON(t, h, "GET", "/Items/Latest?userId="+u.ID, tok, "")
	if rec.Code != 200 {
		t.Fatalf("Items/Latest = %d", rec.Code)
	}

	rec, _ = doJSON(t, h, "GET", "/UserItems/Resume?userId="+u.ID, tok, "")
	if rec.Code != 200 {
		t.Fatalf("UserItems/Resume = %d", rec.Code)
	}

	// Legacy 10.x user scoped routes stay as compatibility aliases.
	rec, _ = doJSON(t, h, "GET", "/Users/"+u.ID+"/Items", tok, "")
	if rec.Code != 200 {
		t.Fatalf("legacy /Users/{id}/Items = %d, want 200", rec.Code)
	}
	rec, _ = doJSON(t, h, "GET", "/Users/"+u.ID+"/Views", tok, "")
	if rec.Code != 200 {
		t.Fatalf("legacy /Users/{id}/Views = %d, want 200", rec.Code)
	}
	movieID := demo.Items()[0].ID
	rec, _ = doJSON(t, h, "GET", "/Users/"+u.ID+"/Items/"+movieID, tok, "")
	if rec.Code != 200 {
		t.Fatalf("legacy item details = %d, want 200", rec.Code)
	}
}

func TestItemsFiltersAndUserData(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	rec, data := doJSON(t, h, "GET", "/Items?userId="+u.ID, tok, "")
	total := int(data["TotalRecordCount"].(float64))
	if rec.Code != 200 || total == 0 {
		t.Fatalf("Items = %d %v", rec.Code, data["TotalRecordCount"])
	}
	rec, data = doJSON(t, h, "GET", "/Items?userId="+u.ID+"&genres=%E7%A7%91%E5%B9%BB", tok, "")
	if got := int(data["TotalRecordCount"].(float64)); rec.Code != 200 || got == 0 || got >= total {
		t.Fatalf("genres filter = %d (all %d)", got, total)
	}
	rec, data = doJSON(t, h, "GET", "/Items?userId="+u.ID+"&years=2024", tok, "")
	if int(data["TotalRecordCount"].(float64)) == 0 {
		t.Fatal("years filter returned nothing")
	}

	movie := demo.Items()[0].ID
	rec, data = doJSON(t, h, "POST", "/UserFavoriteItems/"+movie+"?userId="+u.ID, tok, "")
	if rec.Code != 200 || data["IsFavorite"] != true || data["ItemId"] != movie {
		t.Fatalf("favorite response = %d %v", rec.Code, data)
	}
	rec, data = doJSON(t, h, "GET", "/Items?userId="+u.ID+"&isFavorite=true", tok, "")
	if int(data["TotalRecordCount"].(float64)) != 1 {
		t.Fatalf("isFavorite total = %v", data["TotalRecordCount"])
	}

	rec, _ = doJSON(t, h, "POST", "/UserItems/"+movie+"/UserData?userId="+u.ID, tok, `{"PlaybackPositionTicks":50000000}`)
	if rec.Code != 200 {
		t.Fatalf("UserData POST = %d", rec.Code)
	}
	rec, data = doJSON(t, h, "GET", "/UserItems/"+movie+"/UserData?userId="+u.ID, tok, "")
	if rec.Code != 200 || int64(data["PlaybackPositionTicks"].(float64)) != 50000000 || data["ItemId"] != movie {
		t.Fatalf("UserData GET = %d %v", rec.Code, data)
	}

	rec, data = doJSON(t, h, "POST", "/UserPlayedItems/"+movie+"?userId="+u.ID, tok, "")
	if rec.Code != 200 || data["Played"] != true {
		t.Fatalf("UserPlayedItems = %d %v", rec.Code, data["Played"])
	}
}

func TestLatestGrouping(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	req := httptest.NewRequest("GET", "/Items/Latest?userId="+u.ID+"&parentId="+demo.ShowLibraryID, nil)
	req.Header.Set("X-Emby-Token", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var list []itemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 4 {
		t.Fatalf("grouped latest = %d items, want 4 series", len(list))
	}
	for _, it := range list {
		if it.Type != model.TypeSeries {
			t.Fatalf("grouped latest returned %s", it.Type)
		}
	}

	req = httptest.NewRequest("GET", "/Items/Latest?userId="+u.ID+"&parentId="+demo.ShowLibraryID+"&groupItems=false&limit=5", nil)
	req.Header.Set("X-Emby-Token", tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	hasEpisode := false
	for _, it := range list {
		if it.Type == model.TypeEpisode {
			hasEpisode = true
		}
	}
	if !hasEpisode {
		t.Fatal("ungrouped latest returned no episodes")
	}
}

func TestSearchHintsShapeAndValidation(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	rec, _ := doJSON(t, h, "GET", "/Search/Hints", tok, "")
	if rec.Code != 400 {
		t.Fatalf("missing searchTerm = %d, want 400", rec.Code)
	}
	req := httptest.NewRequest("GET", "/Search/Hints?searchTerm=%E4%BF%A1%E5%8F%B7&limit=1&userId="+u.ID, nil)
	req.Header.Set("X-Emby-Token", tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body struct {
		SearchHints      []map[string]any `json:"SearchHints"`
		TotalRecordCount int              `json:"TotalRecordCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.SearchHints) != 1 {
		t.Fatalf("hints = %d, want 1", len(body.SearchHints))
	}
	if body.SearchHints[0]["PrimaryImageTag"] == nil {
		t.Fatal("hint is missing PrimaryImageTag")
	}
	if _, ok := body.SearchHints[0]["ImageTags"]; ok {
		t.Fatal("hint still returns non-spec ImageTags")
	}
}

func TestGenresPersonsAndDisplayPreferences(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	now := time.Now().UTC()
	a.Catalog.Replace(demo.Libraries(), []*model.Item{{
		ID: "custom-movie", Type: model.TypeMovie, Name: "Custom", SortName: "Custom",
		LibraryID: demo.MovieLibraryID, ParentID: demo.MovieLibraryID, AddedAt: now,
		Genres: []string{"Drama"}, People: []model.Person{{Name: "Alice", Type: "Actor"}},
	}})

	rec, data := doJSON(t, h, "GET", "/Genres?userId="+u.ID, tok, "")
	if rec.Code != 200 || int(data["TotalRecordCount"].(float64)) != 1 {
		t.Fatalf("genres = %d %v", rec.Code, data["TotalRecordCount"])
	}
	rec, data = doJSON(t, h, "GET", "/Persons?userId="+u.ID, tok, "")
	if rec.Code != 200 || int(data["TotalRecordCount"].(float64)) != 1 {
		t.Fatalf("persons = %d %v", rec.Code, data["TotalRecordCount"])
	}
	list, _ := data["Items"].([]any)
	if len(list) != 1 {
		t.Fatalf("persons items = %v", data["Items"])
	}
	person, _ := list[0].(map[string]any)
	if person["Name"] != "Alice" || person["Type"] != "Person" || len(person["Id"].(string)) != 32 {
		t.Fatalf("person = %v", person)
	}
	rec, data = doJSON(t, h, "GET", "/Items/"+personID("Alice")+"?userId="+u.ID, tok, "")
	if rec.Code != 200 || data["Type"] != "Person" || data["Name"] != "Alice" {
		t.Fatalf("person details = %d %v", rec.Code, data)
	}

	rec, data = doJSON(t, h, "GET", "/DisplayPreferences/p1?userId="+u.ID+"&client=test", tok, "")
	if rec.Code != 200 || data["Client"] != "test" {
		t.Fatalf("default prefs = %d %v", rec.Code, data)
	}
	rec, _ = doJSON(t, h, "POST", "/DisplayPreferences/p1?userId="+u.ID+"&client=test", tok, `{"SortBy":"DateCreated","ShowSidebar":false}`)
	if rec.Code != 204 {
		t.Fatalf("save prefs = %d", rec.Code)
	}
	rec, data = doJSON(t, h, "GET", "/DisplayPreferences/p1?userId="+u.ID+"&client=test", tok, "")
	if rec.Code != 200 || data["SortBy"] != "DateCreated" || data["ShowSidebar"] != false {
		t.Fatalf("saved prefs = %d %v", rec.Code, data)
	}
	rec, _ = doJSON(t, h, "GET", "/DisplayPreferences/p1?userId="+u.ID, tok, "")
	if rec.Code != 400 {
		t.Fatalf("missing client = %d, want 400", rec.Code)
	}
}

// The Jellyfin SDK deserializes UserItemDataDto.ItemId as a UUID and throws
// on an empty string, so every item/view must carry it.
func TestUserDataItemIDPresent(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	check := func(path string) {
		rec, data := doJSON(t, h, "GET", path, tok, "")
		if rec.Code != 200 {
			t.Fatalf("%s = %d", path, rec.Code)
		}
		items, _ := data["Items"].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			ud, _ := item["UserData"].(map[string]any)
			if ud == nil || ud["ItemId"] == "" || ud["ItemId"] == nil {
				t.Fatalf("%s: empty UserData.ItemId for %v", path, item["Id"])
			}
		}
	}
	check("/UserViews?userId=" + u.ID)
	check("/Items?userId=" + u.ID + "&limit=10")
	check("/Users/" + u.ID + "/Views")

	rec, data := doJSON(t, h, "GET", "/Items/"+demo.Items()[0].ID+"?userId="+u.ID, tok, "")
	if rec.Code != 200 {
		t.Fatalf("item details = %d", rec.Code)
	}
	ud, _ := data["UserData"].(map[string]any)
	if ud == nil || ud["ItemId"] == "" {
		t.Fatalf("item details: empty UserData.ItemId")
	}

	rec, data = doJSON(t, h, "GET", "/Items/Root", tok, "")
	if rec.Code == 200 {
		if ud, _ := data["UserData"].(map[string]any); ud == nil || ud["ItemId"] == "" {
			t.Fatal("root item: empty UserData.ItemId")
		}
	}
}

// Jellyfin's query binding is case-insensitive; clients such as Filebar send
// PascalCase names. Ignoring them merged every library into one list.
func TestQueryParameterCasing(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	rec, data := doJSON(t, h, "GET",
		"/Users/"+u.ID+"/Items?ParentId="+demo.MovieLibraryID+"&IncludeItemTypes=Movie&Limit=500", tok, "")
	if rec.Code != 200 {
		t.Fatalf("items = %d", rec.Code)
	}
	items, _ := data["Items"].([]any)
	if len(items) == 0 {
		t.Fatal("PascalCase ParentId returned no items")
	}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["Type"] != model.TypeMovie {
			t.Fatalf("library filter leaked %s", item["Type"])
		}
	}

	rec, _ = doJSON(t, h, "GET", "/UserViews?UserId="+u.ID, tok, "")
	if rec.Code != 200 {
		t.Fatalf("PascalCase UserId = %d", rec.Code)
	}

	req := httptest.NewRequest("GET", "/Users/"+u.ID+"/Items/Latest?ParentId="+demo.MovieLibraryID+"&Limit=50", nil)
	req.Header.Set("X-Emby-Token", tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var latest []itemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &latest); err != nil {
		t.Fatal(err)
	}
	if len(latest) == 0 {
		t.Fatal("PascalCase latest returned nothing")
	}
	for _, it := range latest {
		if it.Type != model.TypeMovie {
			t.Fatalf("latest leaked %s from another library", it.Type)
		}
	}
}

func TestLoginDecodesPercentEncodedDeviceName(t *testing.T) {
	a, h := newTestAPI(t)
	u, _ := testUser(t, a)

	req := httptest.NewRequest("POST", "/Users/AuthenticateByName", strings.NewReader(`{"Username":"alice","Pw":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Authorization",
		`MediaBrowser Client="Filebar", Device="%E7%9C%9F%E6%88%91GT6", DeviceId="filebar-1", Version="1.4.13"`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login = %d", rec.Code)
	}
	var body struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	sessions := a.Store.Sessions(u.ID, body.AccessToken)
	var name string
	for _, s := range sessions {
		if s.Current {
			name = s.DeviceName
		}
	}
	if name != "真我GT6" {
		t.Fatalf("device name = %q, want 真我GT6", name)
	}
}

// Browsers send header values as bytes: Chrome encodes "·" (U+00B7) as the
// single byte 0xB7. That is not valid UTF-8, and lowercasing it used to shift
// every offset after it, so Version="1.0.0" was parsed as ".0.0".
func TestMediaBrowserHeaderLatin1(t *testing.T) {
	a, h := newTestAPI(t)
	u, _ := testUser(t, a)

	req := httptest.NewRequest("POST", "/Users/AuthenticateByName", strings.NewReader(`{"Username":"alice","Pw":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Chrome/141.0 Safari/537.36")
	req.Header.Set("X-Emby-Authorization",
		"MediaBrowser Client=\"Nuvio Web\", Device=\"Linux \xb7 Chrome\", DeviceId=\"dev-1\", Version=\"1.0.0\"")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var got store.Session
	for _, s := range a.Store.Sessions(u.ID, body.AccessToken) {
		if s.Current {
			got = s
		}
	}
	if got.DeviceName != "Linux · Chrome" {
		t.Fatalf("device name = %q, want %q", got.DeviceName, "Linux · Chrome")
	}
	if got.Client != "Nuvio Web" {
		t.Fatalf("client = %q", got.Client)
	}
	if got.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0", got.Version)
	}
}

func TestMediaBrowserParamAfterMultibyte(t *testing.T) {
	header := `MediaBrowser Client="Nuvio Web", Device="Linux · Chrome", DeviceId="d", Version="1.0.0"`
	for name, want := range map[string]string{
		"Client":   "Nuvio Web",
		"Device":   "Linux · Chrome",
		"DeviceId": "d",
		"Version":  "1.0.0",
	} {
		if got := mediaBrowserParam(header, name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	// Invalid escapes must be left alone rather than half-decoded.
	if got := DecodeClientText("100%+ sure"); got != "100%+ sure" {
		t.Fatalf("decoded invalid escape: %q", got)
	}
}
