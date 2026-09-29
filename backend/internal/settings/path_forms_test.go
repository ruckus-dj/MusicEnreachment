package settings_test

import (
	"runtime"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestNormalizePathPlatformForms(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		linux          string
		macos          string
		windows        string
		windowsInvalid bool
	}{
		{name: "POSIX absolute", path: "/melotrove-path-form-tests/nested/../tools", linux: "/melotrove-path-form-tests/tools", macos: "/melotrove-path-form-tests/tools"},
		{name: "POSIX volume root", path: "/", linux: "/", macos: "/"},
		{name: "Windows drive absolute", path: `C:\__melotrove_path_form_tests__\nested\..\tools`, windows: `C:\__melotrove_path_form_tests__\tools`},
		{name: "Windows drive volume root", path: `C:\`, windows: `C:\`},
		{name: "Windows drive-relative", path: `C:tools\..\output`, windowsInvalid: true},
		{name: "Windows UNC absolute", path: `\\server\share\__melotrove_path_form_tests__\nested\..\output`, windows: `\\server\share\__melotrove_path_form_tests__\output`},
		{name: "Windows UNC share root", path: `\\server\share\`, windows: `\\server\share\`},
		{name: "Windows root-relative", path: `\tools\output`, windowsInvalid: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := settings.NormalizePath(test.path)
			var want string
			switch runtime.GOOS {
			case "linux":
				want = test.linux
			case "darwin":
				want = test.macos
			case "windows":
				want = test.windows
				if test.windowsInvalid {
					want = ""
				}
			default:
				t.Skipf("path-form expectations are defined for linux, darwin, and windows; running on %s", runtime.GOOS)
			}
			if want == "" {
				if err == nil {
					t.Fatalf("NormalizePath(%q) = %q, nil; want rejection on %s", test.path, got, runtime.GOOS)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizePath(%q): %v", test.path, err)
			}
			if got != want {
				t.Errorf("NormalizePath(%q) = %q; want %q on %s", test.path, got, want, runtime.GOOS)
			}
		})
	}
}

func TestPathsOverlapAtWindowsVolumeAndShareRoots(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows volume and share-root overlap semantics")
	}

	tests := []struct {
		name  string
		root  string
		child string
	}{
		{name: "drive volume root", root: `C:\`, child: `C:\__melotrove_path_form_tests__\output`},
		{name: "UNC share root", root: `\\server\share\`, child: `\\server\share\__melotrove_path_form_tests__\output`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !settings.PathsOverlap(test.root, test.child) {
				t.Errorf("PathsOverlap(%q, %q) = false; want true", test.root, test.child)
			}
		})
	}
}
