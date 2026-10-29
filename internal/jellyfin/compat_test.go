package jellyfin

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// Jellyfin's direct stream endpoint is anonymous: clients such as Findroid
// hand the URL straight to the player without any credentials.
func TestDirectStreamIsAnonymous(t *testing.T) {
	_, h := newTestAPI(t)
	movie := demo.Items()[0]

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/Videos/"+movie.ID+"/stream?static=true", nil))
	if rec.Code != 200 {
		t.Fatalf("anonymous stream = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "video/") {
		t.Fatalf("anonymous stream content type = %q", ct)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/Videos/"+movie.ID+"/stream.mp4", nil))
	if rec.Code != 200 {
		t.Fatalf("anonymous stream.{container} = %d", rec.Code)
	}

	// An explicitly supplied token is still checked.
	req := httptest.NewRequest("GET", "/Videos/"+movie.ID+"/stream?static=true", nil)
	req.Header.Set("X-Emby-Token", "definitely-not-a-token")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("invalid token stream = %d, want 401", rec.Code)
	}

	// HLS stays protected, matching the reference server.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/Videos/"+movie.ID+"/master.m3u8", nil))
	if rec.Code != 401 {
		t.Fatalf("anonymous master playlist = %d, want 401", rec.Code)
	}
}

// Subtitle streams must advertise a Jellyfin codec and a delivery URL that
// returns that format, otherwise clients pick the wrong parser.
func TestSubtitleDeliveryMatchesCodec(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	movie := demo.Items()[0]

	req := httptest.NewRequest("POST", "/Items/"+movie.ID+"/PlaybackInfo?userId="+u.ID, strings.NewReader(`{}`))
	req.Header.Set("X-Emby-Token", tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("playback info = %d", rec.Code)
	}
	var body struct {
		MediaSources []struct {
			MediaStreams []struct {
				Type                 string `json:"Type"`
				Codec                string `json:"Codec"`
				Title                string `json:"Title"`
				IsExternal           bool   `json:"IsExternal"`
				DeliveryMethod       string `json:"DeliveryMethod"`
				DeliveryUrl          string `json:"DeliveryUrl"`
				IsTextSubtitleStream bool   `json:"IsTextSubtitleStream"`
			} `json:"MediaStreams"`
		} `json:"MediaSources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.MediaSources) == 0 {
		t.Fatal("no media sources")
	}
	var sub *struct {
		Type                 string `json:"Type"`
		Codec                string `json:"Codec"`
		Title                string `json:"Title"`
		IsExternal           bool   `json:"IsExternal"`
		DeliveryMethod       string `json:"DeliveryMethod"`
		DeliveryUrl          string `json:"DeliveryUrl"`
		IsTextSubtitleStream bool   `json:"IsTextSubtitleStream"`
	}
	for i := range body.MediaSources[0].MediaStreams {
		if body.MediaSources[0].MediaStreams[i].Type == "Subtitle" {
			sub = &body.MediaSources[0].MediaStreams[i]
			break
		}
	}
	if sub == nil {
		t.Fatal("no subtitle stream")
	}
	if sub.Codec != "subrip" && sub.Codec != "webvtt" {
		t.Fatalf("codec = %q, want a Jellyfin subtitle codec", sub.Codec)
	}
	if !sub.IsExternal || sub.DeliveryMethod != "External" || !sub.IsTextSubtitleStream {
		t.Fatalf("subtitle stream flags = %+v", sub)
	}
	if sub.Title == "" {
		t.Fatal("subtitle stream has no Title")
	}
	want := "/Stream.srt"
	if sub.Codec == "webvtt" {
		want = "/Stream.vtt"
	}
	if !strings.Contains(sub.DeliveryUrl, want) {
		t.Fatalf("delivery url = %q, want suffix %q", sub.DeliveryUrl, want)
	}
	if !strings.Contains(sub.DeliveryUrl, "api_key=") {
		t.Fatalf("delivery url has no api key: %q", sub.DeliveryUrl)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", sub.DeliveryUrl, nil))
	if rec.Code != 200 {
		t.Fatalf("subtitle fetch = %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if sub.Codec == "subrip" && !strings.HasPrefix(ct, "application/x-subrip") {
		t.Fatalf("subrip delivery content type = %q", ct)
	}
	if sub.Codec == "webvtt" && !strings.HasPrefix(ct, "text/vtt") {
		t.Fatalf("webvtt delivery content type = %q", ct)
	}

	// The legacy ".vtt" spelling still resolves.
	legacy := strings.Replace(sub.DeliveryUrl, "Stream.srt", "Stream.vtt", 1)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", legacy, nil))
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/vtt") {
		t.Fatalf("legacy vtt = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestSubtitleDelivery(t *testing.T) {
	cases := []struct {
		sub         model.Subtitle
		codec, form string
	}{
		{model.Subtitle{Codec: "ass", Path: "/media/a.ass"}, "ass", "ass"},
		{model.Subtitle{Codec: "ssa", Path: "/media/a.ssa"}, "ass", "ass"},
		// Embedded ASS goes through the extraction pipeline, which yields VTT.
		{model.Subtitle{Codec: "ass", Embedded: true}, "subrip", "srt"},
		{model.Subtitle{Codec: "srt", Path: "/media/a.srt"}, "subrip", "srt"},
		{model.Subtitle{Codec: "vtt", Path: "/media/a.vtt"}, "webvtt", "vtt"},
		{model.Subtitle{Codec: ""}, "subrip", "srt"},
	}
	for _, c := range cases {
		codec, form := subtitleDelivery(c.sub)
		if codec != c.codec || form != c.form {
			t.Fatalf("subtitleDelivery(%+v) = %q/%q, want %q/%q", c.sub, codec, form, c.codec, c.form)
		}
	}
}

// The endpoint must serve SubRip for both the Jellyfin codec name and the
// familiar extension.
func TestSubtitleSubripRoute(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	movie := demo.Items()[0]
	url := a.subtitleURL(movie, 2, "srt", tok)
	if !strings.HasSuffix(strings.Split(url, "?")[0], "/Stream.srt") {
		t.Fatalf("subrip url = %q", url)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	if rec.Code != 200 {
		t.Fatalf("subrip = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-subrip") {
		t.Fatalf("subrip content type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "-->") || strings.HasPrefix(rec.Body.String(), "WEBVTT") {
		t.Fatalf("subrip body is not SubRip: %q", rec.Body.String()[:min(40, rec.Body.Len())])
	}
	_ = u
}

// The Jellyfin SDK models declare several MediaSourceInfo/MediaStream fields
// as required without defaults. kotlinx.serialization throws
// MissingFieldException when any of them is absent, which left clients stuck
// on a loading spinner for details and playback.
func TestSDKRequiredFieldsPresent(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	movie := demo.Items()[0]

	rec, item := doJSON(t, h, "GET", "/Items/"+movie.ID+"?userId="+u.ID, tok, "")
	if rec.Code != 200 {
		t.Fatalf("item = %d", rec.Code)
	}
	sources, _ := item["MediaSources"].([]any)
	if len(sources) == 0 {
		t.Fatal("item has no MediaSources")
	}
	src := sources[0].(map[string]any)
	for _, key := range []string{
		"Protocol", "Type", "IsRemote", "ReadAtNativeFramerate", "IgnoreDts",
		"IgnoreIndex", "GenPtsInput", "SupportsTranscoding", "SupportsDirectStream",
		"SupportsDirectPlay", "IsInfiniteStream", "RequiresOpening", "RequiresClosing",
		"RequiresLooping", "SupportsProbing", "TranscodingSubProtocol", "HasSegments",
	} {
		if _, ok := src[key]; !ok {
			t.Fatalf("MediaSourceInfo is missing required field %s: %v", key, src)
		}
	}
	streams, _ := src["MediaStreams"].([]any)
	if len(streams) == 0 {
		t.Fatal("media source has no streams")
	}
	for _, raw := range streams {
		stream := raw.(map[string]any)
		for _, key := range []string{
			"Type", "Index", "IsInterlaced", "IsDefault", "IsForced",
			"IsHearingImpaired", "IsOriginal", "IsExternal",
			"IsTextSubtitleStream", "SupportsExternalStream",
		} {
			if _, ok := stream[key]; !ok {
				t.Fatalf("MediaStream is missing required field %s: %v", key, stream)
			}
		}
	}
	ud, _ := item["UserData"].(map[string]any)
	for _, key := range []string{"ItemId", "Key", "PlaybackPositionTicks", "PlayCount", "IsFavorite", "Played"} {
		if _, ok := ud[key]; !ok {
			t.Fatalf("UserItemDataDto is missing required field %s: %v", key, ud)
		}
	}

	rec, info := doJSON(t, h, "POST", "/Items/"+movie.ID+"/PlaybackInfo?userId="+u.ID, tok, "{}")
	if rec.Code != 200 {
		t.Fatalf("playback info = %d", rec.Code)
	}
	if _, ok := info["MediaSources"].([]any); !ok {
		t.Fatal("PlaybackInfoResponse is missing MediaSources")
	}

	rec, hints := doJSON(t, h, "GET", "/Search/Hints?searchTerm=a&userId="+u.ID, tok, "")
	if rec.Code != 200 {
		t.Fatalf("search hints = %d", rec.Code)
	}
	list, _ := hints["SearchHints"].([]any)
	for _, raw := range list {
		hint := raw.(map[string]any)
		for _, key := range []string{"ItemId", "Id", "Name", "Type", "Artists"} {
			if _, ok := hint[key]; !ok {
				t.Fatalf("SearchHint is missing required field %s: %v", key, hint)
			}
		}
	}
}

// External ASS subtitles must be reported as ASS and served untouched, so
// players with a libass renderer keep the original typesetting.
func TestExternalASSServedUnchanged(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	dir := t.TempDir()
	path := filepath.Join(dir, "movie.zh.ass")
	script := "[Script Info]\nTitle: test\nPlayResX: 1920\nPlayResY: 1080\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,{\\pos(960,100)\\fad(0,200)}Sign\n"
	if err := os.WriteFile(path, []byte("\xEF\xBB\xBF"+script), 0o644); err != nil {
		t.Fatal(err)
	}
	item := &model.Item{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Type: model.TypeMovie, Name: "ASS",
		LibraryID: demo.MovieLibraryID, ParentID: demo.MovieLibraryID,
		Media: &model.MediaInfo{
			Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
			Subtitles: []model.Subtitle{{Index: 0, Codec: "ass", Path: path, Language: "zh"}},
		},
	}
	a.Catalog.Replace(demo.Libraries(), []*model.Item{item})

	rec, info := doJSON(t, h, "POST", "/Items/"+item.ID+"/PlaybackInfo?userId="+u.ID, tok, "{}")
	if rec.Code != 200 {
		t.Fatalf("playback info = %d", rec.Code)
	}
	streams := info["MediaSources"].([]any)[0].(map[string]any)["MediaStreams"].([]any)
	var sub map[string]any
	for _, raw := range streams {
		stream := raw.(map[string]any)
		if stream["Type"] == "Subtitle" {
			sub = stream
			break
		}
	}
	if sub == nil {
		t.Fatal("no subtitle stream")
	}
	if sub["Codec"] != "ass" {
		t.Fatalf("codec = %v, want ass", sub["Codec"])
	}
	url, _ := sub["DeliveryUrl"].(string)
	if !strings.Contains(url, "/Stream.ass") {
		t.Fatalf("delivery url = %q, want Stream.ass", url)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	if rec.Code != 200 {
		t.Fatalf("ass fetch = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/x-ssa") {
		t.Fatalf("ass content type = %q", ct)
	}
	if body := rec.Body.String(); body != script {
		t.Fatalf("ass body was modified:\n%q", body)
	}
}

func TestTranscodeTarget(t *testing.T) {
	build := func() *API {
		a := &API{}
		a.SetEncoder("libx264")
		a.SetSoftwareEncoders(map[string]string{"h264": "libx264", "av1": "libsvtav1"})
		a.SetHardware("/dev/dri/renderD128", "")
		a.SetCapabilities([]HardwareCapability{
			{Backend: "software", Encoders: []string{"h264", "av1"}},
			{Backend: "vaapi", Device: "/dev/dri/renderD128", Encoders: []string{"h264", "hevc"}},
		})
		return a
	}

	// AV1 is not available through VAAPI, so the software AV1 encoder wins.
	mode, device, codec, encoder := build().transcodeTarget("auto", "av1")
	if mode != "" || device != "" || codec != "av1" || encoder != "libsvtav1" {
		t.Fatalf("auto/av1 = %q %q %q %q", mode, device, codec, encoder)
	}

	// H.264 stays on the hardware backend.
	mode, device, codec, encoder = build().transcodeTarget("auto", "h264")
	if mode != "vaapi" || device != "/dev/dri/renderD128" || codec != "h264" || encoder != "libx264" {
		t.Fatalf("auto/h264 = %q %q %q %q", mode, device, codec, encoder)
	}

	// An explicit backend that has no render node must not be used.
	a := build()
	a.SetHardware("", "")
	mode, device, codec, _ = a.transcodeTarget("vaapi", "h264")
	if mode != "" || device != "" || codec != "h264" {
		t.Fatalf("missing device = %q %q %q", mode, device, codec)
	}

	// With neither hardware nor software support for HEVC, fall back to H.264.
	a = build()
	a.SetHardware("", "")
	mode, device, codec, encoder = a.transcodeTarget("none", "hevc")
	if mode != "" || device != "" || codec != "h264" || encoder != "libx264" {
		t.Fatalf("hevc fallback = %q %q %q %q", mode, device, codec, encoder)
	}
}

// TMDb artwork is served through the item image endpoints. The remote fetch is
// cached on disk, so the test pre-populates the cache and never hits the CDN.
func TestRemoteLogoAndEpisodeStill(t *testing.T) {
	a, h := newTestAPI(t)
	_, tok := testUser(t, a)
	a.ImageDir = t.TempDir()

	const logoURL = "https://image.tmdb.org/t/p/w500/logo.png"
	const stillURL = "https://image.tmdb.org/t/p/w300/still.jpg"
	const portraitURL = "https://image.tmdb.org/t/p/w185/portrait.jpg"
	const moviePosterURL = "https://image.tmdb.org/t/p/w780/movie.jpg"
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	jpeg := []byte{0xff, 0xd8, 0xff, 0xd9}
	cache := func(category, rawURL, ext string, data []byte) string {
		sum := md5.Sum([]byte(rawURL))
		path := filepath.Join(a.ImageDir, category, hex.EncodeToString(sum[:])+ext)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cache("tvshows", logoURL, ".png", png)
	cache("tvshows", stillURL, ".jpg", jpeg)
	cache("people", portraitURL, ".jpg", jpeg)
	cache("movies", moviePosterURL, ".jpg", jpeg)

	movieID := "logicalmovie"
	seriesID := "logicalseries"
	episodeID := "logicalepisode"
	personID := "logicalperson"
	items := append(demo.Items(),
		&model.Item{ID: seriesID, Type: model.TypeSeries, Name: "Logo Show", LibraryID: demo.ShowLibraryID, LogoURL: logoURL},
		&model.Item{ID: episodeID, Type: model.TypeEpisode, Name: "Episode 1", SeriesID: seriesID,
			LibraryID: demo.ShowLibraryID, SeasonNumber: 1, EpisodeNumber: 1, ThumbURL: stillURL},
		&model.Item{ID: personID, Type: "Person", Name: "Ada", PosterURL: portraitURL},
		&model.Item{ID: movieID, Type: model.TypeMovie, Name: "Film", LibraryID: demo.MovieLibraryID, PosterURL: moviePosterURL},
	)
	a.Catalog.Replace(demo.Libraries(), items)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-Emby-Token", tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := get("/Items/" + seriesID)
	if rec.Code != 200 {
		t.Fatalf("series status = %d: %s", rec.Code, rec.Body.String())
	}
	var series itemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &series); err != nil {
		t.Fatal(err)
	}
	if series.ImageTags["Logo"] == "" {
		t.Fatalf("series has no logo tag: %+v", series.ImageTags)
	}

	rec = get("/Items/" + episodeID)
	if rec.Code != 200 {
		t.Fatalf("episode status = %d: %s", rec.Code, rec.Body.String())
	}
	var episode itemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &episode); err != nil {
		t.Fatal(err)
	}
	if episode.ImageTags["Thumb"] == "" {
		t.Fatalf("episode has no thumb tag: %+v", episode.ImageTags)
	}
	if episode.PrimaryImageAspectRatio != 1.7778 {
		t.Fatalf("episode still aspect ratio = %v", episode.PrimaryImageAspectRatio)
	}

	if rec = get("/Items/" + seriesID + "/Images/Logo"); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), png) {
		t.Fatalf("logo image = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("logo content type = %q", ct)
	}
	if rec = get("/Items/" + episodeID + "/Images/Thumb"); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), jpeg) {
		t.Fatalf("thumb image = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if rec = get("/Items/" + episodeID + "/Images/Primary"); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), jpeg) {
		t.Fatalf("episode primary should use the still: %d", rec.Code)
	}
	if rec = get("/Items/" + personID + "/Images/Primary"); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), jpeg) {
		t.Fatalf("person portrait = %d", rec.Code)
	}
	if rec = get("/Items/" + movieID + "/Images/Primary"); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), jpeg) {
		t.Fatalf("movie poster = %d", rec.Code)
	}
	for _, dir := range []string{"movies", "tvshows", "people"} {
		if st, err := os.Stat(filepath.Join(a.ImageDir, dir)); err != nil || !st.IsDir() {
			t.Fatalf("expected cache directory %s: %v", dir, err)
		}
	}
}
