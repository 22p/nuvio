// Package server wires the media catalog, Jellyfin API and embedded web client
// into one HTTP server.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nuvio/internal/catalog"
	"nuvio/internal/demo"
	"nuvio/internal/hls"
	"nuvio/internal/jellyfin"
	"nuvio/internal/media"
	"nuvio/internal/metadata"
	"nuvio/internal/model"
	"nuvio/internal/store"
)

// Config controls the HTTP server and data locations.
type Config struct {
	Addr    string
	DataDir string
	Demo    bool
	FFmpeg  string
	FFprobe string
	WebFS   fs.FS
}

// Server owns all long lived components.
type Server struct {
	cfg     Config
	Store   *store.Store
	Catalog *catalog.Catalog
	API     *jellyfin.API
	HLS     *hls.Manager

	setupMu     sync.Mutex
	scanMu      sync.Mutex
	scanning    bool
	pendingScan bool
	lastScanErr string
}

// New initialises the store, catalog and API. It does not start scanning.
func New(cfg Config) (*Server, error) {
	st, err := store.Open(cfg.DataDir, "Nuvio")
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, Store: st, Catalog: catalog.New()}
	if err := s.reloadCatalog(); err != nil {
		st.Close()
		return nil, err
	}

	ffmpeg := resolveTool(cfg.FFmpeg, st.Settings().FFmpegPath, "NUVIO_FFMPEG", "ffmpeg")
	ffprobe := resolveTool(cfg.FFprobe, st.Settings().FFprobePath, "NUVIO_FFPROBE", "ffprobe")

	hasDemo := cfg.Demo
	if !hasDemo {
		for _, lib := range st.Libraries() {
			if lib.Demo {
				hasDemo = true
				break
			}
		}
	}
	demoFile := ""
	if hasDemo {
		dir := filepath.Join(cfg.DataDir, "demo")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			p := filepath.Join(dir, "sample.mp4")
			if stat, err := os.Stat(p); err != nil || stat.Size() != int64(len(demo.SampleBytes())) {
				_ = os.WriteFile(p, demo.SampleBytes(), 0o644)
			}
			demoFile = p
		}
	}

	s.API = &jellyfin.API{
		Store:    st,
		Catalog:  s.Catalog,
		DataDir:  cfg.DataDir,
		ImageDir: filepath.Join(cfg.DataDir, "cache", "images"),
		DemoFile: demoFile,
		Started:  time.Now(),
	}
	s.API.SetTools(ffmpeg, ffprobe)
	s.API.SetEncoder(detectEncoder(ffmpeg))
	vaapi, qsv := detectHardware(ffmpeg)
	s.API.SetHardware(vaapi, qsv)
	switch {
	case vaapi != "" && qsv != "":
		log.Printf("Hardware acceleration: vaapi (%s), qsv (%s)", vaapi, qsv)
	case vaapi != "":
		log.Printf("Hardware acceleration: vaapi (%s)", vaapi)
	case qsv != "":
		log.Printf("Hardware acceleration: qsv (%s)", qsv)
	default:
		log.Printf("Hardware acceleration: not available, using software")
	}
	s.HLS = hls.New(filepath.Join(cfg.DataDir, "transcode"), s.API.FFmpeg)
	s.API.HLS = s.HLS
	return s, nil
}

func detectEncoder(ffmpeg string) string {
	if ffmpeg == "" {
		return ""
	}
	out, err := exec.Command(ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		return "libx264"
	}
	text := string(out)
	for _, name := range []string{"libx264", "libopenh264", "h264_v4l2m2m"} {
		if strings.Contains(text, " "+name+" ") {
			return name
		}
	}
	return "libx264"
}

// detectHardware finds a usable VAAPI device. Intel and AMD GPUs on Linux are
// both exposed through /dev/dri/renderD*, and ffmpeg's h264_vaapi encoder is
// available in the Alpine package used by the container image.
func detectHardware(ffmpeg string) (string, string) {
	if ffmpeg == "" {
		return "", ""
	}
	device := detectRenderDevice()
	if device == "" {
		return "", ""
	}
	var vaapi, qsv string
	if ffmpegHas(ffmpeg, "-hwaccels", "vaapi") && ffmpegHas(ffmpeg, "-encoders", "h264_vaapi") && vaapiWorks(ffmpeg, device) {
		vaapi = device
	}
	if ffmpegHas(ffmpeg, "-hwaccels", "qsv") && ffmpegHas(ffmpeg, "-encoders", "h264_qsv") && qsvWorks(ffmpeg, device) {
		qsv = device
	}
	return vaapi, qsv
}

func vaapiWorks(ffmpeg, device string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-vaapi_device", device,
		"-f", "lavfi", "-i", "testsrc2=size=160x120:rate=1",
		"-frames:v", "1",
		"-vf", "format=nv12,hwupload",
		"-c:v", "h264_vaapi",
		"-f", "null", "-",
	).Run() == nil
}

func qsvWorks(ffmpeg, device string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-qsv_device", device,
		"-init_hw_device", "qsv=hw",
		"-filter_hw_device", "hw",
		"-f", "lavfi", "-i", "testsrc2=size=160x120:rate=1",
		"-frames:v", "1",
		"-vf", "format=nv12,hwupload=extra_hw_frames=64,format=qsv",
		"-c:v", "h264_qsv",
		"-f", "null", "-",
	).Run() == nil
}

func ffmpegHas(ffmpeg, flag, needle string) bool {
	out, err := exec.Command(ffmpeg, "-hide_banner", flag).Output()
	return err == nil && strings.Contains(string(out), needle)
}

func detectRenderDevice() string {
	for i := 128; i < 140; i++ {
		p := fmt.Sprintf("/dev/dri/renderD%d", i)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func resolveTool(explicit, saved, env, name string) string {
	if explicit != "" {
		return explicit
	}
	if saved != "" {
		if _, err := os.Stat(saved); err == nil {
			return saved
		}
	}
	if v := os.Getenv(env); v != "" {
		return v
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// Handler builds the complete HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.API.Register(mux)

	mux.HandleFunc("GET /api/app/config", s.handleConfig)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/app/logout", s.handleLogout)
	mux.HandleFunc("GET /api/app/scan", s.handleScanStatus)
	mux.HandleFunc("POST /api/app/password", s.handlePassword)
	mux.HandleFunc("GET /api/app/sessions", s.handleSessions)
	mux.HandleFunc("DELETE /api/app/sessions/{id}", s.handleRevokeSession)
	mux.HandleFunc("GET /api/admin/overview", s.handleAdminOverview)
	mux.HandleFunc("GET /api/admin/users", s.handleAdminUsers)
	mux.HandleFunc("POST /api/admin/users", s.handleAdminCreateUser)
	mux.HandleFunc("PUT /api/admin/users/{id}", s.handleAdminUpdateUser)
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.handleAdminDeleteUser)
	mux.HandleFunc("POST /api/admin/libraries", s.handleAddLibrary)
	mux.HandleFunc("DELETE /api/admin/libraries/{id}", s.handleRemoveLibrary)
	mux.HandleFunc("POST /api/admin/scan", s.handleTriggerScan)
	mux.HandleFunc("PUT /api/admin/settings", s.handleSettings)

	for _, prefix := range []string{"/Users/", "/UserItems/", "/UserPlayedItems/", "/UserFavoriteItems/",
		"/Items", "/Items/", "/Videos/", "/videos/", "/Sessions/", "/System/", "/Shows/", "/Search/", "/Branding/",
		"/DisplayPreferences/", "/UserViews", "/Genres", "/Persons", "/Audio/", "/api/"} {
		mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 404, map[string]string{"error": "endpoint not implemented"})
		})
	}
	mux.HandleFunc("/", s.handleSPA)

	// Jellyfin matches route paths case-insensitively, so clients are free to
	// send their own casing (Findroid builds /items/{id}/Images/...). Normalise
	// before dispatch so Go's case-sensitive patterns still apply.
	return s.withCORS(s.withLogging(withAPIPathCase(mux)))
}

// canonicalSegments maps lower-case route literals back to the casing used by
// the registered patterns.
var canonicalSegments = func() map[string]string {
	names := []string{
		"System", "Info", "Public", "Configuration", "Branding",
		"Users", "AuthenticateByName", "Me", "UserViews", "Views",
		"Items", "Resume", "Latest", "Root", "Suggestions", "Similar",
		"PlaybackInfo", "Images", "Shows", "Seasons", "Episodes", "NextUp",
		"Search", "Hints", "UserItems", "UserData", "UserPlayedItems",
		"UserFavoriteItems", "PlayedItems", "FavoriteItems", "PlayingItems",
		"Genres", "Persons", "DisplayPreferences", "Sessions", "Playing",
		"Progress", "Stopped", "Ping", "Capabilities", "Full", "Videos",
		"Audio", "stream", "master.m3u8", "hls", "Subtitles",
	}
	m := make(map[string]string, len(names))
	for _, n := range names {
		m[strings.ToLower(n)] = n
	}
	return m
}()

// singleSegmentAPIRoutes is the set of one-segment paths that only exist in
// the API. Paths such as /users and /search are also SPA routes, so they are
// deliberately excluded.
var singleSegmentAPIRoutes = map[string]bool{
	"items": true, "userviews": true, "genres": true, "persons": true,
}

// canonicalAPIPath rewrites the API literals in p to their registered casing.
// Path values (item ids, image types, file names) are left untouched.
func canonicalAPIPath(p string) string {
	if p == "" || p == "/" {
		return p
	}
	// /api/... is Nuvio's own frontend API, not the Jellyfin API. It is
	// matched exactly, and rewriting segments here used to turn
	// /api/app/sessions into /api/app/Sessions (404 "endpoint not
	// implemented") because "sessions" is also a Jellyfin route literal.
	if p == "/api" || strings.HasPrefix(p, "/api/") {
		return p
	}
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(segments) == 1 {
		lower := strings.ToLower(segments[0])
		if !singleSegmentAPIRoutes[lower] {
			return p
		}
		if c, ok := canonicalSegments[lower]; ok {
			return "/" + c
		}
		return p
	}
	changed := false
	for i, seg := range segments {
		if c, ok := canonicalSegments[strings.ToLower(seg)]; ok && c != seg {
			segments[i] = c
			changed = true
		}
	}
	if !changed {
		return p
	}
	return "/" + strings.Join(segments, "/")
}

func withAPIPathCase(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := canonicalAPIPath(r.URL.Path); p != r.URL.Path {
			r = r.Clone(r.Context())
			r.URL.Path = p
			r.URL.RawPath = ""
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, HEAD, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(rec, r)
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

// reloadCatalog restores libraries and persisted items from SQLite.
func (s *Server) reloadCatalog() error {
	libs := s.Store.Libraries()
	items, err := s.Store.Items()
	if err != nil {
		return err
	}
	allowed := make(map[string]bool, len(libs))
	for _, lib := range libs {
		allowed[lib.ID] = true
	}
	kept := items[:0]
	for _, it := range items {
		if allowed[it.LibraryID] {
			kept = append(kept, it)
		}
	}
	s.Catalog.Replace(libs, kept)
	s.Catalog.SetLastScan(s.Store.LastScan())
	return nil
}

// catalogVersion is bumped when a scan can discover new metadata, forcing one
// automatic rescan for existing libraries after an upgrade.
const catalogVersion = 2

// ScanIfNeeded performs the one-time scan for an existing library set that has
// never been persisted. Normal startups only load nuvio.db.
func (s *Server) ScanIfNeeded() {
	if len(s.Store.Libraries()) == 0 {
		return
	}
	if s.Store.Settings().ScannerVersion < catalogVersion {
		s.StartScan()
		return
	}
	if s.Catalog.Status().ItemCount == 0 && s.Store.LastScan().IsZero() {
		s.StartScan()
	}
}

// StartScan launches a catalog scan in the background.
func (s *Server) StartScan() {
	s.scanMu.Lock()
	if s.scanning {
		s.pendingScan = true
		s.scanMu.Unlock()
		return
	}
	s.scanning = true
	s.scanMu.Unlock()
	s.Catalog.SetScanning(true)
	go s.runScan()
}

func (s *Server) runScan() {
	start := time.Now()
	libs := s.Store.Libraries()
	oldItems, _ := s.Store.Items()
	oldByLibrary := map[string][]*model.Item{}
	for _, it := range oldItems {
		oldByLibrary[it.LibraryID] = append(oldByLibrary[it.LibraryID], it)
	}
	var items []*model.Item
	var errs []string
	demoIDs := map[string]bool{}
	for _, l := range libs {
		if l.Demo {
			demoIDs[l.ID] = true
		}
	}
	if len(demoIDs) > 0 {
		for _, it := range demo.Items() {
			if demoIDs[it.LibraryID] {
				items = append(items, it)
			}
		}
	}
	for _, lib := range libs {
		if lib.Demo {
			continue
		}
		scanner := media.Scanner{FFprobe: s.API.FFprobe()}
		got, err := scanner.Scan(context.Background(), lib)
		if err != nil {
			log.Printf("Failed to scan library %s: %v", lib.Name, err)
			errs = append(errs, fmt.Sprintf("%s: %v", lib.Name, err))
			items = append(items, oldByLibrary[lib.ID]...)
			continue
		}
		log.Printf("Library %s: %d items", lib.Name, len(got))
		items = append(items, got...)
	}
	// Libraries may have been added or removed while we were scanning. Only
	// publish items that still belong to a configured library.
	current := s.Store.Libraries()
	allowed := make(map[string]bool, len(current))
	for _, lib := range current {
		allowed[lib.ID] = true
	}
	kept := items[:0]
	for _, it := range items {
		if allowed[it.LibraryID] {
			kept = append(kept, it)
		}
	}
	settings := s.Store.Settings()
	tmdbKey := settings.TMDBAPIKey
	if tmdbKey == "" {
		tmdbKey = os.Getenv("NUVIO_TMDB_API_KEY")
	}
	if tmdbKey != "" {
		enricher := metadata.New(tmdbKey, settings.MetadataLanguage)
		if err := enricher.Enrich(context.Background(), kept); err != nil {
			log.Printf("TMDb metadata enrichment failed: %v", err)
			errs = append(errs, "TMDb metadata: "+err.Error())
		}
	}
	if err := s.Store.ReplaceItems(kept); err != nil {
		log.Printf("Could not save the media index: %v", err)
		errs = append(errs, "Could not save the media index: "+err.Error())
	}
	s.Catalog.Replace(current, kept)
	s.Catalog.SetLastScan(s.Store.LastScan())
	s.Store.UpdateSettings(func(st *model.ServerSettings) {
		st.ScannerVersion = catalogVersion
	})
	s.scanMu.Lock()
	s.scanning = false
	s.lastScanErr = strings.Join(errs, "; ")
	pending := s.pendingScan
	s.pendingScan = false
	s.scanMu.Unlock()
	if len(errs) > 0 {
		s.Catalog.SetScanError(strings.Join(errs, "; "))
	}
	log.Printf("Scan finished: %d items in %s", len(kept), time.Since(start).Round(time.Millisecond))
	if pending {
		s.StartScan()
	}
}

// Close stops transcoding sessions and closes SQLite.
func (s *Server) Close() {
	s.HLS.StopAll()
	_ = s.Store.Close()
}

// App API ----------------------------------------------------------------

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	st := s.Store.Settings()
	status := s.Catalog.Status()
	writeJSON(w, 200, map[string]any{
		"serverName":    st.ServerName,
		"version":       jellyfin.Version,
		"apiVersion":    jellyfin.ApiVersion,
		"setupComplete": s.Store.SetupComplete(),
		"ffmpeg":        s.API.FFmpeg() != "",
		"ffprobe":       s.API.FFprobe() != "",
		"demo":          s.cfg.Demo,
		"tmdb":          st.TMDBAPIKey != "" || os.Getenv("NUVIO_TMDB_API_KEY") != "",
		"scanning":      status.Scanning || s.isScanning(),
		"itemCount":     status.ItemCount,
		"serverId":      s.Store.ServerID(),
	})
}

func (s *Server) isScanning() bool {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	return s.scanning
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.Store.SetupComplete() {
		writeJSON(w, 409, map[string]any{"error": "The server is already set up"})
		return
	}
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Invalid request format"})
		return
	}
	u, err := s.Store.CreateUser(body.Name, body.Password, true)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if s.cfg.Demo {
		for _, lib := range demo.Libraries() {
			s.Store.EnsureLibrary(lib)
		}
	}
	s.Catalog.SetViews(s.Store.Libraries())
	token, err := s.Store.IssueToken(u.ID, store.SessionMeta{
		DeviceName: jellyfin.DeviceLabel(r),
		Client:     "Nuvio Web",
		Address:    jellyfin.ClientAddress(r),
	})
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "Could not create a session"})
		return
	}
	s.StartScan()
	writeJSON(w, 200, map[string]any{
		"accessToken": token,
		"userId":      u.ID,
		"userName":    u.Name,
		"serverId":    s.Store.ServerID(),
		"serverName":  s.Store.Settings().ServerName,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.API.UserFromRequest(r); ok && u != nil {
		if tok := tokenOf(r); tok != "" {
			s.Store.RevokeToken(tok)
		}
	}
	w.WriteHeader(204)
}

func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.API.UserFromRequest(r); !ok {
		writeJSON(w, 401, map[string]any{"error": "Not signed in"})
		return
	}
	status := s.Catalog.Status()
	status.Scanning = status.Scanning || s.isScanning()
	writeJSON(w, 200, status)
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.API.UserFromRequest(r)
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "Not signed in"})
		return
	}
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Invalid request format"})
		return
	}
	if !s.Store.VerifyPassword(u.ID, body.Current) {
		writeJSON(w, 403, map[string]any{"error": "Current password is incorrect"})
		return
	}
	if len(body.New) < 4 {
		writeJSON(w, 400, map[string]any{"error": "New password must be at least 4 characters"})
		return
	}
	if err := s.Store.SetPassword(u.ID, body.New); err != nil {
		writeJSON(w, 500, map[string]any{"error": "Update failed"})
		return
	}
	w.WriteHeader(204)
}

// Sessions API -----------------------------------------------------------

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := s.API.UserFromRequest(r)
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "Not signed in"})
		return
	}
	token := tokenOf(r)
	if meta := jellyfin.SessionMetaFromRequest(r); meta.DeviceName != "" || meta.Client != "" || meta.Version != "" {
		s.Store.RefreshSession(token, meta)
	}
	sessions := s.Store.Sessions(u.ID, token)
	for _, sess := range sessions {
		if sess.Current && sess.DeviceName == "" {
			s.Store.DescribeToken(token, store.SessionMeta{
				DeviceName: jellyfin.DeviceLabel(r),
				Client:     "Nuvio Web",
				Address:    jellyfin.ClientAddress(r),
			})
			sessions = s.Store.Sessions(u.ID, token)
			break
		}
	}
	for i := range sessions {
		sessions[i].DeviceName = jellyfin.DecodeClientText(sessions[i].DeviceName)
		sessions[i].Client = jellyfin.DecodeClientText(sessions[i].Client)
		sessions[i].Version = jellyfin.DecodeClientText(sessions[i].Version)
	}
	if sessions == nil {
		sessions = []store.Session{}
	}
	writeJSON(w, 200, sessions)
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	u, ok := s.API.UserFromRequest(r)
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "Not signed in"})
		return
	}
	if !s.Store.RevokeSession(u.ID, r.PathValue("id")) {
		writeJSON(w, 404, map[string]any{"error": "Device not found"})
		return
	}
	w.WriteHeader(204)
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	u, ok := s.API.UserFromRequest(r)
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "Not signed in"})
		return nil, false
	}
	if !u.IsAdmin {
		writeJSON(w, 403, map[string]any{"error": "Administrator rights required"})
		return nil, false
	}
	return u, true
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	status := s.Catalog.Status()
	status.Scanning = status.Scanning || s.isScanning()
	views := s.Catalog.Views()
	libs := make([]map[string]any, 0, len(views))
	for _, v := range views {
		paths := v.Library.Paths
		if paths == nil {
			paths = []string{}
		}
		libs = append(libs, map[string]any{
			"id":        v.Library.ID,
			"name":      v.Library.Name,
			"type":      v.Library.Type,
			"paths":     paths,
			"demo":      v.Library.Demo,
			"itemCount": v.ItemCount,
		})
	}
	users := s.Store.Users()
	userOut := make([]map[string]any, 0, len(users))
	for _, u := range users {
		userOut = append(userOut, userPayload(u))
	}
	writeJSON(w, 200, map[string]any{
		"serverName": s.Store.Settings().ServerName,
		"version":    jellyfin.Version,
		"apiVersion": jellyfin.ApiVersion,
		"dataPath":   s.cfg.DataDir,
		"dbPath":     s.Store.Path(),
		"demo":       s.cfg.Demo,
		"tmdb":       s.Store.Settings().TMDBAPIKey != "" || os.Getenv("NUVIO_TMDB_API_KEY") != "",
		"ffmpeg":     s.API.FFmpeg(),
		"ffprobe":    s.API.FFprobe(),
		"encoder":    s.API.Encoder(),
		"hardware":   s.API.HardwareMode(),
		"scan":       status,
		"counts":     s.Catalog.Counts(),
		"libraries":  libs,
		"users":      userOut,
		"settings":   s.Store.Settings(),
	})
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	users := s.Store.Users()
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, userPayload(u))
	}
	writeJSON(w, 200, out)
}

func userPayload(u model.User) map[string]any {
	libs := u.AllowedLibraries
	if libs == nil {
		libs = []string{}
	}
	return map[string]any{
		"id":               u.ID,
		"name":             u.Name,
		"isAdmin":          u.IsAdmin,
		"hasPassword":      u.HasPassword,
		"allowedLibraries": libs,
		"allowTranscode":   u.AllowTranscode,
		"allowRemux":       u.AllowRemux,
		"createdAt":        u.CreatedAt,
	}
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Admin    bool   `json:"admin"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Invalid request format"})
		return
	}
	u, err := s.Store.CreateUser(body.Name, body.Password, body.Admin)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, userPayload(*u))
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	target, found := s.Store.FindUser(r.PathValue("id"))
	if !found {
		writeJSON(w, 404, map[string]any{"error": "User not found"})
		return
	}
	var body struct {
		Password         *string   `json:"password"`
		IsAdmin          *bool     `json:"isAdmin"`
		AllowedLibraries *[]string `json:"allowedLibraries"`
		AllowTranscode   *bool     `json:"allowTranscode"`
		AllowRemux       *bool     `json:"allowRemux"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Invalid request format"})
		return
	}
	if body.Password != nil {
		if len(*body.Password) < 4 {
			writeJSON(w, 400, map[string]any{"error": "Password must be at least 4 characters"})
			return
		}
		if err := s.Store.SetPassword(target.ID, *body.Password); err != nil {
			writeJSON(w, 500, map[string]any{"error": "Could not change the password"})
			return
		}
	}
	if body.IsAdmin != nil && *body.IsAdmin != target.IsAdmin {
		if target.ID == actor.ID && !*body.IsAdmin {
			writeJSON(w, 400, map[string]any{"error": "You cannot remove your own administrator rights"})
			return
		}
		if target.IsAdmin && !*body.IsAdmin && s.Store.AdminCount() <= 1 {
			writeJSON(w, 400, map[string]any{"error": "Keep at least one administrator"})
			return
		}
		if err := s.Store.SetUserAdmin(target.ID, *body.IsAdmin); err != nil {
			writeJSON(w, 500, map[string]any{"error": "Could not update permissions"})
			return
		}
		target.IsAdmin = *body.IsAdmin
	}
	libs := target.AllowedLibraries
	if body.AllowedLibraries != nil {
		valid := map[string]bool{}
		for _, v := range s.Store.Libraries() {
			valid[v.ID] = true
		}
		for _, id := range *body.AllowedLibraries {
			if !valid[id] {
				writeJSON(w, 400, map[string]any{"error": "Library not found: " + id})
				return
			}
		}
		libs = *body.AllowedLibraries
	}
	transcode := target.AllowTranscode
	if body.AllowTranscode != nil {
		transcode = *body.AllowTranscode
	}
	remux := target.AllowRemux
	if body.AllowRemux != nil {
		remux = *body.AllowRemux
	}
	if err := s.Store.UpdateUserPolicy(target.ID, libs, transcode, remux); err != nil {
		writeJSON(w, 500, map[string]any{"error": "Could not update permissions"})
		return
	}
	updated, _ := s.Store.FindUser(target.ID)
	if updated == nil {
		updated = target
	}
	writeJSON(w, 200, userPayload(*updated))
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	target, found := s.Store.FindUser(r.PathValue("id"))
	if !found {
		writeJSON(w, 404, map[string]any{"error": "User not found"})
		return
	}
	if target.ID == actor.ID {
		writeJSON(w, 400, map[string]any{"error": "You cannot delete the account you are signed in with"})
		return
	}
	if target.IsAdmin && s.Store.AdminCount() <= 1 {
		writeJSON(w, 400, map[string]any{"error": "Keep at least one administrator"})
		return
	}
	if err := s.Store.DeleteUser(target.ID); err != nil {
		writeJSON(w, 500, map[string]any{"error": "Delete failed"})
		return
	}
	w.WriteHeader(204)
}

func (s *Server) handleAddLibrary(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		Name  string   `json:"name"`
		Type  string   `json:"type"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Invalid request format"})
		return
	}
	clean := make([]string, 0, len(body.Paths))
	for _, p := range body.Paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if !filepath.IsAbs(p) {
			writeJSON(w, 400, map[string]any{"error": "Enter an absolute path: " + p})
			return
		}
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			writeJSON(w, 400, map[string]any{"error": "Directory not found: " + p})
			return
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		writeJSON(w, 400, map[string]any{"error": "At least one media folder is required"})
		return
	}
	lib, err := s.Store.AddLibrary(body.Name, body.Type, clean, false)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	s.Catalog.SetViews(s.Store.Libraries())
	go s.StartScan()
	writeJSON(w, 200, map[string]any{"id": lib.ID, "name": lib.Name, "type": lib.Type, "paths": lib.Paths})
}

func (s *Server) handleRemoveLibrary(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.Store.RemoveLibrary(id); err != nil {
		writeJSON(w, 500, map[string]any{"error": "Delete failed"})
		return
	}
	if err := s.reloadCatalog(); err != nil {
		writeJSON(w, 500, map[string]any{"error": "Could not reload libraries"})
		return
	}
	w.WriteHeader(204)
}

func (s *Server) handleTriggerScan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	s.StartScan()
	writeJSON(w, 202, map[string]any{"scanning": true})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		ServerName       *string `json:"serverName"`
		TranscodeEnabled *bool   `json:"transcodeEnabled"`
		RemuxEnabled     *bool   `json:"remuxEnabled"`
		MaxBitrateMbps   *int    `json:"maxBitrateMbps"`
		TranscodePreset  *string `json:"transcodePreset"`
		HardwareAccel    *string `json:"hardwareAccel"`
		FFmpegPath       *string `json:"ffmpegPath"`
		FFprobePath      *string `json:"ffprobePath"`
		TMDBAPIKey       *string `json:"tmdbApiKey"`
		MetadataLanguage *string `json:"metadataLanguage"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Invalid request format"})
		return
	}
	for _, p := range []*string{body.FFmpegPath, body.FFprobePath} {
		if p == nil || strings.TrimSpace(*p) == "" {
			continue
		}
		st, err := os.Stat(strings.TrimSpace(*p))
		if err != nil || st.IsDir() {
			writeJSON(w, 400, map[string]any{"error": "Binary path does not exist: " + *p})
			return
		}
	}
	if body.HardwareAccel != nil {
		switch strings.ToLower(strings.TrimSpace(*body.HardwareAccel)) {
		case "auto", "vaapi", "qsv", "none":
		default:
			writeJSON(w, 400, map[string]any{"error": "Invalid hardware acceleration mode"})
			return
		}
	}
	oldProbe := s.API.FFprobe()
	oldSettings := s.Store.Settings()
	settings := s.Store.UpdateSettings(func(st *model.ServerSettings) {
		if body.ServerName != nil && strings.TrimSpace(*body.ServerName) != "" {
			st.ServerName = strings.TrimSpace(*body.ServerName)
		}
		if body.TranscodeEnabled != nil {
			st.TranscodeEnabled = *body.TranscodeEnabled
		}
		if body.RemuxEnabled != nil {
			st.RemuxEnabled = *body.RemuxEnabled
		}
		if body.MaxBitrateMbps != nil && *body.MaxBitrateMbps >= 1 && *body.MaxBitrateMbps <= 200 {
			st.MaxBitrateMbps = *body.MaxBitrateMbps
		}
		if body.TranscodePreset != nil {
			st.TranscodePreset = *body.TranscodePreset
		}
		if body.HardwareAccel != nil {
			st.HardwareAccel = strings.ToLower(strings.TrimSpace(*body.HardwareAccel))
		}
		if body.FFmpegPath != nil {
			st.FFmpegPath = strings.TrimSpace(*body.FFmpegPath)
		}
		if body.FFprobePath != nil {
			st.FFprobePath = strings.TrimSpace(*body.FFprobePath)
		}
		if body.TMDBAPIKey != nil {
			st.TMDBAPIKey = strings.TrimSpace(*body.TMDBAPIKey)
		}
		if body.MetadataLanguage != nil && strings.TrimSpace(*body.MetadataLanguage) != "" {
			st.MetadataLanguage = strings.TrimSpace(*body.MetadataLanguage)
		}
	})
	ffmpeg := resolveTool("", settings.FFmpegPath, "NUVIO_FFMPEG", "ffmpeg")
	ffprobe := resolveTool("", settings.FFprobePath, "NUVIO_FFPROBE", "ffprobe")
	s.API.SetTools(ffmpeg, ffprobe)
	s.API.SetEncoder(detectEncoder(ffmpeg))
	s.API.SetHardware(detectHardware(ffmpeg))
	metadataChanged := oldSettings.TMDBAPIKey != settings.TMDBAPIKey || oldSettings.MetadataLanguage != settings.MetadataLanguage
	if ffprobe != oldProbe || (metadataChanged && (settings.TMDBAPIKey != "" || os.Getenv("NUVIO_TMDB_API_KEY") != "")) {
		go s.StartScan()
	}
	writeJSON(w, 200, settings)
}

// SPA --------------------------------------------------------------------

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" || rel == "." {
		rel = "index.html"
	}
	if rel != "index.html" {
		if f, err := fs.Stat(s.cfg.WebFS, rel); err == nil && !f.IsDir() {
			s.serveFile(w, r, rel)
			return
		}
	}
	// SPA fallback for client side routes.
	s.serveFile(w, r, "index.html")
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := s.cfg.WebFS.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return
	}
	data, _ := io.ReadAll(f)
	http.ServeContent(w, r, name, st.ModTime(), bytes.NewReader(data))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func tokenOf(r *http.Request) string {
	if v := r.Header.Get("X-Emby-Token"); v != "" {
		return v
	}
	if v := r.URL.Query().Get("api_key"); v != "" {
		return v
	}
	return ""
}
