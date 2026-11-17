package jellyfin

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"nuvio/internal/model"
)

func TestSafeImageContentType(t *testing.T) {
	for _, ct := range []string{
		"image/jpeg", "image/png; charset=binary", "image/webp", "image/gif", "image/avif", "image/bmp",
	} {
		if safeImageContentType(ct) == "" {
			t.Fatalf("%q should be accepted", ct)
		}
	}
	for _, ct := range []string{
		"text/html; charset=utf-8", "text/xml", "image/svg+xml", "application/octet-stream", "",
	} {
		if got := safeImageContentType(ct); got != "" {
			t.Fatalf("%q should be rejected, got %q", ct, got)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeImageClient(requests *int64, body []byte) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt64(requests, 1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"image/jpeg"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})}
}

// Scan-time prefetch must download each unique TMDb portrait once and skip
// hosts the proxy does not allow.
func TestPrefetchPersonImagesWarmsCache(t *testing.T) {
	a, _ := newTestAPI(t)
	a.ImageDir = t.TempDir()
	const portrait = "https://image.tmdb.org/t/p/w185/ada.jpg"
	jpeg := []byte{0xff, 0xd8, 0xff, 0xd9}
	var requests int64
	a.imageHTTP = fakeImageClient(&requests, jpeg)

	items := []*model.Item{
		{ID: "m1", Type: model.TypeMovie, People: []model.Person{{Name: "Ada", ImageURL: portrait}}},
		{ID: "m2", Type: model.TypeMovie, People: []model.Person{{Name: "Ada", ImageURL: portrait}}},
		{ID: "m3", Type: model.TypeMovie, People: []model.Person{{Name: "Eve", ImageURL: "https://evil.example/x.jpg"}}},
	}
	a.PrefetchPersonImages(items)

	if got := atomic.LoadInt64(&requests); got != 1 {
		t.Fatalf("downloads = %d, want 1 (duplicates and non-TMDb hosts must be skipped)", got)
	}
	sum := md5.Sum([]byte(portrait))
	cachePath := filepath.Join(a.ImageDir, "people", hex.EncodeToString(sum[:])+".jpg")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("portrait was not cached: %v", err)
	}
	if !bytes.Equal(data, jpeg) {
		t.Fatalf("cached bytes = %d, want %d", len(data), len(jpeg))
	}

	// A second scan over the same catalog must not hit the network again.
	a.PrefetchPersonImages(items)
	if got := atomic.LoadInt64(&requests); got != 1 {
		t.Fatalf("downloads after rescan = %d, want 1", got)
	}
}

// The cast DTO must point clients at the local image endpoint, not TMDb.
func TestPersonDTOUsesServerImage(t *testing.T) {
	a, h := newTestAPI(t)
	a.ImageDir = t.TempDir()
	u, _ := testUser(t, a)
	const portrait = "https://image.tmdb.org/t/p/w185/ada.jpg"
	jpeg := []byte{0xff, 0xd8, 0xff, 0xd9}

	it := &model.Item{
		ID: "film", Type: model.TypeMovie, Name: "Film", SortName: "Film", LibraryID: "lib",
		People: []model.Person{
			{Name: "Ada", Role: "Hero", Type: "Actor", ImageURL: portrait},
			{Name: "NoPhoto", Role: "Extra", Type: "Actor"},
		},
	}
	a.Catalog.Replace(nil, []*model.Item{it})

	dto := a.buildItem(it, a.userState(u, false), true, "")
	if len(dto.People) != 2 {
		t.Fatalf("people = %d", len(dto.People))
	}
	if dto.People[0].PrimaryImageTag == "" {
		t.Fatal("person with a portrait has no PrimaryImageTag")
	}
	if dto.People[1].PrimaryImageTag != "" {
		t.Fatal("person without a portrait should not advertise an image")
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("ImageUrl")) {
		t.Fatalf("external image URL leaked into the DTO: %s", raw)
	}

	sum := md5.Sum([]byte(portrait))
	cachePath := filepath.Join(a.ImageDir, "people", hex.EncodeToString(sum[:])+".jpg")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, jpeg, 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/Items/"+dto.People[0].Id+"/Images/Primary", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("person image status = %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), jpeg) {
		t.Fatalf("person image = %d bytes, want %d", rec.Body.Len(), len(jpeg))
	}
}
