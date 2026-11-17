package jellyfin

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	// Nuvio stores a single image per type, so only index 0 exists. Negative
	// and non-numeric indexes must not silently serve the primary image.
	if raw := r.PathValue("imageIndex"); raw != "" {
		idx, err := strconv.Atoi(raw)
		if err != nil || idx != 0 {
			http.Error(w, "image not found", http.StatusNotFound)
			return
		}
	}
	etag := `"` + imageTag(it, kind) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if path != "" {
		f, err := os.Open(path)
		if err == nil {
			defer f.Close()
			st, _ := f.Stat()
			head := make([]byte, 512)
			n, _ := f.Read(head)
			_, _ = f.Seek(0, io.SeekStart)
			// A file named poster.jpg is not necessarily an image. Only serve
			// recognized raster types: HTML or SVG content in a media folder
			// must not become a same-origin page.
			if ct := safeImageContentType(http.DetectContentType(head[:n])); ct != "" {
				w.Header().Set("Content-Type", ct)
				if st != nil {
					http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
					return
				}
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

// remoteImagePath accepts only https URLs served by TMDb's CDN. The media
// library may contain arbitrary URLs from NFO files; proxying those would let
// a crafted file make the server fetch internal addresses.
func remoteImagePath(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "tmdb.org" && !strings.HasSuffix(host, ".tmdb.org") {
		return "", false
	}
	return u.Path, true
}

// imageCachePath returns where a remote image is stored, grouped by media type.
func (a *API) imageCachePath(it *model.Item, rawURL string) string {
	urlPath, ok := remoteImagePath(rawURL)
	if !ok || a.ImageDir == "" {
		return ""
	}
	sum := md5.Sum([]byte(rawURL))
	return filepath.Join(a.ImageDir, imageCategory(it), hex.EncodeToString(sum[:])+imageExt(urlPath))
}

// remoteImageClient returns the shared HTTP client and concurrency gate. The
// gate is shared by request handling and scan-time prefetching so together
// they never exceed six concurrent downloads.
func (a *API) remoteImageClient() (*http.Client, chan struct{}) {
	a.imageMu.Lock()
	defer a.imageMu.Unlock()
	if a.imageHTTP == nil {
		a.imageHTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if a.imageSem == nil {
		a.imageSem = make(chan struct{}, 6)
	}
	return a.imageHTTP, a.imageSem
}

// fetchRemoteImage downloads one image. The caller holds a slot in the shared
// semaphore.
func fetchRemoteImage(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("unexpected content type %q", ct)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	return data, nil
}

// storeCachedImage writes through a temporary file so a crash or a concurrent
// reader never sees a partial image.
func (a *API) storeCachedImage(cachePath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(cachePath), ".img-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, cachePath); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// serveRemoteImage downloads and caches a TMDb CDN image. Returning false lets
// the caller fall back to generated artwork when the CDN is unreachable.
func (a *API) serveRemoteImage(w http.ResponseWriter, r *http.Request, it *model.Item, rawURL string) bool {
	if a.ImageDir == "" {
		return false
	}
	cachePath := a.imageCachePath(it, rawURL)
	if cachePath == "" {
		a.imageFailed(rawURL, fmt.Errorf("unsupported image URL"))
		return false
	}
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		writeImageBytes(w, data)
		return true
	}

	client, sem := a.remoteImageClient()

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
	data, err := fetchRemoteImage(r.Context(), client, rawURL)
	if err != nil {
		// A browser that navigates away cancels the request; that is not a
		// download failure worth logging.
		if r.Context().Err() == nil {
			a.imageFailed(rawURL, err)
		}
		return false
	}
	if err := a.storeCachedImage(cachePath, data); err != nil {
		log.Printf("Image cache write failed: %s: %v", cachePath, err)
	}
	writeImageBytes(w, data)
	return true
}

// PrefetchPersonImages fills the portrait cache for every cast member in the
// catalog. runScan calls it after enrichment so the web client can read every
// avatar from /Items/{id}/Images/Primary without touching TMDb.
func (a *API) PrefetchPersonImages(items []*model.Item) {
	if a.ImageDir == "" {
		return
	}
	type portrait struct {
		url  string
		path string
	}
	seen := make(map[string]bool)
	pending := make([]portrait, 0, 64)
	for _, it := range items {
		for _, p := range it.People {
			rawURL := strings.TrimSpace(p.ImageURL)
			if rawURL == "" || seen[rawURL] {
				continue
			}
			seen[rawURL] = true
			cachePath := a.imageCachePath(personItem(p.Name, rawURL), rawURL)
			if cachePath == "" {
				continue
			}
			if st, err := os.Stat(cachePath); err == nil && st.Size() > 0 {
				continue
			}
			pending = append(pending, portrait{url: rawURL, path: cachePath})
		}
	}
	if len(pending) == 0 {
		return
	}

	start := time.Now()
	client, sem := a.remoteImageClient()
	workers := len(pending)
	if workers > 6 {
		workers = 6
	}
	var downloaded, failed int64
	var wg sync.WaitGroup
	jobs := make(chan portrait)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				sem <- struct{}{}
				// A request may have filled the cache while this worker waited.
				if data, err := os.ReadFile(p.path); err == nil && len(data) > 0 {
					<-sem
					continue
				}
				data, err := fetchRemoteImage(context.Background(), client, p.url)
				<-sem
				if err != nil {
					a.imageFailed(p.url, err)
					atomic.AddInt64(&failed, 1)
					continue
				}
				if err := a.storeCachedImage(p.path, data); err != nil {
					log.Printf("Image cache write failed: %s: %v", p.path, err)
					atomic.AddInt64(&failed, 1)
					continue
				}
				atomic.AddInt64(&downloaded, 1)
			}
		}()
	}
	for _, p := range pending {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	log.Printf("Person image cache: %d downloaded, %d failed in %s", downloaded, failed, time.Since(start).Round(time.Millisecond))
}

// imageFailed logs a download error once per URL. A library page can request
// dozens of images, so repeat failures must not flood the log.
func (a *API) imageFailed(rawURL string, err error) {
	if _, loaded := a.imageFail.LoadOrStore(rawURL, struct{}{}); !loaded {
		log.Printf("Image fetch failed: %s: %v", rawURL, err)
	}
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
	ct := safeImageContentType(http.DetectContentType(data))
	if ct == "" {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// safeImageContentType returns the media type only for image formats a browser
// may safely render from a media library. SVG and HTML are intentionally
// excluded because they can execute script on the server origin.
func safeImageContentType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	switch strings.ToLower(strings.TrimSpace(ct)) {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "image/avif", "image/bmp":
		return ct
	}
	return ""
}
