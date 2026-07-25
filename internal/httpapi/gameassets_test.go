package httpapi

import "testing"

// TestImageContentTypeRejectsActiveContent pins the asset content-type
// allowlist. game_assets.content_type is a plain column, so anything able
// to write that table must not be able to make the app serve HTML (or any other
// active content) from its own origin.
func TestImageContentTypeRejectsActiveContent(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"image/webp", "image/webp"},
		{"image/png", "image/png"},
		{"image/jpeg", "image/jpeg"},
		{"image/jpg", "image/jpeg"},
		{"IMAGE/WEBP", "image/webp"},
		{"  image/png  ", "image/png"},
		{"text/html", "application/octet-stream"},
		{"image/svg+xml", "application/octet-stream"},
		{"application/javascript", "application/octet-stream"},
		{"", "application/octet-stream"},
	}

	for _, tt := range tests {
		if got := imageContentType(tt.in); got != tt.want {
			t.Errorf("imageContentType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
