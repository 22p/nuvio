package jellyfin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nuvio/internal/catalog"
	"nuvio/internal/demo"
	"nuvio/internal/hls"
	"nuvio/internal/model"
	"nuvio/internal/store"
)

func newRuntimeAPI(t testing.TB) (*API, http.Handler) {
	t.Helper()
	a, h := newTestAPI(t)
	a.HLS = hls.New(t.TempDir(), func() string { return "" })
	return a, h
}

func catalogItem(t testing.TB, a *API, typ string, withMedia bool) *model.Item {
	t.Helper()
	for _, it := range a.Catalog.All() {
		if it.Type != typ || (withMedia && it.Media == nil) {
			continue
		}
		return it
	}
	t.Fatalf("demo catalog has no %s (media=%v)", typ, withMedia)
	return nil
}

func serve(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if token != "" {
		setToken(req, token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func hyphenateID(id string) string {
	if len(id) != 32 {
		return id
	}
	return id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}

// --- functional -----------------------------------------------------------

func TestPlaybackInfoDirectPlayContract(t *testing.T) {
	a, h := newRuntimeAPI(t)
	u, tok := testUser(t, a)
	movie := catalogItem(t, a, model.TypeMovie, true)

	body := fmt.Sprintf("{\"UserId\":%q,\"EnableDirectPlay\":true,\"EnableTranscoding\":false,\"DeviceProfile\":{\"DirectPlayProfiles\":[{\"Container\":\"mp4\",\"Type\":\"Video\",\"VideoCodec\":\"h264\",\"AudioCodec\":\"aac\"}]}}", u.ID)
	rec := serve(t, h, http.MethodPost, "/Items/"+movie.ID+"/PlaybackInfo?userId="+u.ID, tok, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out playbackInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.MediaSources) != 1 {
		t.Fatalf("media sources = %d", len(out.MediaSources))
	}
	if out.PlaySessionId == "" {
		t.Fatal("PlaySessionId is empty")
	}
	src := out.MediaSources[0]
	if !src.SupportsDirectPlay || !src.SupportsDirectStream {
		t.Fatalf("direct flags = play:%v stream:%v", src.SupportsDirectPlay, src.SupportsDirectStream)
	}
	if src.SupportsTranscoding || src.TranscodingUrl != "" {
		t.Fatalf("transcoding advertised without ffmpeg: %v %q", src.SupportsTranscoding, src.TranscodingUrl)
	}
	if src.Container != "mp4" || src.Protocol != "File" || src.Type != "Default" {
		t.Fatalf("source = %+v", src)
	}
	if src.DefaultAudioStreamIndex != 1 || src.DefaultSubtitleStreamIndex != -1 {
		t.Fatalf("default stream indexes = %d/%d", src.DefaultAudioStreamIndex, src.DefaultSubtitleStreamIndex)
	}
	counts := map[string]int{}
	for _, s := range src.MediaStreams {
		counts[s.Type]++
		if s.Type == "Subtitle" && !strings.Contains(s.DeliveryUrl, "ApiKey=") {
			t.Fatalf("subtitle delivery url has no token: %q", s.DeliveryUrl)
		}
	}
	if counts["Video"] != 1 || counts["Audio"] != 1 || counts["Subtitle"] < 1 {
		t.Fatalf("stream counts = %+v", counts)
	}
}

func TestPlaybackInfoFolderReportsNoCompatibleStream(t *testing.T) {
	a, h := newRuntimeAPI(t)
	u, tok := testUser(t, a)
	series := catalogItem(t, a, model.TypeSeries, false)

	rec := serve(t, h, http.MethodPost, "/Items/"+series.ID+"/PlaybackInfo?userId="+u.ID, tok, "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out playbackInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.MediaSources) != 0 || out.ErrorCode != "NoCompatibleStream" {
		t.Fatalf("folder playback info = %+v", out)
	}
	if out.PlaySessionId == "" {
		t.Fatal("PlaySessionId is empty")
	}
}

func TestLibraryVisibilityFiltersViewsAndItems(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	if err := a.Store.UpdateUserPolicy(u.ID, []string{demo.MovieLibraryID}, true, true); err != nil {
		t.Fatal(err)
	}

	rec := serve(t, h, http.MethodGet, "/UserViews?userId="+u.ID, tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("views status = %d", rec.Code)
	}
	var views queryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views.Items) != 1 || views.Items[0].Id != demo.MovieLibraryID {
		t.Fatalf("views = %+v", views.Items)
	}

	rec = serve(t, h, http.MethodGet, "/Items?parentId="+demo.ShowLibraryID+"&userId="+u.ID, tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("items status = %d", rec.Code)
	}
	var items queryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items.Items) != 0 || items.TotalRecordCount != 0 {
		t.Fatalf("restricted library leaked %d items", len(items.Items))
	}

	episode := catalogItem(t, a, model.TypeEpisode, false)
	rec = serve(t, h, http.MethodGet, "/Items/"+episode.ID+"?userId="+u.ID, tok, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("restricted item status = %d", rec.Code)
	}

	admin, err := a.Store.CreateUser("root", "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	adminTok, err := a.Store.IssueToken(admin.ID, store.SessionMeta{})
	if err != nil {
		t.Fatal(err)
	}
	rec = serve(t, h, http.MethodGet, "/UserViews?userId="+admin.ID, adminTok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin views status = %d", rec.Code)
	}
	var adminViews queryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &adminViews); err != nil {
		t.Fatal(err)
	}
	if len(adminViews.Items) != 2 {
		t.Fatalf("admin views = %d", len(adminViews.Items))
	}
}

func TestResumeLifecycle(t *testing.T) {
	a, h := newRuntimeAPI(t)
	u, tok := testUser(t, a)
	movie := catalogItem(t, a, model.TypeMovie, true)

	body := fmt.Sprintf("{\"ItemId\":%q,\"PositionTicks\":5000000,\"IsPaused\":false}", movie.ID)
	rec := serve(t, h, http.MethodPost, "/Sessions/Playing/Progress", tok, body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("progress status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = serve(t, h, http.MethodGet, "/UserItems/Resume?userId="+u.ID, tok, "")
	var resume queryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &resume); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range resume.Items {
		if it.Id == movie.ID {
			found = true
			if it.UserData == nil || it.UserData.PlaybackPositionTicks != 5_000_000 || it.UserData.PlayedPercentage <= 0 {
				t.Fatalf("resume user data = %+v", it.UserData)
			}
		}
	}
	if !found {
		t.Fatal("movie missing from resume")
	}

	rec = serve(t, h, http.MethodPost, "/UserPlayedItems/"+movie.ID+"?userId="+u.ID, tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("played status = %d", rec.Code)
	}
	var ud userItemData
	if err := json.Unmarshal(rec.Body.Bytes(), &ud); err != nil {
		t.Fatal(err)
	}
	if !ud.Played || ud.PlaybackPositionTicks != 0 || ud.ItemId != movie.ID {
		t.Fatalf("played user data = %+v", ud)
	}

	rec = serve(t, h, http.MethodGet, "/UserItems/Resume?userId="+u.ID, tok, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &resume); err != nil {
		t.Fatal(err)
	}
	for _, it := range resume.Items {
		if it.Id == movie.ID {
			t.Fatal("played movie still in resume")
		}
	}

	rec = serve(t, h, http.MethodDelete, "/UserPlayedItems/"+movie.ID+"?userId="+u.ID, tok, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &ud); err != nil {
		t.Fatal(err)
	}
	if ud.Played {
		t.Fatalf("unmark played failed: %+v", ud)
	}
}

func TestFavoriteLifecycle(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	movie := catalogItem(t, a, model.TypeMovie, true)

	rec := serve(t, h, http.MethodPost, "/UserFavoriteItems/"+movie.ID+"?userId="+u.ID, tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("favorite status = %d", rec.Code)
	}
	var ud userItemData
	if err := json.Unmarshal(rec.Body.Bytes(), &ud); err != nil {
		t.Fatal(err)
	}
	if !ud.IsFavorite {
		t.Fatalf("favorite not set: %+v", ud)
	}

	rec = serve(t, h, http.MethodGet, "/UserItems/"+movie.ID+"/UserData?userId="+u.ID, tok, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &ud); err != nil {
		t.Fatal(err)
	}
	if !ud.IsFavorite {
		t.Fatalf("user data lost favorite: %+v", ud)
	}

	rec = serve(t, h, http.MethodDelete, "/UserFavoriteItems/"+movie.ID+"?userId="+u.ID, tok, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &ud); err != nil {
		t.Fatal(err)
	}
	if ud.IsFavorite {
		t.Fatalf("favorite not cleared: %+v", ud)
	}
}

func nextUpFirst(t *testing.T, h http.Handler, token, userID, seriesID string) *itemDTO {
	t.Helper()
	rec := serve(t, h, http.MethodGet, "/Shows/NextUp?seriesId="+seriesID+"&userId="+userID, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("next up status = %d", rec.Code)
	}
	var out queryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) == 0 {
		t.Fatal("next up returned no episode")
	}
	return &out.Items[0]
}

func TestNextUpAdvancesAfterWatched(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	series := catalogItem(t, a, model.TypeSeries, false)
	eps, _ := a.Catalog.Query(catalog.Query{ParentID: series.ID, Recursive: true, Types: []string{model.TypeEpisode}}, nil)
	if len(eps) < 2 {
		t.Skip("series needs at least two episodes")
	}

	first := nextUpFirst(t, h, tok, u.ID, series.ID)
	rec := serve(t, h, http.MethodPost, "/UserPlayedItems/"+first.Id+"?userId="+u.ID, tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("mark played status = %d", rec.Code)
	}
	second := nextUpFirst(t, h, tok, u.ID, series.ID)
	if second.Id == first.Id {
		t.Fatalf("next up did not advance after watching %s", first.Id)
	}
}

func TestStreamRangeAndHead(t *testing.T) {
	a, h := newTestAPI(t)
	_, tok := testUser(t, a)
	movie := catalogItem(t, a, model.TypeMovie, true)
	streamURL := "/Videos/" + movie.ID + "/stream?static=true"

	req := httptest.NewRequest(http.MethodGet, streamURL, nil)
	setToken(req, tok)
	req.Header.Set("Range", "bytes=0-99")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d", rec.Code)
	}
	if rec.Body.Len() != 100 {
		t.Fatalf("range length = %d", rec.Body.Len())
	}
	if got := rec.Header().Get("Content-Range"); !strings.HasPrefix(got, "bytes 0-99/") {
		t.Fatalf("content range = %q", got)
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("accept ranges = %q", rec.Header().Get("Accept-Ranges"))
	}

	req = httptest.NewRequest(http.MethodGet, streamURL, nil)
	setToken(req, tok)
	req.Header.Set("Range", "bytes=999999999-")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("unsatisfiable range status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodHead, streamURL, nil)
	setToken(req, tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("head status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("head returned a body of %d bytes", rec.Body.Len())
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Fatal("head has no Content-Length")
	}
}

func TestImageETagAndIndexBoundaries(t *testing.T) {
	a, h := newTestAPI(t)
	movie := catalogItem(t, a, model.TypeMovie, true)
	base := "/Items/" + movie.ID + "/Images/Primary"

	rec := serve(t, h, http.MethodGet, base+"?maxWidth=120", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("image status = %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("image has no ETag")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type = %q", ct)
	}

	req := httptest.NewRequest(http.MethodGet, base+"?maxWidth=120", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional image status = %d", rec.Code)
	}

	cases := []struct {
		path string
		want int
	}{
		{base + "/0", http.StatusOK},
		{base + "/1", http.StatusNotFound},
		{base + "/-1", http.StatusNotFound},
		{base + "/abc", http.StatusNotFound},
		{"/Items/" + movie.ID + "/Images/Bogus", http.StatusNotFound},
	}
	for _, tc := range cases {
		if got := serve(t, h, http.MethodGet, tc.path, "", "").Code; got != tc.want {
			t.Fatalf("%s = %d, want %d", tc.path, got, tc.want)
		}
	}
}

func TestItemsPaginationBoundaries(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	all := 0
	for _, it := range a.Catalog.All() {
		switch it.Type {
		case model.TypeMovie, model.TypeSeries, model.TypeSeason, model.TypeEpisode:
			all++
		}
	}
	cases := []struct {
		name      string
		query     string
		wantItems int
		wantTotal int
	}{
		{"limit one", "startIndex=0&limit=1", 1, all},
		{"negative start", "startIndex=-5&limit=2", 2, all},
		{"beyond end", "startIndex=99999&limit=10", 0, all},
		{"zero limit", "limit=0", all, all},
		{"negative limit", "limit=-3", all, all},
		{"max limit", "startIndex=1&limit=9223372036854775807", all - 1, all},
		{"max start", "startIndex=9223372036854775807&limit=2", 0, all},
		{"disabled total", "limit=1&enableTotalRecordCount=false", 1, 0},
		{"garbage values", "startIndex=abc&limit=xyz", all, all},
	}
	for _, tc := range cases {
		rec := serve(t, h, http.MethodGet, "/Items?userId="+u.ID+"&"+tc.query, tok, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", tc.name, rec.Code)
		}
		var out queryResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(out.Items) != tc.wantItems || out.TotalRecordCount != tc.wantTotal {
			t.Fatalf("%s: items=%d total=%d, want %d/%d", tc.name, len(out.Items), out.TotalRecordCount, tc.wantItems, tc.wantTotal)
		}
	}
}

func TestEmptyCatalogEndpoints(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	a.Catalog.Replace(nil, nil)

	for _, path := range []string{
		"/Items?userId=" + u.ID,
		"/UserViews?userId=" + u.ID,
		"/Genres?userId=" + u.ID,
		"/Persons?userId=" + u.ID,
	} {
		rec := serve(t, h, http.MethodGet, path, tok, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", path, rec.Code)
		}
		var out queryResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(out.Items) != 0 || out.TotalRecordCount != 0 {
			t.Fatalf("%s: %+v", path, out)
		}
	}

	rec := serve(t, h, http.MethodGet, "/Items/Latest?userId="+u.ID, tok, "")
	var latest []itemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &latest); err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(latest) != 0 {
		t.Fatalf("latest = %d items", len(latest))
	}
	if rec := serve(t, h, http.MethodGet, "/Items/Root?userId="+u.ID, tok, ""); rec.Code != http.StatusOK {
		t.Fatalf("root status = %d", rec.Code)
	}
}

func TestIDNormalizationAndNotFound(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	movie := catalogItem(t, a, model.TypeMovie, true)

	rec := serve(t, h, http.MethodGet, "/Items/"+hyphenateID(movie.ID)+"?userId="+u.ID, tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("hyphenated id status = %d: %s", rec.Code, rec.Body.String())
	}
	var out itemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Id != movie.ID {
		t.Fatalf("id = %q, want %q", out.Id, movie.ID)
	}

	rec = serve(t, h, http.MethodGet, "/Items/deadbeef?userId="+u.ID, tok, "")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("unknown item = %d %q", rec.Code, rec.Body.String())
	}
}

func TestSearchHintTermBoundaries(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	for _, term := range []string{"", "   "} {
		rec := serve(t, h, http.MethodGet, "/Search/Hints?userId="+u.ID+"&searchTerm="+url.QueryEscape(term), tok, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("term %q status = %d", term, rec.Code)
		}
	}

	long := strings.Repeat("x", 4096)
	cases := []string{
		"/Search/Hints?userId=" + u.ID + "&searchTerm=" + url.QueryEscape(long),
		"/Search/Hints?userId=" + u.ID + "&searchTerm=" + url.QueryEscape("星际"),
		"/Search/Hints?userId=" + u.ID + "&searchTerm=a&limit=-1",
		"/Search/Hints?userId=" + u.ID + "&searchTerm=a&includeItemTypes=Bogus",
	}
	for _, path := range cases {
		rec := serve(t, h, http.MethodGet, path, tok, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", path, rec.Code)
		}
		if !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("%s: invalid json", path)
		}
	}
}

// Hostile paging values must be clamped on every paged endpoint, not just
// /Items. This used to panic with a slice bounds error on several routes.
func TestPagedEndpointsRejectHostileBounds(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	series := catalogItem(t, a, model.TypeSeries, false)

	paths := []string{
		"/Items?userId=" + u.ID,
		"/UserItems/Resume?userId=" + u.ID,
		"/Genres?userId=" + u.ID,
		"/Persons?userId=" + u.ID,
		"/Shows/NextUp?userId=" + u.ID,
		"/Shows/NextUp?userId=" + u.ID + "&seriesId=" + series.ID,
		"/Search/Hints?userId=" + u.ID + "&searchTerm=a",
		"/Items/Suggestions?userId=" + u.ID,
		"/Items/Latest?userId=" + u.ID,
	}
	queries := []string{
		"startIndex=-5&limit=2",
		"startIndex=9223372036854775807&limit=2",
		"startIndex=1&limit=9223372036854775807",
		"startIndex=-9223372036854775808&limit=-1",
		"startIndex=abc&limit=xyz",
	}
	for _, path := range paths {
		for _, q := range queries {
			sep := "&"
			if !strings.Contains(path, "?") {
				sep = "?"
			}
			rec := serve(t, h, http.MethodGet, path+sep+q, tok, "")
			if rec.Code >= 500 {
				t.Fatalf("%s&%s = %d: %s", path, q, rec.Code, rec.Body.String())
			}
		}
	}
}

// --- exception / error handling ------------------------------------------

func TestAuthFailures(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)

	if rec := serve(t, h, http.MethodGet, "/Items", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d", rec.Code)
	}
	if rec := serve(t, h, http.MethodGet, "/Items?ApiKey=wrong", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("bad token status = %d", rec.Code)
	}
	if rec := serve(t, h, http.MethodGet, "/Users/Me?ApiKey="+tok, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("query token status = %d", rec.Code)
	}

	admin, err := a.Store.CreateUser("root", "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	adminTok, err := a.Store.IssueToken(admin.ID, store.SessionMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if rec := serve(t, h, http.MethodGet, "/Users/"+admin.ID+"?ApiKey="+tok, "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin other user status = %d", rec.Code)
	}
	if rec := serve(t, h, http.MethodGet, "/Users/"+u.ID+"?ApiKey="+adminTok, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("admin other user status = %d", rec.Code)
	}
}

func TestMalformedAndOversizedBodies(t *testing.T) {
	a, h := newRuntimeAPI(t)
	u, tok := testUser(t, a)
	movie := catalogItem(t, a, model.TypeMovie, true)

	huge := strings.Repeat("a", 2<<20)
	cases := []struct {
		name  string
		path  string
		body  string
		token string
	}{
		{"login malformed", "/Users/AuthenticateByName", "{", ""},
		{"login empty", "/Users/AuthenticateByName", "", ""},
		{"login oversized", "/Users/AuthenticateByName", huge, ""},
		{"progress malformed", "/Sessions/Playing", "{", tok},
		{"progress empty", "/Sessions/Playing", "", tok},
		{"progress oversized", "/Sessions/Playing", huge, tok},
		{"userdata malformed", "/UserItems/" + movie.ID + "/UserData?userId=" + u.ID, "{", tok},
		{"capabilities malformed", "/Sessions/Capabilities/Full", "{", tok},
	}
	for _, tc := range cases {
		rec := serve(t, h, http.MethodPost, tc.path, tc.token, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d: %s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

func TestMethodNotAllowedAndUnknownRoutes(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPut, "/Items", http.StatusMethodNotAllowed},
		{http.MethodPost, "/System/Info/Public", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/Users/Me", http.StatusMethodNotAllowed},
		{http.MethodGet, "/Definitely/Not/A/Route", http.StatusNotFound},
		{http.MethodGet, "/Items/doesnotexist?userId=" + u.ID, http.StatusNotFound},
	}
	for _, tc := range cases {
		if got := serve(t, h, tc.method, tc.path, tok, "").Code; got != tc.want {
			t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestPlaybackInfoInvalidMediaSource(t *testing.T) {
	a, h := newRuntimeAPI(t)
	u, tok := testUser(t, a)
	movie := catalogItem(t, a, model.TypeMovie, true)

	rec := serve(t, h, http.MethodPost, "/Items/"+movie.ID+"/PlaybackInfo?userId="+u.ID, tok, "{\"MediaSourceId\":\"deadbeef\"}")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad media source status = %d", rec.Code)
	}
	rec = serve(t, h, http.MethodPost, "/Items/deadbeef/PlaybackInfo?userId="+u.ID, tok, "{}")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown item status = %d", rec.Code)
	}
}

func TestSubtitleRouteBoundaries(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	movie := catalogItem(t, a, model.TypeMovie, true)
	base := "/Videos/" + movie.ID + "/" + a.mediaSourceID(movie) + "/Subtitles/"

	rec := serve(t, h, http.MethodGet, base+"2/Stream.vtt", tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("vtt status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "WEBVTT") {
		t.Fatal("vtt body has no WEBVTT header")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/vtt") {
		t.Fatalf("vtt content type = %q", ct)
	}

	rec = serve(t, h, http.MethodGet, base+"2/Stream.srt", tok, "")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/x-subrip") {
		t.Fatalf("srt = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	if rec := serve(t, h, http.MethodGet, base+"999/Stream.vtt", tok, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown subtitle index = %d", rec.Code)
	}
	if rec := serve(t, h, http.MethodGet, base+"2/Stream.xyz", tok, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unsupported subtitle format = %d", rec.Code)
	}
	if rec := serve(t, h, http.MethodGet, base+"2/Stream.vtt", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("subtitle without token = %d", rec.Code)
	}

	series := catalogItem(t, a, model.TypeSeries, false)
	if rec := serve(t, h, http.MethodGet, "/Videos/"+series.ID+"/"+a.mediaSourceID(movie)+"/Subtitles/2/Stream.vtt", tok, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("folder subtitle = %d", rec.Code)
	}
	_ = u
}
