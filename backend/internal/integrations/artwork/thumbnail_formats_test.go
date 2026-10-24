package artwork

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// gopherDocLosslessWebP is golang.org/x/image@v0.47.0/testdata/
// gopher-doc.1bpp.lossless.webp, inlined so the fixture does not depend on the
// Go module cache. golang.org/x/image is licensed under the BSD 3-Clause
// license.
const gopherDocLosslessWebP = `
UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm
/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX
0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YV
kFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadP
BPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36su
B9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAat
Qa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+g
vGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI
1vEqxAhotocAAA==`

func TestThumbnailNativeFormats(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString(gopherDocLosslessWebP)
	if err != nil {
		t.Fatalf("decode WebP fixture: %v", err)
	}
	for _, tc := range []struct {
		name   string
		encode func(*image.NRGBA) ([]byte, error)
	}{
		{"GIF", func(img *image.NRGBA) ([]byte, error) {
			var b bytes.Buffer
			err := gif.Encode(&b, img, nil)
			return b.Bytes(), err
		}},
		{"TIFF", func(img *image.NRGBA) ([]byte, error) {
			var b bytes.Buffer
			err := tiff.Encode(&b, img, nil)
			return b.Bytes(), err
		}},
		{"BMP", func(img *image.NRGBA) ([]byte, error) {
			var b bytes.Buffer
			err := bmp.Encode(&b, img)
			return b.Bytes(), err
		}},
		{"WebP", func(*image.NRGBA) ([]byte, error) { return webp, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewNRGBA(image.Rect(0, 0, 64, 32))
			for y := 0; y < 32; y++ {
				for x := 0; x < 64; x++ {
					img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), A: 255})
				}
			}
			data, err := tc.encode(img)
			if err != nil {
				t.Fatal(err)
			}
			_, sourceWidth, sourceHeight, err := Inspect(data)
			if err != nil {
				t.Fatalf("inspect %s fixture: %v", tc.name, err)
			}
			assertNativeThumbnail(t, tc.name, data, sourceWidth, sourceHeight)
		})
	}
}

func assertNativeThumbnail(t *testing.T, name string, data []byte, sourceWidth, sourceHeight int) {
	t.Helper()
	dir := t.TempDir()
	input, output := filepath.Join(dir, "original."+name), filepath.Join(dir, "thumbnail.png")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := Thumbnail(context.Background(), input, output); err != nil {
		t.Fatalf("Thumbnail(%s): %v", name, err)
	}
	result, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	format, width, height, err := Inspect(result)
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" {
		t.Fatalf("output format = %q, want png", format)
	}
	if width > MaxPublishedDimension || height > MaxPublishedDimension || width > sourceWidth || height > sourceHeight {
		t.Fatalf("output %dx%d exceeds source %dx%d or bound", width, height, sourceWidth, sourceHeight)
	}
	// Aspect ratio may shift by at most one pixel per axis from rounding during
	// downscaling; compare cross-multiplied dimensions rather than orientation.
	aspectDelta := width*sourceHeight - height*sourceWidth
	if aspectDelta < 0 {
		aspectDelta = -aspectDelta
	}
	if aspectDelta > (sourceWidth+sourceHeight)/2 {
		t.Fatalf("aspect ratio changed: source %dx%d output %dx%d", sourceWidth, sourceHeight, width, height)
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("original changed (read error: %v)", err)
	}
}
