package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCanonicalAPIPath(t *testing.T) {
	cases := map[string]string{
		"/items": "/Items",
		"/items/39374607-622e-3258-f02c-addfa03e9c09/Images/Primary": "/Items/39374607-622e-3258-f02c-addfa03e9c09/Images/Primary",
		"/items/abc/Images/Backdrop/0":                               "/Items/abc/Images/Backdrop/0",
		"/users/abc/views":                                           "/Users/abc/Views",
		"/users/abc/items/latest":                                    "/Users/abc/Items/Latest",
		"/userviews":                                                 "/UserViews",
		"/useritems/resume":                                          "/UserItems/Resume",
		"/userplayeditems/abc":                                       "/UserPlayedItems/abc",
		"/userfavoriteitems/abc":                                     "/UserFavoriteItems/abc",
		"/shows/abc/nextup":                                          "/Shows/abc/NextUp",
		"/search/hints":                                              "/Search/Hints",
		"/system/info/public":                                        "/System/Info/Public",
		"/displaypreferences/usersettings":                           "/DisplayPreferences/usersettings",
		"/videos/abc/stream":                                         "/Videos/abc/stream",
		"/videos/abc/master.m3u8":                                    "/Videos/abc/master.m3u8",
		"/genres":                                                    "/Genres",
		"/persons":                                                   "/Persons",
		"/sessions/playing":                                          "/Sessions/Playing",
		// SPA routes keep their casing, otherwise client side routing breaks.
		"/users":  "/users",
		"/search": "/search",
		"/library/d285532134e62accb1f8ee105c2daa34": "/library/d285532134e62accb1f8ee105c2daa34",
		"/item/abc": "/item/abc",
		"/settings": "/settings",
		"/":         "/",
	}
	for in, want := range cases {
		if got := canonicalAPIPath(in); got != want {
			t.Fatalf("canonicalAPIPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The middleware must actually reach the registered pattern, which is what
// makes lower-case image URLs work.
func TestAPIPathCaseMiddleware(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("image"))
	})
	mux.HandleFunc("GET /Items/{itemId}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("item"))
	})
	h := withAPIPathCase(mux)

	for path, want := range map[string]string{
		"/items/abc/Images/Primary": "image",
		"/items/abc":                "item",
		"/Items/abc":                "item",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || rec.Body.String() != want {
			t.Fatalf("%s = %d %q, want 200 %q", path, rec.Code, rec.Body.String(), want)
		}
	}
}
