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
