package jellyfin

import "testing"

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
