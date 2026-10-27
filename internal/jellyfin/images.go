package jellyfin

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nuvio/internal/art"
	"nuvio/internal/hls"
)

func (a *API) handleImage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	it, ok := a.Catalog.Get(normalizeID(r.PathValue("itemId")))
	if !ok || !canSeeItem(u, it) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	kind := strings.ToLower(r.PathValue("imageType"))
	var path string
	widescreen := false
	switch kind {
	case "primary", "poster":
		path = it.PosterPath
	case "backdrop", "background":
		path = it.BackdropPath
		widescreen = true
	case "thumb":
		path = it.ThumbPath
		if path == "" {
			path = it.BackdropPath
		}
		widescreen = true
	case "logo":
		path = it.LogoPath
	case "banner", "art":
		path = it.BackdropPath
		widescreen = true
	default:
		http.Error(w, "unsupported image type", http.StatusNotFound)
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

	remoteURL := ""
	if widescreen {
		remoteURL = it.BackdropURL
	} else if kind == "primary" || kind == "poster" {
		remoteURL = it.PosterURL
	}
	if remoteURL != "" && a.serveRemoteImage(w, r, remoteURL) {
		return
	}
	if kind == "logo" {
		http.Error(w, "no logo", http.StatusNotFound)
		return
	}

	width := hls.ParseInt(r.URL.Query().Get("maxWidth"), 0)
	if width == 0 {
		width = hls.ParseInt(r.URL.Query().Get("width"), 0)
	}
	if width <= 0 {
		if widescreen {
			width = 1600
		} else {
			width = 600
		}
	}
	if width > 2000 {
		width = 2000
	}
	height := width * 3 / 2
	if widescreen {
		height = width * 9 / 16
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
func (a *API) serveRemoteImage(w http.ResponseWriter, r *http.Request, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(strings.ToLower(u.Hostname()), "tmdb.org") {
		return false
	}
	if a.ImageDir == "" {
		return false
	}
	sum := md5.Sum([]byte(rawURL))
	cachePath := filepath.Join(a.ImageDir, hex.EncodeToString(sum[:])+".img")
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		writeImageBytes(w, data)
		return true
	}

	a.imageMu.Lock()
	defer a.imageMu.Unlock()
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		writeImageBytes(w, data)
		return true
	}
	if a.imageHTTP == nil {
		a.imageHTTP = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	resp, err := a.imageHTTP.Do(req)
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
	if err := os.MkdirAll(a.ImageDir, 0o755); err == nil {
		tmp := cachePath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err == nil {
			_ = os.Rename(tmp, cachePath)
		}
	}
	writeImageBytes(w, data)
	return true
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
