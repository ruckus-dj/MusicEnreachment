//go:build windows

package sourcefs

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"golang.org/x/sys/windows"
)

// TestWindowsCleanupMissingRecognizesCanonicalNotExist guards the cleanup
// classifier against openDir mapping NTSTATUS not-found to a joined
// fs.ErrNotExist error instead of a bare NTStatus.
func TestWindowsCleanupMissingRecognizesCanonicalNotExist(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "joined canonical not exist and name not found",
			err:  errors.Join(fs.ErrNotExist, windows.STATUS_OBJECT_NAME_NOT_FOUND),
			want: true,
		},
		{
			name: "wrapped joined canonical not exist and path not found",
			err:  fmt.Errorf("open intermediate: %w", errors.Join(fs.ErrNotExist, windows.STATUS_OBJECT_PATH_NOT_FOUND)),
			want: true,
		},
		{
			name: "native name not found",
			err:  windows.STATUS_OBJECT_NAME_NOT_FOUND,
			want: true,
		},
		{
			name: "native path not found",
			err:  windows.STATUS_OBJECT_PATH_NOT_FOUND,
			want: true,
		},
		{
			name: "canonical not exist",
			err:  fs.ErrNotExist,
			want: true,
		},
		{
			name: "native access denied",
			err:  windows.STATUS_ACCESS_DENIED,
			want: false,
		},
		{
			name: "wrapped access denied",
			err:  fmt.Errorf("open artifact: %w", windows.STATUS_ACCESS_DENIED),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWindowsCleanupMissing(tt.err); got != tt.want {
				t.Fatalf("isWindowsCleanupMissing(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
