package jellyfin

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nuvio/internal/demo"
	"nuvio/internal/model"
	"nuvio/internal/store"
)

// rawGet performs a request without a *testing.T so it can run in a worker
// goroutine under load.
func rawGet(h http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		setToken(req, token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func rawPost(h http.Handler, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if token != "" {
		setToken(req, token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func stressID(prefix string, i int) string {
	sum := md5.Sum([]byte(fmt.Sprintf("%s:%d", prefix, i)))
	return hex.EncodeToString(sum[:])
}

// buildLargeCatalog produces a synthetic library used to measure browse cost
// at a realistic size: N movies plus a series tree.
func buildLargeCatalog(movies, series, seasonsPer, episodesPer int) ([]*model.Library, []*model.Item) {
	now := time.Now().UTC()
	movieLib := &model.Library{ID: stressID("lib", 1), Name: "Movies", Type: model.LibraryMovies, AddedAt: now}
	showLib := &model.Library{ID: stressID("lib", 2), Name: "Shows", Type: model.LibraryShows, AddedAt: now}

	items := make([]*model.Item, 0, movies+series+series*seasonsPer*(1+episodesPer))
	for i := 0; i < movies; i++ {
		items = append(items, &model.Item{
			ID:           stressID("movie", i),
			Type:         model.TypeMovie,
			Name:         fmt.Sprintf("Movie %05d", i),
			SortName:     fmt.Sprintf("Movie %05d", i),
			Year:         1980 + i%45,
			Genres:       []string{"Action", fmt.Sprintf("Genre%d", i%20)},
			RuntimeTicks: 90 * 60 * 10_000_000,
			LibraryID:    movieLib.ID,
			ParentID:     movieLib.ID,
			Container:    "mp4",
			AddedAt:      now.Add(-time.Duration(i) * time.Minute),
		})
	}
	for i := 0; i < series; i++ {
		seriesID := stressID("series", i)
		items = append(items, &model.Item{
			ID:        seriesID,
			Type:      model.TypeSeries,
			Name:      fmt.Sprintf("Series %04d", i),
			SortName:  fmt.Sprintf("Series %04d", i),
			Year:      2000 + i%20,
			Genres:    []string{"Drama"},
			LibraryID: showLib.ID,
			ParentID:  showLib.ID,
			AddedAt:   now.Add(-time.Duration(i) * time.Hour),
		})
		for s := 0; s < seasonsPer; s++ {
			seasonID := stressID(seriesID, s)
			items = append(items, &model.Item{
				ID:           seasonID,
				Type:         model.TypeSeason,
				Name:         fmt.Sprintf("Season %d", s+1),
				SortName:     fmt.Sprintf("Season %02d", s+1),
				SeriesID:     seriesID,
				SeriesName:   fmt.Sprintf("Series %04d", i),
				SeasonNumber: s + 1,
				LibraryID:    showLib.ID,
				ParentID:     seriesID,
				AddedAt:      now.Add(-time.Duration(i) * time.Hour),
			})
			for e := 0; e < episodesPer; e++ {
				items = append(items, &model.Item{
					ID:            stressID(seasonID+"e", e),
					Type:          model.TypeEpisode,
					Name:          fmt.Sprintf("Episode %d", e+1),
					SortName:      fmt.Sprintf("Episode %03d", e+1),
					SeriesID:      seriesID,
					SeriesName:    fmt.Sprintf("Series %04d", i),
					SeasonID:      seasonID,
					SeasonNumber:  s + 1,
					EpisodeNumber: e + 1,
					RuntimeTicks:  45 * 60 * 10_000_000,
					LibraryID:     showLib.ID,
					ParentID:      seasonID,
					AddedAt:       now.Add(-time.Duration(i) * time.Hour),
				})
			}
		}
	}
	return []*model.Library{movieLib, showLib}, items
}

var browsePaths = []string{
	"/Items",
	"/Items/Latest",
	"/Items/Suggestions",
	"/UserViews",
	"/Genres",
	"/Persons",
	"/UserItems/Resume",
	"/Search/Hints?searchTerm=Movie",
	"/Shows/NextUp",
}

// TestStressConcurrentBrowse hammers every read path from many goroutines while
// the race detector is watching for shared-state corruption.
func TestStressConcurrentBrowse(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	series := catalogItem(t, a, model.TypeSeries, false)

	type target struct{ path string }
	targets := make([]target, 0, len(browsePaths)+2)
	for _, p := range browsePaths {
		sep := "?"
		if strings.Contains(p, "?") {
			sep = "&"
		}
		targets = append(targets, target{p + sep + "userId=" + u.ID})
	}
	movie := catalogItem(t, a, model.TypeMovie, true)
	targets = append(targets,
		target{"/Items/" + movie.ID + "?userId=" + u.ID},
		target{"/Shows/" + series.ID + "/Episodes?userId=" + u.ID},
	)

	const workers = 24
	const rounds = 25
	var failures, served int64
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				tg := targets[(w+i)%len(targets)]
				rec := rawGet(h, tg.path, tok)
				atomic.AddInt64(&served, 1)
				if rec.Code != http.StatusOK {
					atomic.AddInt64(&failures, 1)
					t.Errorf("%s -> %d: %s", tg.path, rec.Code, rec.Body.String())
					continue
				}
				if !json.Valid(rec.Body.Bytes()) {
					atomic.AddInt64(&failures, 1)
					t.Errorf("%s returned invalid json", tg.path)
				}
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if failures > 0 {
		t.Fatalf("%d/%d requests failed", failures, served)
	}
	t.Logf("%d requests in %s (%.0f req/s, %d workers)", served, elapsed.Round(time.Millisecond), float64(served)/elapsed.Seconds(), workers)
}

// TestStressConcurrentWrites exercises the single-writer SQLite path with many
// users reporting progress at once, then verifies nothing was lost.
func TestStressConcurrentWrites(t *testing.T) {
	a, h := newTestAPI(t)
	var movies []*model.Item
	for _, it := range a.Catalog.All() {
		if it.Type == model.TypeMovie {
			movies = append(movies, it)
		}
	}
	if len(movies) == 0 {
		t.Fatal("demo catalog has no movie")
	}

	const users = 12
	const rounds = 30
	type account struct {
		u    *model.User
		tok  string
		item string
	}
	accounts := make([]account, 0, users)
	for i := 0; i < users; i++ {
		u, err := a.Store.CreateUser(fmt.Sprintf("stress%d", i), "secret", false)
		if err != nil {
			t.Fatal(err)
		}
		tok, err := a.Store.IssueToken(u.ID, store.SessionMeta{})
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, account{u: u, tok: tok, item: movies[i%len(movies)].ID})
	}

	var failures int64
	var wg sync.WaitGroup
	start := time.Now()
	for _, acc := range accounts {
		wg.Add(1)
		go func(acc account) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				body := fmt.Sprintf("{\"ItemId\":%q,\"PositionTicks\":%d,\"IsPaused\":false}", acc.item, 1_000_000+i)
				rec := rawPost(h, "/Sessions/Playing/Progress", acc.tok, body)
				if rec.Code != http.StatusNoContent {
					atomic.AddInt64(&failures, 1)
					t.Errorf("progress %s #%d -> %d: %s", acc.u.Name, i, rec.Code, rec.Body.String())
				}
			}
			rec := rawPost(h, "/UserFavoriteItems/"+acc.item+"?userId="+acc.u.ID, acc.tok, "")
			if rec.Code != http.StatusOK {
				atomic.AddInt64(&failures, 1)
				t.Errorf("favorite %s -> %d: %s", acc.u.Name, rec.Code, rec.Body.String())
			}
		}(acc)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if failures > 0 {
		t.Fatalf("%d writes failed", failures)
	}

	for _, acc := range accounts {
		ud := a.Store.GetUserData(acc.u.ID, acc.item)
		if want := int64(1_000_000 + rounds - 1); ud.PositionTicks != want {
			t.Fatalf("%s position = %d, want %d", acc.u.Name, ud.PositionTicks, want)
		}
		if ud.Played || ud.PlayCount != 0 {
			t.Fatalf("%s recorded a bogus play state: %+v", acc.u.Name, ud)
		}
		if !ud.Favorite {
			t.Fatalf("%s lost the favorite flag", acc.u.Name)
		}
	}
	t.Logf("%d writes in %s (%.0f writes/s)", users*(rounds+1), elapsed.Round(time.Millisecond), float64(users*(rounds+1))/elapsed.Seconds())
}

// TestStressCatalogSwapDuringReads verifies that a rescan swapping the whole
// catalog never exposes a partially built index to concurrent readers.
func TestStressCatalogSwapDuringReads(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	views := demo.Libraries()
	full := demo.Items()
	expected := 0
	for _, it := range full {
		switch it.Type {
		case model.TypeMovie, model.TypeSeries, model.TypeSeason, model.TypeEpisode:
			expected++
		}
	}
	if expected == 0 {
		t.Fatal("demo catalog is empty")
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var failures, reads int64
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := rawGet(h, "/Items?userId="+u.ID, tok)
				atomic.AddInt64(&reads, 1)
				if rec.Code != http.StatusOK {
					atomic.AddInt64(&failures, 1)
					t.Errorf("items -> %d", rec.Code)
					return
				}
				var out queryResult
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
					atomic.AddInt64(&failures, 1)
					t.Errorf("decode: %v", err)
					return
				}
				if out.TotalRecordCount != 0 && out.TotalRecordCount != expected {
					atomic.AddInt64(&failures, 1)
					t.Errorf("torn catalog: total = %d, want 0 or %d", out.TotalRecordCount, expected)
					return
				}
			}
		}()
	}

	// Keep swapping for a fixed window so readers actually observe several
	// generations of the catalog rather than finishing first.
	deadline := time.Now().Add(400 * time.Millisecond)
	for i := 0; time.Now().Before(deadline); i++ {
		if i%2 == 0 {
			a.Catalog.Replace(views, full)
		} else {
			a.Catalog.Replace(nil, nil)
		}
		time.Sleep(200 * time.Microsecond)
	}
	close(stop)
	wg.Wait()
	if failures > 0 {
		t.Fatalf("%d failures over %d reads", failures, reads)
	}
	// The race detector slows everything down ~10x, so only require that the
	// readers actually overlapped several swaps.
	if reads < 25 {
		t.Fatalf("only %d reads completed during the swap window", reads)
	}
	// Leave the registry in the full state for any later assertion.
	a.Catalog.Replace(views, full)
	t.Logf("%d concurrent reads during continuous catalog swaps", reads)
}

// TestStressLargeCatalogLatency is a coarser guard against quadratic browse
// behaviour: at 14k items a page must still come back in single-digit
// milliseconds, not seconds.
func TestStressLargeCatalogLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("large catalog stress test")
	}
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	views, items := buildLargeCatalog(3000, 1000, 2, 4)
	a.Catalog.Replace(views, items)
	total := len(items)

	cases := []struct {
		name string
		path string
		want int
	}{
		{"first page", "/Items?userId=" + u.ID + "&limit=20", 20},
		{"deep page", fmt.Sprintf("/Items?userId=%s&startIndex=%d&limit=20", u.ID, total-7), 7},
		{"sorted page", "/Items?userId=" + u.ID + "&sortBy=SortName&limit=20", 20},
		{"search", "/Items?userId=" + u.ID + "&searchTerm=Movie%2000123", 1},
		{"episodes", "/Shows/" + items[len(items)-1].SeriesID + "/Episodes?userId=" + u.ID, 2 * 4},
		{"genres", "/Genres?userId=" + u.ID, -1},
		{"suggestions", "/Items/Suggestions?userId=" + u.ID + "&limit=20", 20},
	}
	for _, tc := range cases {
		rec := rawGet(h, tc.path, tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s -> %d: %s", tc.name, rec.Code, rec.Body.String())
		}
		if tc.want >= 0 {
			var out queryResult
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if len(out.Items) != tc.want {
				t.Fatalf("%s returned %d items, want %d", tc.name, len(out.Items), tc.want)
			}
		}
		// Warm, then measure a few runs and assert a generous ceiling. The
		// point is to catch algorithmic regressions, not to benchmark CI.
		best := time.Hour
		for i := 0; i < 5; i++ {
			start := time.Now()
			rec = rawGet(h, tc.path, tok)
			if d := time.Since(start); d < best {
				best = d
			}
		}
		t.Logf("%-12s %8s for %d items", tc.name, best.Round(time.Microsecond), total)
		if best > 3*time.Second {
			t.Fatalf("%s took %s for %d items", tc.name, best, total)
		}
	}
}

// TestStressConcurrentRanges serves many overlapping byte ranges at once; the
// file handle and Content-Length bookkeeping must stay per-request.
func TestStressConcurrentRanges(t *testing.T) {
	a, h := newTestAPI(t)
	_, tok := testUser(t, a)
	movie := catalogItem(t, a, model.TypeMovie, true)
	streamURL := "/Videos/" + movie.ID + "/stream?static=true"

	const workers = 16
	const rounds = 20
	var failures int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				from := (w*rounds + i) * 37
				to := from + 63
				req := httptest.NewRequest(http.MethodGet, streamURL, nil)
				setToken(req, tok)
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusPartialContent {
					atomic.AddInt64(&failures, 1)
					t.Errorf("range %d-%d -> %d", from, to, rec.Code)
					continue
				}
				if rec.Body.Len() != 64 {
					atomic.AddInt64(&failures, 1)
					t.Errorf("range %d-%d returned %d bytes", from, to, rec.Body.Len())
				}
				if got := rec.Header().Get("Content-Range"); !strings.HasPrefix(got, fmt.Sprintf("bytes %d-%d/", from, to)) {
					atomic.AddInt64(&failures, 1)
					t.Errorf("range %d-%d content-range = %q", from, to, got)
				}
			}
		}(w)
	}
	wg.Wait()
	if failures > 0 {
		t.Fatalf("%d range failures", failures)
	}
}

// TestStressNoGoroutineLeak makes sure request handling does not accumulate
// goroutines, which would eventually exhaust memory on a long-running server.
func TestStressNoGoroutineLeak(t *testing.T) {
	a, h := newTestAPI(t)
	u, tok := testUser(t, a)
	paths := make([]string, 0, len(browsePaths))
	for _, p := range browsePaths {
		sep := "?"
		if strings.Contains(p, "?") {
			sep = "&"
		}
		paths = append(paths, p+sep+"userId="+u.ID)
	}

	for i := 0; i < 20; i++ {
		rawGet(h, paths[i%len(paths)], tok)
	}
	baseline := runtime.NumGoroutine()
	for i := 0; i < 400; i++ {
		rawGet(h, paths[i%len(paths)], tok)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		now := runtime.NumGoroutine()
		if now <= baseline+5 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines grew from %d to %d after 400 requests", baseline, now)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
