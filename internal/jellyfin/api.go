// Package jellyfin implements the subset of the Jellyfin HTTP API that Nuvio
// supports. The web client bundled with the server uses the same endpoints as
// native Jellyfin clients: there is no private side channel for playback,
// browsing or progress reporting.
package jellyfin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"runtime"
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
const Version = "1.1.1"

// ApiVersion is the Jellyfin server version we advertise. Native clients gate
// features on this string.
const ApiVersion = "12.4.0"

// API holds the shared server dependencies.
type API struct {
	Store    *store.Store
	Catalog  *catalog.Catalog
	HLS      *hls.Manager
	DataDir  string
	ImageDir string
	DemoFile string
	Started  time.Time

	toolsMu     sync.RWMutex
	ffmpeg      string
	ffprobe     string
	encoder     string
	vaapiDevice string
	qsvDevice   string
	imageMu     sync.Mutex
	imageHTTP   *http.Client
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

// SetHardware records the detected hardware render nodes. An empty string
// means that backend is unavailable.
func (a *API) SetHardware(vaapiDevice, qsvDevice string) {
	a.toolsMu.Lock()
	a.vaapiDevice, a.qsvDevice = vaapiDevice, qsvDevice
	a.toolsMu.Unlock()
}

// HardwareDevice returns the render node for the requested backend.
func (a *API) HardwareDevice(mode string) string {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	switch mode {
	case "vaapi":
		return a.vaapiDevice
	case "qsv":
		return a.qsvDevice
	}
	return ""
}

// HardwareMode returns the preferred detected backend (VAAPI first).
func (a *API) HardwareMode() string {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	if a.vaapiDevice != "" {
		return "vaapi"
	}
	if a.qsvDevice != "" {
		return "qsv"
	}
	return ""
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
	mux.HandleFunc("GET /System/Configuration", a.handleSystemConfiguration)
	mux.HandleFunc("GET /Branding/Configuration", a.handleBranding)
	mux.HandleFunc("POST /Users/AuthenticateByName", a.handleAuthenticate)
	mux.HandleFunc("GET /Users/Public", a.handlePublicUsers)
	mux.HandleFunc("GET /Users/Me", a.handleMe)
	mux.HandleFunc("GET /Users/{userId}", a.handleUser)
	// Legacy 10.x user scoped aliases. New clients use the 12.4 routes below,
	// but older Jellyfin apps still call these paths.
	mux.HandleFunc("GET /Users/{userId}/Views", a.handleViews)
	mux.HandleFunc("GET /Users/{userId}/Items", a.handleItems)
	mux.HandleFunc("GET /Users/{userId}/Items/Resume", a.handleResume)
	mux.HandleFunc("GET /Users/{userId}/Items/Latest", a.handleLatest)
	mux.HandleFunc("GET /Users/{userId}/Items/{itemId}", a.handleItem)
	mux.HandleFunc("POST /Users/{userId}/PlayedItems/{itemId}", a.handlePlayed(true))
	mux.HandleFunc("DELETE /Users/{userId}/PlayedItems/{itemId}", a.handlePlayed(false))
	mux.HandleFunc("POST /Users/{userId}/FavoriteItems/{itemId}", a.handleFavorite(true))
	mux.HandleFunc("DELETE /Users/{userId}/FavoriteItems/{itemId}", a.handleFavorite(false))
	mux.HandleFunc("POST /Users/{userId}/PlayingItems/{itemId}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("DELETE /Users/{userId}/PlayingItems/{itemId}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /UserViews", a.handleViews)
	mux.HandleFunc("GET /Items", a.handleItems)
	mux.HandleFunc("GET /Items/Latest", a.handleLatest)
	mux.HandleFunc("GET /Items/{itemId}", a.handleItem)
	mux.HandleFunc("GET /UserItems/Resume", a.handleResume)
	mux.HandleFunc("GET /UserItems/{itemId}/UserData", a.handleUserData)
	mux.HandleFunc("POST /UserItems/{itemId}/UserData", a.handleUserData)
	mux.HandleFunc("GET /Items/Suggestions", a.handleSuggestions)
	mux.HandleFunc("GET /Items/{itemId}/Similar", a.handleSimilar)
	mux.HandleFunc("GET /Shows/{seriesId}/Seasons", a.handleSeasons)
	mux.HandleFunc("GET /Shows/{seriesId}/Episodes", a.handleEpisodes)
	mux.HandleFunc("GET /Shows/NextUp", a.handleNextUp)
	mux.HandleFunc("GET /Search/Hints", a.handleSearchHints)
	mux.HandleFunc("POST /UserPlayedItems/{itemId}", a.handlePlayed(true))
	mux.HandleFunc("DELETE /UserPlayedItems/{itemId}", a.handlePlayed(false))
	mux.HandleFunc("POST /UserFavoriteItems/{itemId}", a.handleFavorite(true))
	mux.HandleFunc("DELETE /UserFavoriteItems/{itemId}", a.handleFavorite(false))
	mux.HandleFunc("POST /Sessions/Playing", a.handlePlaying)
	mux.HandleFunc("POST /Sessions/Playing/Progress", a.handlePlaying)
	mux.HandleFunc("POST /Sessions/Playing/Stopped", a.handleStopped)
	mux.HandleFunc("POST /Sessions/Capabilities/Full", a.handleCapabilities)
	mux.HandleFunc("POST /Sessions/Playing/Ping", a.handlePing)
	mux.HandleFunc("GET /Items/Root", a.handleRoot)
	mux.HandleFunc("GET /Genres", a.handleGenres)
	mux.HandleFunc("GET /Persons", a.handlePersons)
	mux.HandleFunc("GET /DisplayPreferences/{id}", a.handleDisplayPreferences)
	mux.HandleFunc("POST /DisplayPreferences/{id}", a.handleDisplayPreferences)

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

// normalizeID accepts both Jellyfin's hyphenated GUIDs and Nuvio's compact
// hex ids. Clients serialize UUIDs with dashes, while storage uses md5 hex.
func normalizeID(id string) string {
	return strings.ReplaceAll(strings.TrimSpace(id), "-", "")
}

func normalizeIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = normalizeID(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func (a *API) tokenFromRequest(r *http.Request) string {
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		if v := r.Header.Get(name); v != "" {
			return v
		}
	}
	q := queryValues(r)
	for _, name := range []string{"api_key", "apikey", "x-emby-token", "accesstoken"} {
		if v := q.Get(name); v != "" {
			return v
		}
	}
	// Native clients authenticate with an Authorization-style header that
	// carries "Token=..." (MediaBrowser scheme), not only a bearer token.
	for _, name := range []string{"X-Emby-Authorization", "Authorization"} {
		h := r.Header.Get(name)
		if h == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			return strings.TrimSpace(h[7:])
		}
		if v := mediaBrowserParam(h, "Token"); v != "" {
			return v
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
	pathID = normalizeID(pathID)
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

// queryParams is a case-insensitive view over the request query. Jellyfin's
// ASP.NET binder matches query names case-insensitively, and third-party
// clients send ParentId / IncludeItemTypes with different casing.
type queryParams url.Values

func (q queryParams) Get(name string) string {
	for k, vs := range q {
		if len(vs) > 0 && strings.EqualFold(k, name) {
			return vs[0]
		}
	}
	return ""
}

func queryValues(r *http.Request) queryParams {
	return queryParams(r.URL.Query())
}

// requestUserID resolves the target user for both the 12.x routes (userId
// query) and the legacy user scoped routes (/Users/{userId}/...).
func requestUserID(r *http.Request, pathID string) string {
	if id := normalizeID(pathID); id != "" {
		return id
	}
	return queryValues(r).Get("userId")
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

// rootItemID is a stable, UUID-shaped id for the virtual root item.
const rootItemID = "00000000000000000000000000000001"

func (a *API) rootDTO() itemDTO {
	it := rootViewItem(a.Store.Settings().ServerName)
	return itemDTO{
		Name: it.Name, ServerId: a.Store.ServerID(), Id: rootItemID, SortName: it.SortName,
		Type: "CollectionFolder", IsFolder: true,
		ImageTags:         map[string]string{"Primary": imageTag(it, "Primary")},
		BackdropImageTags: []string{}, ParentBackdropImageTags: []string{},
		PlayAccess: "Full", LocationType: "Virtual",
		UserData: &userItemData{ItemId: rootItemID, Key: rootItemID},
	}
}

func (a *API) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.rootDTO())
}

func (a *API) handlePublicInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"LocalAddress":           "",
		"ServerName":             a.Store.Settings().ServerName,
		"Version":                ApiVersion,
		"ProductName":            "Jellyfin Server",
		"OperatingSystem":        systemOS(),
		"Id":                     a.Store.ServerID(),
		"StartupWizardCompleted": a.Store.SetupComplete(),
	})
}

func (a *API) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireUser(w, r); !ok {
		return
	}
	s := a.Store.Settings()
	writeJSON(w, 200, map[string]any{
		"ServerName":                 s.ServerName,
		"Version":                    ApiVersion,
		"ProductName":                "Jellyfin Server",
		"Id":                         a.Store.ServerID(),
		"LocalAddress":               "",
		"OperatingSystem":            systemOS(),
		"OperatingSystemDisplayName": systemOS(),
		"SystemArchitecture":         systemArchitecture(),
		"EncoderLocation":            "System",
		"PackageName":                "",
		"HasUpdateAvailable":         false,
		"StartupWizardCompleted":     a.Store.SetupComplete(),
		"HasPendingRestart":          false,
		"IsShuttingDown":             false,
		"SupportsLibraryMonitor":     false,
		"WebSocketPortNumber":        0,
		"CanSelfRestart":             false,
		"CanLaunchWebBrowser":        false,
		"ProgramDataPath":            a.DataDir,
		"WebPath":                    "/",
		"ItemsByNamePath":            "",
		"CachePath":                  a.DataDir,
		"LogPath":                    a.DataDir,
		"InternalMetadataPath":       a.DataDir,
		"TranscodingTempPath":        a.DataDir,
		"CompletedInstallations":     []any{},
		"CastReceiverApplications":   []any{},
	})
}

func systemOS() string {
	switch runtime.GOOS {
	case "linux":
		return "Linux"
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	default:
		return runtime.GOOS
	}
}

func systemArchitecture() string {
	switch runtime.GOARCH {
	case "amd64":
		return "X64"
	case "arm64":
		return "Arm64"
	case "386":
		return "X86"
	case "arm":
		return "Arm"
	default:
		return runtime.GOARCH
	}
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
		out = append(out, buildUserDTO(&users[i], a.Store.ServerID(), a.Store.Settings().ServerName))
	}
	writeJSON(w, 200, out)
}

func mediaBrowserParam(header, name string) string {
	idx := strings.Index(strings.ToLower(header), strings.ToLower(name)+"=")
	if idx < 0 {
		return ""
	}
	s := strings.TrimSpace(header[idx+len(name)+1:])
	s = strings.TrimPrefix(s, `"`)
	if j := strings.IndexAny(s, `",`); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(strings.Trim(s, `"`))
}

// ClientAddress returns the caller's IP, honoring the headers set by a reverse
// proxy in front of the server.
func ClientAddress(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if v := strings.TrimSpace(xff); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// DecodeClientText reverses the percent-encoding some clients apply to
// user-visible strings such as their device name ("真我GT6" arrives encoded).
func DecodeClientText(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	if v, err := url.PathUnescape(s); err == nil && v != "" {
		return v
	}
	return s
}

// DeviceLabel derives a readable device name from the user agent, for clients
// that do not send X-Emby-Device-Name. The format matches the web UI's
// "Linux · Chrome" style.
func DeviceLabel(r *http.Request) string {
	ua := r.UserAgent()
	osName := ""
	switch {
	case strings.Contains(ua, "Android"):
		osName = "Android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"):
		osName = "iOS"
	case strings.Contains(ua, "Windows"):
		osName = "Windows"
	case strings.Contains(ua, "CrOS"):
		osName = "ChromeOS"
	case strings.Contains(ua, "Mac OS X"), strings.Contains(ua, "Macintosh"):
		osName = "macOS"
	case strings.Contains(ua, "Linux"):
		osName = "Linux"
	}
	browser := ""
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	switch {
	case osName != "" && browser != "":
		return osName + " · " + browser
	case browser != "":
		return browser
	case osName != "":
		return osName
	}
	return "Browser"
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
	now := time.Now().UTC()
	auth := r.Header.Get("X-Emby-Authorization")
	if auth == "" {
		auth = r.Header.Get("Authorization")
	}
	client := r.Header.Get("X-Emby-Client")
	if client == "" {
		client = mediaBrowserParam(auth, "Client")
	}
	deviceName := r.Header.Get("X-Emby-Device-Name")
	if deviceName == "" {
		deviceName = mediaBrowserParam(auth, "Device")
	}
	deviceID := r.Header.Get("X-Emby-Device-Id")
	if deviceID == "" {
		deviceID = mediaBrowserParam(auth, "DeviceId")
	}
	clientVersion := r.Header.Get("X-Emby-Client-Version")
	if clientVersion == "" {
		clientVersion = mediaBrowserParam(auth, "Version")
	}
	if deviceName == "" {
		deviceName = DeviceLabel(r)
	}
	client = DecodeClientText(client)
	deviceName = DecodeClientText(deviceName)
	token, err := a.Store.IssueToken(u.ID, store.SessionMeta{
		DeviceID:   deviceID,
		DeviceName: deviceName,
		Client:     client,
		Version:    clientVersion,
		Address:    ClientAddress(r),
	})
	if err != nil {
		writeError(w, 500, "Could not create a session")
		return
	}
	log.Printf("Login: user=%q client=%q device=%q version=%q",
		u.Name, client, deviceName, clientVersion,
	)
	writeJSON(w, 200, map[string]any{
		"User":        buildUserDTO(u, a.Store.ServerID(), a.Store.Settings().ServerName),
		"AccessToken": token,
		"ServerId":    a.Store.ServerID(),
		"SessionInfo": map[string]any{
			"Id":                    randomHex(12),
			"UserId":                u.ID,
			"UserName":              u.Name,
			"DeviceId":              deviceID,
			"DeviceName":            deviceName,
			"DeviceType":            "",
			"Client":                client,
			"ApplicationVersion":    clientVersion,
			"ServerId":              a.Store.ServerID(),
			"RemoteEndPoint":        r.RemoteAddr,
			"LastActivityDate":      now.Format("2006-01-02T15:04:05.0000000Z"),
			"LastPlaybackCheckIn":   now.Format("2006-01-02T15:04:05.0000000Z"),
			"IsActive":              true,
			"SupportsMediaControl":  true,
			"SupportsRemoteControl": false,
			"HasCustomDeviceName":   false,
			"PlayableMediaTypes":    []string{"Video"},
			"SupportedCommands":     []string{},
			"PlayState": map[string]any{
				"PositionTicks":       0,
				"CanSeek":             true,
				"IsPaused":            false,
				"IsMuted":             false,
				"VolumeLevel":         100,
				"AudioStreamIndex":    1,
				"SubtitleStreamIndex": -1,
				"MediaSourceId":       "",
				"PlayMethod":          "DirectPlay",
				"RepeatMode":          "RepeatNone",
				"PlaybackOrder":       "Default",
			},
			"Capabilities": map[string]any{
				"PlayableMediaTypes":           []string{"Video"},
				"SupportedCommands":            []string{},
				"SupportsMediaControl":         true,
				"SupportsPersistentIdentifier": true,
			},
		},
	})
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, buildUserDTO(u, a.Store.ServerID(), a.Store.Settings().ServerName))
}

func (a *API) handleUser(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, r.PathValue("userId"))
	if !ok {
		return
	}
	writeJSON(w, 200, buildUserDTO(u, a.Store.ServerID(), a.Store.Settings().ServerName))
}

// Views -------------------------------------------------------------------

func (a *API) handleViews(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
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

func intList(s string) []int {
	out := []int{}
	for _, p := range splitList(s) {
		if v, err := strconv.Atoi(p); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func boolParam(r *http.Request, name string) (bool, bool) {
	v := queryValues(r).Get(name)
	if v == "" {
		return false, false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, false
	}
	return b, true
}

type itemQueryOptions struct {
	filters    []string
	isFavorite *bool
	isPlayed   *bool
	mediaTypes []string
	excludeIDs map[string]bool
	genreIDs   []string
	personIDs  []string
	person     string
	userData   bool
	images     bool
	totalCount bool
}

func (a *API) parseItemsQuery(r *http.Request) (catalog.Query, itemQueryOptions) {
	v := queryValues(r)
	q := catalog.Query{
		ParentID: normalizeID(v.Get("parentId")),
		Search:   v.Get("searchTerm"),
		Start:    hls.ParseInt(v.Get("startIndex"), 0),
		Limit:    hls.ParseInt(v.Get("limit"), 0),
		IDs:      normalizeIDs(splitList(v.Get("ids"))),
	}
	if sortBy := splitList(v.Get("sortBy")); len(sortBy) > 0 {
		q.SortBy = sortBy[0]
	}
	if sortOrder := splitList(v.Get("sortOrder")); len(sortOrder) > 0 {
		q.Desc = strings.EqualFold(sortOrder[0], "Descending")
	}
	q.Types = splitList(v.Get("includeItemTypes"))
	q.ExcludeTypes = splitList(v.Get("excludeItemTypes"))
	q.Genres = splitList(v.Get("genres"))
	if len(q.Genres) == 0 {
		q.Genres = splitList(v.Get("genre"))
	}
	q.Years = intList(v.Get("years"))
	if len(q.Years) == 0 {
		q.Years = intList(v.Get("year"))
	}
	q.Recursive, _ = boolParam(r, "recursive")
	if f, ok := boolParam(r, "isFolder"); ok {
		q.IsFolder = &f
	}
	if q.Search != "" {
		q.Recursive = true
		if len(q.Types) == 0 {
			q.Types = []string{model.TypeMovie, model.TypeSeries, model.TypeEpisode}
		}
	}
	opts := itemQueryOptions{
		filters:    splitList(v.Get("filters")),
		mediaTypes: splitList(v.Get("mediaTypes")),
		genreIDs:   normalizeIDs(splitList(v.Get("genreIds"))),
		personIDs:  normalizeIDs(splitList(v.Get("personIds"))),
		person:     v.Get("person"),
		userData:   true,
		images:     true,
		totalCount: true,
	}
	if b, ok := boolParam(r, "isFavorite"); ok {
		opts.isFavorite = &b
	}
	if b, ok := boolParam(r, "isPlayed"); ok {
		opts.isPlayed = &b
	}
	if b, ok := boolParam(r, "enableUserData"); ok {
		opts.userData = b
	}
	if b, ok := boolParam(r, "enableImages"); ok {
		opts.images = b
	}
	if b, ok := boolParam(r, "enableTotalRecordCount"); ok {
		opts.totalCount = b
	}
	if ids := normalizeIDs(splitList(v.Get("excludeItemIds"))); len(ids) > 0 {
		opts.excludeIDs = make(map[string]bool, len(ids))
		for _, id := range ids {
			opts.excludeIDs[id] = true
		}
	}
	return q, opts
}

func matchesGenreIDs(it *model.Item, ids []string) bool {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[strings.ToLower(id)] = true
	}
	for _, g := range it.Genres {
		if want[strings.ToLower(genreID(g))] {
			return true
		}
	}
	return false
}

func matchesPersonIDs(it *model.Item, ids []string) bool {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[strings.ToLower(id)] = true
	}
	for _, p := range it.People {
		if want[strings.ToLower(personID(p.Name))] {
			return true
		}
	}
	return false
}

func matchesPersonName(it *model.Item, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range it.People {
		if strings.Contains(strings.ToLower(p.Name), name) {
			return true
		}
	}
	return false
}

func mediaTypeMatches(it *model.Item, mediaTypes []string) bool {
	for _, mt := range mediaTypes {
		switch strings.ToLower(mt) {
		case "video":
			if it.Type == model.TypeMovie || it.Type == model.TypeEpisode {
				return true
			}
		case "":
			return true
		}
	}
	return false
}

func (a *API) handleItems(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	q, opts := a.parseItemsQuery(r)
	if len(q.Types) == 0 && q.ParentID == "" {
		q.Types = []string{model.TypeMovie, model.TypeSeries, model.TypeSeason, model.TypeEpisode}
	}
	playedIDs := a.Store.PlayedIDs(u.ID)
	favIDs := a.Store.FavoriteIDs(u.ID)
	resume := a.Store.Resumable(u.ID)
	keep := func(it *model.Item) bool {
		if !canSeeItem(u, it) {
			return false
		}
		if opts.excludeIDs[it.ID] {
			return false
		}
		if opts.isFavorite != nil && favIDs[it.ID] != *opts.isFavorite {
			return false
		}
		if opts.isPlayed != nil && playedIDs[it.ID] != *opts.isPlayed {
			return false
		}
		if len(opts.mediaTypes) > 0 && !mediaTypeMatches(it, opts.mediaTypes) {
			return false
		}
		if len(opts.genreIDs) > 0 && !matchesGenreIDs(it, opts.genreIDs) {
			return false
		}
		if len(opts.personIDs) > 0 && !matchesPersonIDs(it, opts.personIDs) {
			return false
		}
		if opts.person != "" && !matchesPersonName(it, opts.person) {
			return false
		}
		for _, f := range opts.filters {
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
		dto := a.buildItem(it, u.ID, false, "")
		if !opts.userData {
			dto.UserData = nil
		}
		if !opts.images {
			dto.ImageTags = map[string]string{}
			dto.BackdropImageTags = []string{}
			dto.ParentBackdropImageTags = []string{}
		}
		out = append(out, dto)
	}
	if !opts.totalCount {
		total = 0
	}
	writeJSON(w, 200, queryResult{Items: out, TotalRecordCount: total, StartIndex: q.Start})
}

func (a *API) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	if mediaTypes := splitList(queryValues(r).Get("mediaType")); len(mediaTypes) > 0 && !containsFold(mediaTypes, "Video") {
		writeJSON(w, 200, queryResult{Items: []itemDTO{}, TotalRecordCount: 0, StartIndex: 0})
		return
	}
	start := hls.ParseInt(queryValues(r).Get("startIndex"), 0)
	limit := hls.ParseInt(queryValues(r).Get("limit"), 6)
	if limit <= 0 {
		limit = 6
	}
	if limit > 50 {
		limit = 50
	}
	types := splitList(queryValues(r).Get("type"))
	if len(types) == 0 {
		types = splitList(queryValues(r).Get("includeItemTypes"))
	}
	if len(types) == 0 {
		types = []string{model.TypeMovie, model.TypeSeries}
	}
	q := catalog.Query{
		Types:     types,
		Recursive: true,
		Start:     start,
		Limit:     limit,
		SortBy:    "CommunityRating",
		Desc:      true,
	}
	items, total := a.Catalog.Query(q, func(it *model.Item) bool {
		return canSeeItem(u, it)
	})
	out := make([]itemDTO, 0, len(items))
	for _, it := range items {
		out = append(out, a.buildItem(it, u.ID, false, ""))
	}
	if b, ok := boolParam(r, "enableTotalRecordCount"); !ok || !b {
		total = 0
	}
	writeJSON(w, 200, queryResult{Items: out, TotalRecordCount: total, StartIndex: start})
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

func (a *API) handleResume(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	v := queryValues(r)
	parent := normalizeID(v.Get("parentId"))
	types := splitList(v.Get("includeItemTypes"))
	excludeTypes := splitList(v.Get("excludeItemTypes"))
	mediaTypes := splitList(v.Get("mediaTypes"))
	search := strings.ToLower(v.Get("searchTerm"))
	resume := a.Store.Resumable(u.ID)
	list := make([]*model.Item, 0, len(resume))
	for id, p := range resume {
		it, ok := a.Catalog.Get(normalizeID(id))
		if !ok || p.PositionTicks == 0 || !canSeeItem(u, it) {
			continue
		}
		if it.Type != model.TypeMovie && it.Type != model.TypeEpisode {
			continue
		}
		if len(types) > 0 && !containsFold(types, it.Type) {
			continue
		}
		if containsFold(excludeTypes, it.Type) {
			continue
		}
		if len(mediaTypes) > 0 && !mediaTypeMatches(it, mediaTypes) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(it.Name), search) {
			continue
		}
		if parent != "" && it.LibraryID != parent && it.ParentID != parent && it.SeriesID != parent && it.SeasonID != parent {
			continue
		}
		list = append(list, it)
	}
	sort.Slice(list, func(i, j int) bool {
		return resume[list[i].ID].LastPlayed.After(resume[list[j].ID].LastPlayed)
	})
	start := hls.ParseInt(v.Get("startIndex"), 0)
	limit := hls.ParseInt(v.Get("limit"), 20)
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
	if b, ok := boolParam(r, "enableTotalRecordCount"); ok && !b {
		total = 0
	}
	writeJSON(w, 200, queryResult{Items: out, TotalRecordCount: total, StartIndex: start})
}

func (a *API) handleLatest(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	v := queryValues(r)
	parent := normalizeID(v.Get("parentId"))
	limit := hls.ParseInt(v.Get("limit"), 20)
	types := splitList(v.Get("includeItemTypes"))
	group := true
	if b, ok := boolParam(r, "groupItems"); ok {
		group = b
	}
	var played *bool
	if b, ok := boolParam(r, "isPlayed"); ok {
		played = &b
	}
	var candidates []*model.Item
	if parent != "" {
		if p, ok := a.Catalog.Get(parent); ok {
			if !canSeeItem(u, p) {
				writeJSON(w, 200, []itemDTO{})
				return
			}
		} else if !canSeeLibrary(u, parent) {
			writeJSON(w, 200, []itemDTO{})
			return
		}
		candidates, _ = a.Catalog.Query(catalog.Query{ParentID: parent, Recursive: true}, nil)
	} else {
		candidates = a.Catalog.All()
	}
	if len(types) == 0 {
		types = []string{model.TypeMovie, model.TypeSeries, model.TypeEpisode}
	}
	groupEpisodes := group && (len(v.Get("includeItemTypes")) == 0 || containsFold(types, model.TypeSeries))
	playedIDs := a.Store.PlayedIDs(u.ID)
	seen := map[string]bool{}
	items := make([]*model.Item, 0, len(candidates))
	for _, it := range candidates {
		if !canSeeItem(u, it) || !containsFold(types, it.Type) {
			continue
		}
		if played != nil && playedIDs[it.ID] != *played {
			continue
		}
		if groupEpisodes && it.Type == model.TypeEpisode && it.SeriesID != "" {
			series, ok := a.Catalog.Get(it.SeriesID)
			if !ok || !canSeeItem(u, series) {
				continue
			}
			it = series
		}
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AddedAt.Equal(items[j].AddedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].AddedAt.After(items[j].AddedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]itemDTO, 0, len(items))
	for _, it := range items {
		out = append(out, a.buildItem(it, u.ID, false, ""))
	}
	writeJSON(w, 200, out)
}

func (a *API) handleItem(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	id := normalizeID(r.PathValue("itemId"))
	if it, ok := a.Catalog.Get(id); ok {
		if !canSeeItem(u, it) {
			writeError(w, 404, "Item does not exist")
			return
		}
		writeJSON(w, 200, a.buildItem(it, u.ID, true, a.tokenFromRequest(r)))
		return
	}
	// Native clients address library views and the virtual root as items, both
	// for details and for artwork. Resolve them instead of returning 404.
	if id == rootItemID {
		writeJSON(w, 200, a.rootDTO())
		return
	}
	for _, v := range a.Catalog.Views() {
		if v.Library.ID != id {
			continue
		}
		if !canSeeLibrary(u, v.Library.ID) {
			writeError(w, 404, "Item does not exist")
			return
		}
		writeJSON(w, 200, buildViewDTO(v.Library, v.ItemCount, a.Store.ServerID(), u.ID))
		return
	}
	if it, ok := a.virtualItem(id); ok {
		writeJSON(w, 200, a.buildItem(it, u.ID, false, a.tokenFromRequest(r)))
		return
	}
	writeError(w, 404, "Item does not exist")
}

func (a *API) handleSimilar(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
	if !ok || !canSeeItem(u, it) {
		writeError(w, 404, "Item does not exist")
		return
	}
	limit := hls.ParseInt(queryValues(r).Get("limit"), 12)
	if limit <= 0 {
		limit = 12
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
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]itemDTO, 0, len(list))
	for _, s := range list {
		out = append(out, a.buildItem(s.it, u.ID, false, ""))
	}
	writeJSON(w, 200, queryResult{Items: out, TotalRecordCount: len(out), StartIndex: 0})
}

// Shows -------------------------------------------------------------------

func (a *API) handleSeasons(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	seriesID := normalizeID(r.PathValue("seriesId"))
	series, ok := a.Catalog.Get(seriesID)
	if !ok || !canSeeItem(u, series) {
		writeError(w, 404, "Series not found")
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
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	seriesID := normalizeID(r.PathValue("seriesId"))
	series, ok := a.Catalog.Get(seriesID)
	if !ok || !canSeeItem(u, series) {
		writeError(w, 404, "Series not found")
		return
	}
	v := queryValues(r)
	seasonID := normalizeID(v.Get("seasonId"))
	seasonNumber := hls.ParseInt(v.Get("season"), -1)
	var eps []*model.Item
	if seasonID != "" {
		eps = a.Catalog.Children(seasonID)
	} else {
		eps, _ = a.Catalog.Query(catalog.Query{ParentID: seriesID, Recursive: true, Types: []string{model.TypeEpisode}}, nil)
	}
	sortBy := strings.ToLower(v.Get("sortBy"))
	sortOrder := strings.ToLower(v.Get("sortOrder"))
	sort.SliceStable(eps, func(i, j int) bool {
		var before bool
		switch sortBy {
		case "datecreated", "datelastcontentadded", "premieredate":
			before = eps[i].AddedAt.Before(eps[j].AddedAt)
		case "sortname":
			before = strings.ToLower(eps[i].SortName) < strings.ToLower(eps[j].SortName)
		default:
			if eps[i].SeasonNumber != eps[j].SeasonNumber {
				before = eps[i].SeasonNumber < eps[j].SeasonNumber
			} else {
				before = eps[i].EpisodeNumber < eps[j].EpisodeNumber
			}
		}
		if sortOrder == "descending" {
			return !before
		}
		return before
	})
	filtered := make([]itemDTO, 0, len(eps))
	for _, it := range eps {
		if it.Type != model.TypeEpisode {
			continue
		}
		if seasonNumber >= 0 && it.SeasonNumber != seasonNumber {
			continue
		}
		filtered = append(filtered, a.buildItem(it, u.ID, false, ""))
	}
	start := hls.ParseInt(v.Get("startIndex"), 0)
	limit := hls.ParseInt(v.Get("limit"), 0)
	total := len(filtered)
	if start > total {
		start = total
	}
	end := start + limit
	if limit <= 0 || end > total {
		end = total
	}
	writeJSON(w, 200, queryResult{Items: filtered[start:end], TotalRecordCount: total, StartIndex: start})
}

func (a *API) handleNextUp(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	v := queryValues(r)
	seriesID := normalizeID(v.Get("seriesId"))
	limit := hls.ParseInt(v.Get("limit"), 24)
	start := hls.ParseInt(v.Get("startIndex"), 0)
	var seriesList []*model.Item
	if seriesID != "" {
		if it, ok := a.Catalog.Get(seriesID); ok && it.Type == model.TypeSeries && canSeeItem(u, it) {
			seriesList = append(seriesList, it)
		}
	} else {
		for _, lib := range a.Catalog.Views() {
			if !canSeeLibrary(u, lib.Library.ID) {
				continue
			}
			for _, it := range a.Catalog.Children(lib.Library.ID) {
				if it.Type == model.TypeSeries {
					seriesList = append(seriesList, it)
				}
			}
		}
	}
	var out []*model.Item
	for _, series := range seriesList {
		eps, _ := a.Catalog.Query(catalog.Query{ParentID: series.ID, Recursive: true, Types: []string{model.TypeEpisode}}, nil)
		sort.SliceStable(eps, func(i, j int) bool {
			if eps[i].SeasonNumber != eps[j].SeasonNumber {
				return eps[i].SeasonNumber < eps[j].SeasonNumber
			}
			return eps[i].EpisodeNumber < eps[j].EpisodeNumber
		})
		last := -1
		for i, ep := range eps {
			ud := a.Store.GetUserData(u.ID, ep.ID)
			if ud.Played {
				last = i
			}
			if ud.PositionTicks > 0 && !ud.Played {
				last = i - 1
				break
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
	total := len(out)
	if start > total {
		start = total
	}
	end := start + limit
	if limit <= 0 || end > total {
		end = total
	}
	items := make([]itemDTO, 0, end-start)
	for _, it := range out[start:end] {
		items = append(items, a.buildItem(it, u.ID, false, ""))
	}
	writeJSON(w, 200, queryResult{Items: items, TotalRecordCount: total, StartIndex: start})
}

func (a *API) handleSearchHints(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	v := queryValues(r)
	term := strings.TrimSpace(v.Get("searchTerm"))
	if term == "" {
		writeError(w, 400, "searchTerm is required")
		return
	}
	types := splitList(v.Get("includeItemTypes"))
	if len(types) == 0 {
		types = []string{model.TypeMovie, model.TypeSeries, model.TypeEpisode}
	}
	start := hls.ParseInt(v.Get("startIndex"), 0)
	limit := hls.ParseInt(v.Get("limit"), 30)
	q := catalog.Query{
		Search:    term,
		Recursive: true,
		Types:     types,
		Start:     start,
		Limit:     limit,
		ParentID:  normalizeID(v.Get("parentId")),
	}
	items, total := a.Catalog.Query(q, func(it *model.Item) bool { return canSeeItem(u, it) })
	hints := make([]map[string]any, 0, len(items))
	for _, it := range items {
		h := map[string]any{
			"ItemId":          it.ID,
			"Id":              it.ID,
			"Name":            it.Name,
			"Type":            it.Type,
			"MediaType":       mediaTypeOf(it),
			"ProductionYear":  it.Year,
			"RunTimeTicks":    it.RuntimeTicks,
			"SeriesName":      it.SeriesName,
			"PrimaryImageTag": imageTag(it, "Primary"),
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
		u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
		if !ok {
			return
		}
		it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
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
		u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
		if !ok {
			return
		}
		it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
		if !ok || !canSeeItem(u, it) {
			writeError(w, 404, "Item does not exist")
			return
		}
		a.Store.SetFavorite(u.ID, it.ID, fav)
		writeJSON(w, 200, a.buildUserData(it, u.ID))
	}
}

func (a *API) handleUserData(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, r.PathValue("userId")))
	if !ok {
		return
	}
	it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
	if !ok || !canSeeItem(u, it) {
		writeError(w, 404, "Item does not exist")
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Played                *bool      `json:"Played"`
			IsFavorite            *bool      `json:"IsFavorite"`
			PlaybackPositionTicks *int64     `json:"PlaybackPositionTicks"`
			PlayCount             *int       `json:"PlayCount"`
			LastPlayedDate        *time.Time `json:"LastPlayedDate"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeError(w, 400, "Invalid request format")
			return
		}
		if body.IsFavorite != nil {
			a.Store.SetFavorite(u.ID, it.ID, *body.IsFavorite)
		}
		if body.Played != nil {
			a.Store.SetPlayed(u.ID, it.ID, *body.Played)
		}
		if body.PlaybackPositionTicks != nil {
			played := body.Played != nil && *body.Played
			a.Store.SetProgress(u.ID, it.ID, *body.PlaybackPositionTicks, played)
		}
	}
	writeJSON(w, 200, a.buildUserData(it, u.ID))
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
	it, ok := a.Catalog.Get(normalizeID(rep.ItemId))
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

func (a *API) handlePing(w http.ResponseWriter, r *http.Request) {
	if queryValues(r).Get("playSessionId") == "" {
		writeError(w, 400, "playSessionId is required")
		return
	}
	w.WriteHeader(204)
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
