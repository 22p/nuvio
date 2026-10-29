package jellyfin

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nuvio/internal/art"
	"nuvio/internal/hls"
	"nuvio/internal/model"
)

// imageItem resolves catalog items and synthetic entries (libraries, root,
// genres, people) that clients address with the same endpoint.
func (a *API) imageItem(rawID string) (*model.Item, bool) {
	id := normalizeID(rawID)
	if it, ok := a.Catalog.Get(id); ok {
		return it, true
	}
	return a.virtualItem(id)
}

func (a *API) handleImage(w http.ResponseWriter, r *http.Request) {
	// Jellyfin clients load artwork through plain <img>/URL requests that carry
	// no credentials, so images are served anonymously like the reference
	// server. A token, when present, still enforces library permissions.
	it, ok := a.imageItem(r.PathValue("itemId"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if u, ok := a.userFromRequest(r); ok && !canSeeItem(u, it) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	kind := strings.ToLower(r.PathValue("imageType"))
	var path, remoteURL string
	widescreen := false
	switch kind {
	case "primary", "poster":
		path = it.PosterPath
		remoteURL = it.PosterURL
		// TMDb-only libraries have no local poster; use the episode still
		// rather than the inherited series poster.
		if it.Type == model.TypeEpisode && path == "" && it.ThumbURL != "" {
			remoteURL = it.ThumbURL
		}
	case "backdrop", "background":
		path = it.BackdropPath
		remoteURL = it.BackdropURL
		widescreen = true
	case "thumb":
		path = it.ThumbPath
		if path == "" {
			path = it.BackdropPath
		}
		remoteURL = it.ThumbURL
		if remoteURL == "" {
			remoteURL = it.BackdropURL
		}
		widescreen = true
	case "logo":
		path = it.LogoPath
		remoteURL = it.LogoURL
	case "banner", "art":
		path = it.BackdropPath
		remoteURL = it.BackdropURL
		widescreen = true
	default:
		http.Error(w, "unsupported image type", http.StatusNotFound)
		return
	}
	// Nuvio stores a single image per type, so any index above zero does not
	// exist.
	if hls.ParseInt(r.PathValue("imageIndex"), 0) > 0 {
		http.Error(w, "image not found", http.StatusNotFound)
		return
	}
	etag := `"` + imageTag(it, kind) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=86400")

	if path != "" {
		f, err := os.Open(path)
		if err == nil {
			defer f.Close()
			st, _ := f.Stat()
			head := make([]byte, 512)
			n, _ := f.Read(head)
			_, _ = f.Seek(0, io.SeekStart)
			ct := http.DetectContentType(head[:n])
			if ct == "application/octet-stream" {
				ct = "image/jpeg"
			}
			w.Header().Set("Content-Type", ct)
			if st != nil {
				http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
				return
			}
		}
	}

	if remoteURL != "" && a.serveRemoteImage(w, r, it, remoteURL) {
		return
	}
	if kind == "logo" {
		http.Error(w, "no logo", http.StatusNotFound)
		return
	}

	maxWidth := hls.ParseInt(queryValues(r).Get("maxWidth"), 0)
	if maxWidth == 0 {
		maxWidth = hls.ParseInt(queryValues(r).Get("width"), 0)
	}
	maxHeight := hls.ParseInt(queryValues(r).Get("maxHeight"), 0)
	if maxHeight == 0 {
		maxHeight = hls.ParseInt(queryValues(r).Get("height"), 0)
	}
	baseW, baseH, width := 2, 3, 600
	if widescreen {
		baseW, baseH, width = 16, 9, 1600
	}
	height := width * baseH / baseW
	switch {
	case maxWidth > 0 && maxHeight > 0:
		if maxWidth*baseH < maxHeight*baseW {
			width, height = maxWidth, maxWidth*baseH/baseW
		} else {
			width, height = maxHeight*baseW/baseH, maxHeight
		}
	case maxWidth > 0:
		width, height = maxWidth, maxWidth*baseH/baseW
	case maxHeight > 0:
		width, height = maxHeight*baseW/baseH, maxHeight
	}
	if width > 2000 {
		width = 2000
	}
	if height > 3000 {
		height = 3000
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	seed := it.ArtSeed
	if seed == "" {
		seed = it.ID
	}
	data := art.Render(seed+":"+kind, width, height)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// serveRemoteImage downloads and caches a TMDb CDN image. Returning false lets
// the caller fall back to generated artwork when the CDN is unreachable.
func (a *API) serveRemoteImage(w http.ResponseWriter, r *http.Request, it *model.Item, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(strings.ToLower(u.Hostname()), "tmdb.org") {
		return false
	}
	if a.ImageDir == "" {
		return false
	}
	sum := md5.Sum([]byte(rawURL))
	name := hex.EncodeToString(sum[:]) + imageExt(u.Path)
	cachePath := filepath.Join(a.ImageDir, imageCategory(it), name)
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		writeImageBytes(w, data)
		return true
	}

	a.imageMu.Lock()
	if a.imageHTTP == nil {
		a.imageHTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if a.imageSem == nil {
		a.imageSem = make(chan struct{}, 6)
	}
	client, sem := a.imageHTTP, a.imageSem
	a.imageMu.Unlock()

	// Library pages request dozens of posters at once; a global lock made them
	// download one by one and many timed out. Cache hits above stay lock-free.
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-r.Context().Done():
		return false
	}
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		writeImageBytes(w, data)
		return true
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20))
	if err != nil || len(data) == 0 {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		tmp := cachePath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err == nil {
			_ = os.Rename(tmp, cachePath)
		}
	}
	writeImageBytes(w, data)
	return true
}

// imageCategory groups the on-disk artwork cache by media type.
func imageCategory(it *model.Item) string {
	if it == nil {
		return "other"
	}
	switch it.Type {
	case model.TypeMovie:
		return "movies"
	case model.TypeSeries, model.TypeSeason, model.TypeEpisode:
		return "tvshows"
	case "Person":
		return "people"
	}
	return "other"
}

// imageExt keeps the cache file browsable; unknown types fall back to .img.
func imageExt(urlPath string) string {
	switch ext := strings.ToLower(path.Ext(urlPath)); ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".avif":
		return ext
	default:
		return ".img"
	}
}

func writeImageBytes(w http.ResponseWriter, data []byte) {
	ct := http.DetectContentType(data)
	if !strings.HasPrefix(ct, "image/") {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}
