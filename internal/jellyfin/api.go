// Package jellyfin implements the subset of the Jellyfin HTTP API that Nuvio
// supports. The web client bundled with the server uses the same endpoints as
// native Jellyfin clients: there is no private side channel for playback,
// browsing or progress reporting.
package jellyfin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nuvio/internal/catalog"
	"nuvio/internal/hls"
	"nuvio/internal/model"
	"nuvio/internal/store"
)

// Version is Nuvio's own version.
const Version = "1.0.0"

// ApiVersion is the Jellyfin server version we advertise. Native clients gate
// features on this string.
const ApiVersion = "10.10.7"

// API holds the shared server dependencies.
type API struct {
	Store    *store.Store
	Catalog  *catalog.Catalog
	HLS      *hls.Manager
	DataDir  string
	ImageDir string
	DemoFile string
	Started  time.Time

	toolsMu   sync.RWMutex
	ffmpeg    string
	ffprobe   string
	encoder   string
	imageMu   sync.Mutex
	imageHTTP *http.Client
}

// SetTools updates ffmpeg/ffprobe paths at runtime.
func (a *API) SetTools(ffmpeg, ffprobe string) {
	a.toolsMu.Lock()
	a.ffmpeg, a.ffprobe = ffmpeg, ffprobe
	a.toolsMu.Unlock()
}

// SetEncoder records the H.264 encoder used for transcoding.
func (a *API) SetEncoder(name string) {
	a.toolsMu.Lock()
	a.encoder = name
	a.toolsMu.Unlock()
}

// Encoder returns the selected H.264 encoder.
func (a *API) Encoder() string {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	return a.encoder
}

// FFmpeg returns the configured ffmpeg binary (may be empty).
func (a *API) FFmpeg() string {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	return a.ffmpeg
}

// FFprobe returns the configured ffprobe binary (may be empty).
func (a *API) FFprobe() string {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	return a.ffprobe
}

// Register installs all Jellyfin compatible routes on mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /System/Info/Public", a.handlePublicInfo)
	mux.HandleFunc("GET /System/Info", a.handleSystemInfo)
	mux.HandleFunc("GET /System/Configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{})
	})
	mux.HandleFunc("GET /Branding/Configuration", a.handleBranding)
	mux.HandleFunc("POST /Users/AuthenticateByName", a.handleAuthenticate)
	mux.HandleFunc("GET /Users/Public", a.handlePublicUsers)
	mux.HandleFunc("GET /Users/Me", a.handleMe)
	mux.HandleFunc("GET /Users/{userId}", a.handleUser)
	mux.HandleFunc("GET /Users/{userId}/Views", a.handleViews)
	mux.HandleFunc("GET /UserViews", a.handleUserViews)
	mux.HandleFunc("GET /Users/{userId}/Items", a.handleItems)
	mux.HandleFunc("GET /Users/{userId}/Items/Resume", a.handleResume)
	mux.HandleFunc("GET /Users/{userId}/Items/Latest", a.handleLatest)
	mux.HandleFunc("GET /Users/{userId}/Items/{itemId}", a.handleItem)
	mux.HandleFunc("GET /Items/{itemId}", a.handleItem)
	mux.HandleFunc("GET /Items/{itemId}/Similar", a.handleSimilar)
	mux.HandleFunc("GET /Shows/{seriesId}/Seasons", a.handleSeasons)
	mux.HandleFunc("GET /Shows/{seriesId}/Episodes", a.handleEpisodes)
	mux.HandleFunc("GET /Shows/NextUp", a.handleNextUp)
	mux.HandleFunc("GET /Search/Hints", a.handleSearchHints)
	mux.HandleFunc("POST /Users/{userId}/PlayedItems/{itemId}", a.handlePlayed(true))
	mux.HandleFunc("DELETE /Users/{userId}/PlayedItems/{itemId}", a.handlePlayed(false))
	mux.HandleFunc("POST /Users/{userId}/FavoriteItems/{itemId}", a.handleFavorite(true))
	mux.HandleFunc("DELETE /Users/{userId}/FavoriteItems/{itemId}", a.handleFavorite(false))
	mux.HandleFunc("POST /Sessions/Playing", a.handlePlaying)
	mux.HandleFunc("POST /Sessions/Playing/Progress", a.handlePlaying)
	mux.HandleFunc("POST /Sessions/Playing/Stopped", a.handleStopped)
	mux.HandleFunc("POST /Sessions/Capabilities/Full", a.handleCapabilities)
	mux.HandleFunc("POST /Sessions/Playing/Ping", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("POST /Users/{userId}/PlayingItems/{itemId}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("DELETE /Users/{userId}/PlayingItems/{itemId}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /Items/Root", a.handleRoot)
	mux.HandleFunc("GET /Genres", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, queryResult{Items: []itemDTO{}, TotalRecordCount: 0, StartIndex: 0})
	})
	mux.HandleFunc("GET /Persons", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, queryResult{Items: []itemDTO{}, TotalRecordCount: 0, StartIndex: 0})
	})
	mux.HandleFunc("GET /DisplayPreferences/{id}", a.handleDisplayPreferences)
	mux.HandleFunc("POST /DisplayPreferences/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	a.registerMedia(mux)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func (a *API) tokenFromRequest(r *http.Request) string {
	if v := r.Header.Get("X-Emby-Token"); v != "" {
		return v
	}
	if v := r.Header.Get("X-MediaBrowser-Token"); v != "" {
		return v
	}
	q := r.URL.Query()
	if v := q.Get("api_key"); v != "" {
		return v
	}
	if v := q.Get("ApiKey"); v != "" {
		return v
	}
	h := r.Header.Get("Authorization")
	if h != "" {
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			return strings.TrimSpace(h[7:])
		}
		if i := strings.Index(strings.ToLower(h), "token="); i >= 0 {
			v := h[i+6:]
			v = strings.TrimPrefix(v, `"`)
			if j := strings.IndexAny(v, `",`); j >= 0 {
				v = v[:j]
			}
			return strings.TrimSpace(strings.Trim(v, `"`))
		}
	}
	return ""
}

func (a *API) userFromRequest(r *http.Request) (*model.User, bool) {
	tok := a.tokenFromRequest(r)
	if tok == "" {
		return nil, false
	}
	return a.Store.UserByToken(tok)
}

func (a *API) requireUser(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	u, ok := a.userFromRequest(r)
	if !ok {
		writeError(w, 401, "Not signed in")
		return nil, false
	}
	return u, true
}

// resolveUser returns the user identified by pathID when the caller is an
// administrator, otherwise the token owner.
func (a *API) resolveUser(w http.ResponseWriter, r *http.Request, pathID string) (*model.User, bool) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return nil, false
	}
	if pathID == "" || pathID == u.ID {
		return u, true
	}
	if !u.IsAdmin {
		writeError(w, 403, "You cannot access this user")
		return nil, false
	}
	target, ok := a.Store.FindUser(pathID)
	if !ok {
		writeError(w, 404, "User not found")
		return nil, false
	}
	return target, true
}

// canSeeLibrary reports whether the user may browse a library. Admins and
// users without an explicit restriction see everything.
func canSeeLibrary(u *model.User, libraryID string) bool {
	if u == nil || u.IsAdmin || len(u.AllowedLibraries) == 0 {
		return true
	}
	for _, id := range u.AllowedLibraries {
		if id == libraryID {
			return true
		}
	}
	return false
}

func canSeeItem(u *model.User, it *model.Item) bool {
	return it != nil && canSeeLibrary(u, it.LibraryID)
}

// Public endpoints --------------------------------------------------------

func (a *API) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, itemDTO{
		Name: "Nuvio", ServerId: a.Store.ServerID(), Id: "root", Type: "CollectionFolder",
		IsFolder: true, ImageTags: map[string]string{}, BackdropImageTags: []string{},
		ParentBackdropImageTags: []string{}, PlayAccess: "Full", LocationType: "FileSystem",
		UserData: &userItemData{Key: "root"},
	})
}

func (a *API) handlePublicInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"LocalAddress":           "",
		"ServerName":             a.Store.Settings().ServerName,
		"Version":                ApiVersion,
		"ProductName":            "Jellyfin Server",
		"OperatingSystem":        "Linux",
		"Architecture":           "arm64",
		"Id":                     a.Store.ServerID(),
		"StartupWizardCompleted": a.Store.SetupComplete(),
	})
}

func (a *API) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	s := a.Store.Settings()
	writeJSON(w, 200, map[string]any{
		"ServerName":               s.ServerName,
		"Version":                  ApiVersion,
		"ProductName":              "Jellyfin Server",
		"Id":                       a.Store.ServerID(),
		"OperatingSystem":          "Linux",
		"Architecture":             "arm64",
		"SystemUpdateLevel":        "Release",
		"HasPendingRestart":        false,
		"IsShuttingDown":           false,
		"SupportsLibraryMonitor":   false,
		"WebSocketPortNumber":      0,
		"CanSelfRestart":           false,
		"CanLaunchWebBrowser":      false,
		"ProgramDataPath":          a.DataDir,
		"WebPath":                  "/",
		"ItemsByNamePath":          "",
		"CachePath":                a.DataDir,
		"LogPath":                  a.DataDir,
		"InternalMetadataPath":     a.DataDir,
		"TranscodingTempPath":      a.DataDir,
		"CompletedInstallations":   []any{},
		"CastReceiverApplications": []any{},
	})
}

func (a *API) handleBranding(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"LoginDisclaimer":     "",
		"CustomCss":           "",
		"SplashscreenEnabled": false,
	})
}

func (a *API) handlePublicUsers(w http.ResponseWriter, r *http.Request) {
	users := a.Store.Users()
	out := make([]userDTO, 0, len(users))
	for i := range users {
		out = append(out, buildUserDTO(&users[i], a.Store.ServerID()))
	}
	writeJSON(w, 200, out)
}

func (a *API) handleAuthenticate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"Username"`
		Pw       string `json:"Pw"`
		Password string `json:"Password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, 400, "Invalid request format")
		return
	}
	pass := body.Pw
	if pass == "" {
		pass = body.Password
	}
	u, ok := a.Store.Authenticate(strings.TrimSpace(body.Username), pass)
	if !ok {
		writeError(w, 401, "Invalid username or password")
		return
	}
	token, err := a.Store.IssueToken(u.ID)
	if err != nil {
		writeError(w, 500, "Could not create a session")
		return
	}
	now := time.Now().UTC()
	writeJSON(w, 200, map[string]any{
		"User":        buildUserDTO(u, a.Store.ServerID()),
		"AccessToken": token,
		"ServerId":    a.Store.ServerID(),
		"SessionInfo": map[string]any{
			"Id":                    randomHex(12),
			"UserId":                u.ID,
			"UserName":              u.Name,
			"DeviceId":              r.Header.Get("X-Emby-Device-Id"),
			"DeviceName":            r.Header.Get("X-Emby-Device-Name"),
			"Client":                r.Header.Get("X-Emby-Client"),
			"ApplicationVersion":    r.Header.Get("X-Emby-Client-Version"),
			"ServerId":              a.Store.ServerID(),
			"LastActivityDate":      now.Format(time.RFC3339),
			"IsActive":              true,
			"SupportsRemoteControl": false,
		},
	})
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, buildUserDTO(u, a.Store.ServerID()))
}

func (a *API) handleUser(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, r.PathValue("userId"))
	if !ok {
		return
	}
	writeJSON(w, 200, buildUserDTO(u, a.Store.ServerID()))
}

// Views -------------------------------------------------------------------

func (a *API) handleViews(w http.ResponseWriter, r *http.Request) {
	a.viewsResponse(w, r, r.PathValue("userId"))
}

func (a *API) handleUserViews(w http.ResponseWriter, r *http.Request) {
	a.viewsResponse(w, r, r.URL.Query().Get("userId"))
}

func (a *API) viewsResponse(w http.ResponseWriter, r *http.Request, pathUser string) {
	u, ok := a.resolveUser(w, r, pathUser)
	if !ok {
		return
	}
	views := a.Catalog.Views()
	items := make([]itemDTO, 0, len(views))
	for _, v := range views {
		if !canSeeLibrary(u, v.Library.ID) {
			continue
		}
		items = append(items, buildViewDTO(v.Library, v.ItemCount, a.Store.ServerID(), u.ID))
	}
	writeJSON(w, 200, queryResult{Items: items, TotalRecordCount: len(items), StartIndex: 0})
}

// Items -------------------------------------------------------------------

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func boolParam(r *http.Request, name string) (bool, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return false, false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, false
	}
	return b, true
}

func (a *API) parseItemsQuery(r *http.Request) (catalog.Query, []string) {
	q := catalog.Query{
		ParentID: r.URL.Query().Get("parentId"),
		Search:   r.URL.Query().Get("searchTerm"),
		SortBy:   r.URL.Query().Get("sortBy"),
		Desc:     strings.EqualFold(r.URL.Query().Get("sortOrder"), "Descending"),
		Start:    hls.ParseInt(r.URL.Query().Get("startIndex"), 0),
		Limit:    hls.ParseInt(r.URL.Query().Get("limit"), 100),
		IDs:      splitList(r.URL.Query().Get("ids")),
	}
	q.Types = splitList(r.URL.Query().Get("includeItemTypes"))
	q.ExcludeTypes = splitList(r.URL.Query().Get("excludeItemTypes"))
	if v := strings.Split(q.SortBy, ","); len(v) > 0 {
		q.SortBy = strings.TrimSpace(v[0])
	}
	q.Recursive, _ = boolParam(r, "recursive")
	if f, ok := boolParam(r, "isFolder"); ok {
		q.IsFolder = &f
	}
	q.Genre = r.URL.Query().Get("genre")
	q.Year = hls.ParseInt(r.URL.Query().Get("year"), 0)
	if q.Search != "" {
		q.Recursive = true
		if len(q.Types) == 0 {
			q.Types = []string{model.TypeMovie, model.TypeSeries, model.TypeEpisode}
		}
	}
	filters := splitList(r.URL.Query().Get("filters"))
	return q, filters
}

func (a *API) handleItems(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, r.PathValue("userId"))
	if !ok {
		return
	}
	q, filters := a.parseItemsQuery(r)
	if len(q.Types) == 0 && q.ParentID == "" {
		q.Types = []string{model.TypeMovie, model.TypeSeries, model.TypeEpisode}
	}
	playedIDs := a.Store.PlayedIDs(u.ID)
	favIDs := a.Store.FavoriteIDs(u.ID)
	resume := a.Store.Resumable(u.ID)
	keep := func(it *model.Item) bool {
		if !canSeeItem(u, it) {
			return false
		}
		for _, f := range filters {
			switch strings.ToLower(f) {
			case "isplayed":
				if !playedIDs[it.ID] {
					return false
				}
			case "isunplayed":
				if playedIDs[it.ID] {
					return false
				}
			case "isfavorite":
				if !favIDs[it.ID] {
					return false
				}
			case "isresumable":
				if _, ok := resume[it.ID]; !ok {
					return false
				}
			}
		}
		return true
	}
	items, total := a.Catalog.Query(q, keep)
	out := make([]itemDTO, 0, len(items))
	for _, it := range items {
		out = append(out, a.buildItem(it, u.ID, false, ""))
	}
	writeJSON(w, 200, queryResult{Items: out, TotalRecordCount: total, StartIndex: q.Start})
}

func (a *API) handleResume(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, r.PathValue("userId"))
	if !ok {
		return
	}
	resume := a.Store.Resumable(u.ID)
	list := make([]*model.Item, 0, len(resume))
	for id, p := range resume {
		it, ok := a.Catalog.Get(id)
		if !ok || p.PositionTicks == 0 {
			continue
		}
		if !canSeeItem(u, it) {
			continue
		}
		if it.Type != model.TypeMovie && it.Type != model.TypeEpisode {
			continue
		}
		list = append(list, it)
	}
	sort.Slice(list, func(i, j int) bool {
		return resume[list[i].ID].LastPlayed.After(resume[list[j].ID].LastPlayed)
	})
	start := hls.ParseInt(r.URL.Query().Get("startIndex"), 0)
	limit := hls.ParseInt(r.URL.Query().Get("limit"), 20)
	total := len(list)
	if start > total {
		start = total
	}
	end := start + limit
	if end > total || limit <= 0 {
		end = total
	}
	out := make([]itemDTO, 0, end-start)
	for _, it := range list[start:end] {
		out = append(out, a.buildItem(it, u.ID, false, ""))
	}
	writeJSON(w, 200, queryResult{Items: out, TotalRecordCount: total, StartIndex: start})
}

func (a *API) handleLatest(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, r.PathValue("userId"))
	if !ok {
		return
	}
	parent := r.URL.Query().Get("parentId")
	limit := hls.ParseInt(r.URL.Query().Get("limit"), 20)
	var items []*model.Item
	if parent != "" {
		if p, ok := a.Catalog.Get(parent); ok && !canSeeItem(u, p) {
			writeJSON(w, 200, []itemDTO{})
			return
		}
		items = a.Catalog.Latest(parent, limit)
	} else {
		for _, v := range a.Catalog.Views() {
			if !canSeeLibrary(u, v.Library.ID) {
				continue
			}
			items = append(items, a.Catalog.Latest(v.Library.ID, limit)...)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].AddedAt.After(items[j].AddedAt) })
		if len(items) > limit {
			items = items[:limit]
		}
	}
	out := make([]itemDTO, 0, len(items))
	for _, it := range items {
		if !canSeeItem(u, it) {
			continue
		}
		out = append(out, a.buildItem(it, u.ID, false, ""))
	}
	writeJSON(w, 200, out)
}

func (a *API) handleItem(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("itemId")
	it, ok := a.Catalog.Get(id)
	if !ok || !canSeeItem(u, it) {
		writeError(w, 404, "Item does not exist")
		return
	}
	writeJSON(w, 200, a.buildItem(it, u.ID, true, a.tokenFromRequest(r)))
}

func (a *API) handleSimilar(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	it, ok := a.Catalog.Get(r.PathValue("itemId"))
	if !ok || !canSeeItem(u, it) {
		writeError(w, 404, "Item does not exist")
		return
	}
	all, _ := a.Catalog.Query(catalog.Query{ParentID: it.LibraryID, Recursive: true, Limit: 500}, nil)
	genre := map[string]bool{}
	for _, g := range it.Genres {
		genre[g] = true
	}
	type scored struct {
		it    *model.Item
		score int
	}
	var list []scored
	for _, cand := range all {
		if cand.ID == it.ID || (cand.Type != model.TypeMovie && cand.Type != model.TypeSeries) {
			continue
		}
		score := 0
		for _, g := range cand.Genres {
			if genre[g] {
				score++
			}
		}
		if score > 0 {
			list = append(list, scored{cand, score})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	if len(list) > 12 {
		list = list[:12]
	}
	out := make([]itemDTO, 0, len(list))
	for _, s := range list {
		out = append(out, a.buildItem(s.it, u.ID, false, ""))
	}
	writeJSON(w, 200, queryResult{Items: out, TotalRecordCount: len(out), StartIndex: 0})
}

// Shows -------------------------------------------------------------------

func (a *API) handleSeasons(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	seriesID := r.PathValue("seriesId")
	if series, ok := a.Catalog.Get(seriesID); ok && !canSeeItem(u, series) {
		writeJSON(w, 200, queryResult{Items: []itemDTO{}, TotalRecordCount: 0, StartIndex: 0})
		return
	}
	children := a.Catalog.Children(seriesID)
	out := make([]itemDTO, 0, len(children))
	for _, it := range children {
		if it.Type == model.TypeSeason {
			out = append(out, a.buildItem(it, u.ID, false, ""))
		}
	}
	writeJSON(w, 200, queryResult{Items: out, TotalRecordCount: len(out), StartIndex: 0})
}

func (a *API) handleEpisodes(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	seriesID := r.PathValue("seriesId")
	if series, ok := a.Catalog.Get(seriesID); ok && !canSeeItem(u, series) {
		writeJSON(w, 200, queryResult{Items: []itemDTO{}, TotalRecordCount: 0, StartIndex: 0})
		return
	}
	var eps []*model.Item
	if seasonID := r.URL.Query().Get("seasonId"); seasonID != "" {
		eps = a.Catalog.Children(seasonID)
	} else {
		eps, _ = a.Catalog.Query(catalog.Query{ParentID: seriesID, Recursive: true, Types: []string{model.TypeEpisode}, Limit: 1000}, nil)
	}
	sort.SliceStable(eps, func(i, j int) bool {
		if eps[i].SeasonNumber != eps[j].SeasonNumber {
			return eps[i].SeasonNumber < eps[j].SeasonNumber
		}
		return eps[i].EpisodeNumber < eps[j].EpisodeNumber
	})
	out := make([]itemDTO, 0, len(eps))
	for _, it := range eps {
		if it.Type == model.TypeEpisode {
			out = append(out, a.buildItem(it, u.ID, false, ""))
		}
	}
	writeJSON(w, 200, queryResult{Items: out, TotalRecordCount: len(out), StartIndex: 0})
}

func (a *API) handleNextUp(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	seriesID := r.URL.Query().Get("seriesId")
	limit := hls.ParseInt(r.URL.Query().Get("limit"), 24)
	var seriesList []*model.Item
	if seriesID != "" {
		if it, ok := a.Catalog.Get(seriesID); ok && it.Type == model.TypeSeries && canSeeItem(u, it) {
			seriesList = append(seriesList, it)
		}
	} else {
		for _, v := range a.Catalog.Views() {
			if !canSeeLibrary(u, v.Library.ID) {
				continue
			}
			for _, it := range a.Catalog.Children(v.Library.ID) {
				if it.Type == model.TypeSeries {
					seriesList = append(seriesList, it)
				}
			}
		}
	}
	var out []*model.Item
	for _, series := range seriesList {
		eps, _ := a.Catalog.Query(catalog.Query{ParentID: series.ID, Recursive: true, Types: []string{model.TypeEpisode}, Limit: 500}, nil)
		sort.SliceStable(eps, func(i, j int) bool {
			if eps[i].SeasonNumber != eps[j].SeasonNumber {
				return eps[i].SeasonNumber < eps[j].SeasonNumber
			}
			return eps[i].EpisodeNumber < eps[j].EpisodeNumber
		})
		last := -1
		for i, ep := range eps {
			if a.Store.GetUserData(u.ID, ep.ID).Played {
				last = i
			}
		}
		idx := last + 1
		if last < 0 {
			idx = 0
		}
		if idx < len(eps) {
			out = append(out, eps[idx])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	items := make([]itemDTO, 0, len(out))
	for _, it := range out {
		items = append(items, a.buildItem(it, u.ID, false, ""))
	}
	writeJSON(w, 200, queryResult{Items: items, TotalRecordCount: len(items), StartIndex: 0})
}

func (a *API) handleSearchHints(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	q := catalog.Query{Search: r.URL.Query().Get("searchTerm"), Recursive: true,
		Types: []string{model.TypeMovie, model.TypeSeries, model.TypeEpisode}, Limit: 30}
	if q.Search == "" {
		writeJSON(w, 200, map[string]any{"SearchHints": []any{}, "TotalRecordCount": 0})
		return
	}
	items, total := a.Catalog.Query(q, func(it *model.Item) bool { return canSeeItem(u, it) })
	hints := make([]map[string]any, 0, len(items))
	for _, it := range items {
		h := map[string]any{
			"ItemId":         it.ID,
			"Id":             it.ID,
			"Name":           it.Name,
			"Type":           it.Type,
			"MediaType":      mediaTypeOf(it),
			"ProductionYear": it.Year,
			"RunTimeTicks":   it.RuntimeTicks,
			"SeriesName":     it.SeriesName,
			"ImageTags":      map[string]string{"Primary": imageTag(it, "Primary")},
		}
		if it.Type == model.TypeEpisode {
			h["IndexNumber"] = it.EpisodeNumber
			h["ParentIndexNumber"] = it.SeasonNumber
		}
		hints = append(hints, h)
	}
	writeJSON(w, 200, map[string]any{"SearchHints": hints, "TotalRecordCount": total})
}

func mediaTypeOf(it *model.Item) string {
	if it.Type == model.TypeMovie || it.Type == model.TypeEpisode {
		return "Video"
	}
	return ""
}

// User item flags ---------------------------------------------------------

func (a *API) handlePlayed(played bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.resolveUser(w, r, r.PathValue("userId"))
		if !ok {
			return
		}
		it, ok := a.Catalog.Get(r.PathValue("itemId"))
		if !ok || !canSeeItem(u, it) {
			writeError(w, 404, "Item does not exist")
			return
		}
		for _, id := range a.itemAndEpisodes(it) {
			a.Store.SetPlayed(u.ID, id, played)
		}
		writeJSON(w, 200, a.buildUserData(it, u.ID))
	}
}

func (a *API) itemAndEpisodes(it *model.Item) []string {
	out := []string{it.ID}
	if it.Type == model.TypeSeries || it.Type == model.TypeSeason {
		var eps []*model.Item
		if it.Type == model.TypeSeason {
			eps = a.Catalog.Children(it.ID)
		} else {
			eps, _ = a.Catalog.Query(catalog.Query{ParentID: it.ID, Recursive: true, Types: []string{model.TypeEpisode}, Limit: 1000}, nil)
		}
		for _, e := range eps {
			if e.Type == model.TypeEpisode {
				out = append(out, e.ID)
			}
		}
	}
	return out
}

func (a *API) handleFavorite(fav bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.resolveUser(w, r, r.PathValue("userId"))
		if !ok {
			return
		}
		it, ok := a.Catalog.Get(r.PathValue("itemId"))
		if !ok || !canSeeItem(u, it) {
			writeError(w, 404, "Item does not exist")
			return
		}
		a.Store.SetFavorite(u.ID, it.ID, fav)
		writeJSON(w, 200, a.buildUserData(it, u.ID))
	}
}

// Playback progress -------------------------------------------------------

type playbackReport struct {
	ItemId                 string `json:"ItemId"`
	MediaSourceId          string `json:"MediaSourceId"`
	PlaySessionId          string `json:"PlaySessionId"`
	PositionTicks          int64  `json:"PositionTicks"`
	IsPaused               bool   `json:"IsPaused"`
	IsMuted                bool   `json:"IsMuted"`
	PlayMethod             string `json:"PlayMethod"`
	RepeatMode             string `json:"RepeatMode"`
	VolumeLevel            int    `json:"VolumeLevel"`
	PlaybackStartTimeTicks int64  `json:"PlaybackStartTimeTicks"`
}

func (a *API) handlePlaying(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var rep playbackReport
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&rep); err != nil {
		writeError(w, 400, "Invalid request format")
		return
	}
	a.applyProgress(u.ID, rep)
	w.WriteHeader(204)
}

func (a *API) handleStopped(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var rep playbackReport
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&rep); err != nil {
		writeError(w, 400, "Invalid request format")
		return
	}
	a.applyProgress(u.ID, rep)
	if rep.PlaySessionId != "" {
		a.HLS.Stop(rep.PlaySessionId)
	}
	w.WriteHeader(204)
}

func (a *API) applyProgress(userID string, rep playbackReport) {
	if rep.ItemId == "" {
		return
	}
	it, ok := a.Catalog.Get(rep.ItemId)
	if !ok {
		return
	}
	dur := int64(0)
	if it.Media != nil {
		dur = it.Media.DurationTicks
	}
	if dur == 0 {
		dur = it.RuntimeTicks
	}
	played := dur > 0 && rep.PositionTicks > 0 && rep.PositionTicks >= dur*9/10
	a.Store.SetProgress(userID, it.ID, rep.PositionTicks, played)
}

func (a *API) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(204)
}

func (a *API) handleDisplayPreferences(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"Id":                 r.PathValue("id"),
		"SortBy":             "SortName",
		"RememberIndexing":   false,
		"PrimaryImageHeight": 250,
		"PrimaryImageWidth":  250,
		"ScrollDirection":    "Horizontal",
		"ShowBackdrop":       true,
		"RememberSorting":    true,
		"ShowSidebar":        true,
		"CustomPrefs":        map[string]string{},
	})
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// UserFromRequest resolves an access token from a request. Exposed for the
// bundled web client's admin endpoints.
func (a *API) UserFromRequest(r *http.Request) (*model.User, bool) {
	return a.userFromRequest(r)
}
