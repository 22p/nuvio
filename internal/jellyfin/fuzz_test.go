package jellyfin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"nuvio/internal/catalog"
	"nuvio/internal/model"
)

// FuzzPageBounds checks the paging invariant every endpoint relies on:
// the returned window is always a valid slice of [0,total].
func FuzzPageBounds(f *testing.F) {
	for _, seed := range []struct {
		start, limit, total int
	}{
		{0, 0, 0}, {0, 20, 100}, {-5, 2, 100}, {99, 10, 100},
		{1 << 62, 2, 100}, {0, 1 << 62, 100}, {-1, -1, 7}, {7, 0, 7},
	} {
		f.Add(seed.start, seed.limit, seed.total)
	}
	f.Fuzz(func(t *testing.T, start, limit, total int) {
		if total < 0 {
			total = -total
		}
		total %= 1000
		if start > 1<<40 {
			start = start % (1 << 40)
		}
		if limit > 1<<40 {
			limit = limit % (1 << 40)
		}
		lo, hi := catalog.PageBounds(start, limit, total)
		if lo < 0 || lo > total || hi < lo || hi > total {
			t.Fatalf("PageBounds(%d,%d,%d) = %d,%d out of range", start, limit, total, lo, hi)
		}
	})
}

// FuzzNormalizeID keeps clients that send dotted GUIDs or padded ids working
// without letting weird input through unchecked.
func FuzzNormalizeID(f *testing.F) {
	for _, seed := range []string{"", "  ", "abc", "aabbccdd-eeff-0011-2233-445566778899", "abc-", "-", "..--.."} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, id string) {
		got := normalizeID(id)
		if strings.Contains(got, "-") {
			t.Fatalf("normalizeID(%q) still has a dash: %q", id, got)
		}
		if strings.TrimSpace(got) != got {
			t.Fatalf("normalizeID(%q) kept whitespace: %q", id, got)
		}
		if again := normalizeID(got); again != got {
			t.Fatalf("normalizeID is not idempotent: %q -> %q -> %q", id, got, again)
		}
	})
}

// FuzzItemsQuery feeds arbitrary paging/filter values to the browse endpoint.
// Bad values must be tolerated, never crash the handler.
func FuzzItemsQuery(f *testing.F) {
	a, h := newTestAPI(f)
	u, tok := testUser(f, a)
	for _, s := range []struct{ start, limit, types string }{
		{"0", "20", ""},
		{"-5", "2", "Movie"},
		{"9223372036854775807", "2", "Movie,Episode"},
		{"abc", "xyz", "Bogus"},
		{"", "", ","},
	} {
		f.Add(s.start, s.limit, s.types)
	}
	f.Fuzz(func(t *testing.T, start, limit, types string) {
		q := url.Values{}
		q.Set("userId", u.ID)
		q.Set("startIndex", clip(start, 64))
		q.Set("limit", clip(limit, 64))
		if types != "" {
			q.Set("includeItemTypes", clip(types, 128))
		}
		rec := serve(t, h, http.MethodGet, "/Items?"+q.Encode(), tok, "")
		if rec.Code >= 500 {
			t.Fatalf("start=%q limit=%q types=%q -> %d", start, limit, types, rec.Code)
		}
		if rec.Code == http.StatusOK && !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("invalid json: %s", rec.Body.String())
		}
	})
}

// FuzzItemIDPath throws malformed ids at every route that takes one.
func FuzzItemIDPath(f *testing.F) {
	a, h := newTestAPI(f)
	_, tok := testUser(f, a)
	movie := catalogItem(f, a, model.TypeMovie, true)
	for _, seed := range []string{movie.ID, hyphenateID(movie.ID), "deadbeef", "", "..", "%00", strings.Repeat("f", 300)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, id string) {
		id = url.PathEscape(clip(id, 128))
		for _, path := range []string{
			"/Items/" + id,
			"/Videos/" + id + "/stream?static=true",
			"/Items/" + id + "/Images/Primary",
			"/Items/" + id + "/PlaybackInfo",
		} {
			rec := serve(t, h, http.MethodGet, path, tok, "")
			if rec.Code >= 500 {
				t.Fatalf("%s -> %d: %s", path, rec.Code, rec.Body.String())
			}
		}
	})
}

// FuzzPlaybackReport sends arbitrary bodies to the progress endpoints. The
// decoders must reject input without taking the server down.
func FuzzPlaybackReport(f *testing.F) {
	a, h := newRuntimeAPI(f)
	_, tok := testUser(f, a)
	movie := catalogItem(f, a, model.TypeMovie, true)
	for _, seed := range []string{
		"{}",
		"{\"ItemId\":\"" + movie.ID + "\"}",
		"{\"ItemId\":\"" + movie.ID + "\",\"PositionTicks\":1000000}",
		"{\"ItemId\":\"" + movie.ID + "\",\"Failed\":true}",
		"{",
		"null",
		"[]",
		"{\"PositionTicks\":-1}",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		body = clip(body, 4096)
		for _, path := range []string{"/Sessions/Playing", "/Sessions/Playing/Progress", "/Sessions/Playing/Stopped"} {
			rec := serve(t, h, http.MethodPost, path, tok, body)
			if rec.Code >= 500 {
				t.Fatalf("%s body=%q -> %d: %s", path, body, rec.Code, rec.Body.String())
			}
		}
	})
}

// FuzzSearchTerm checks the query parser and the matching code against odd
// terms, including control characters and very long strings.
func FuzzSearchTerm(f *testing.F) {
	a, h := newTestAPI(f)
	u, tok := testUser(f, a)
	for _, seed := range []string{"a", "星际", "  ", "\x00", strings.Repeat("x", 4096), "%"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, term string) {
		term = clip(term, 512)
		q := url.Values{}
		q.Set("userId", u.ID)
		q.Set("searchTerm", term)
		rec := serve(t, h, http.MethodGet, "/Search/Hints?"+q.Encode(), tok, "")
		switch rec.Code {
		case http.StatusOK, http.StatusBadRequest:
		default:
			t.Fatalf("term=%q -> %d: %s", term, rec.Code, rec.Body.String())
		}
		if rec.Code == http.StatusOK && !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("invalid json: %s", rec.Body.String())
		}
	})
}

// FuzzVTTToSRT keeps the WebVTT to SubRip converter from panicking on
// malformed subtitles and from inventing cues that were not in the input.
func FuzzVTTToSRT(f *testing.F) {
	for _, seed := range []string{
		"",
		"WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nhello\n",
		"WEBVTT\n\n00:00:01.000 --> 00:00:02.000 line:0 position:20%\nhello\nworld\n",
		"WEBVTT",
		"no header but --> here",
		"-->",
		"\x00\xff\n\n-->\n",
		strings.Repeat("a\n\n", 200),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := vttToSRT([]byte(in))
		if again := vttToSRT([]byte(in)); string(out) != string(again) {
			t.Fatal("vttToSRT is not deterministic")
		}
		if n, m := strings.Count(string(out), "-->"), strings.Count(in, "-->"); n > m {
			t.Fatalf("output has %d cues but input only %d: %q", n, m, out)
		}
	})
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
