package static

import "testing"

func TestShouldServeIndex(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/", want: false},
		{path: "/settings", want: true},
		{path: "/library/releases", want: true},
		{path: "/assets/app.js", want: false},
		{path: "/favicon.ico", want: false},
	}
	for _, test := range tests {
		if got := shouldServeIndex(test.path); got != test.want {
			t.Errorf("shouldServeIndex(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestCacheControl(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/", want: "no-cache"},
		{path: "/settings", want: "no-cache"},
		{path: "/index.html", want: "no-cache"},
		{path: "/assets/index-ABC123.js", want: "public, max-age=31536000, immutable"},
	}
	for _, test := range tests {
		if got := cacheControl(test.path); got != test.want {
			t.Errorf("cacheControl(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}
