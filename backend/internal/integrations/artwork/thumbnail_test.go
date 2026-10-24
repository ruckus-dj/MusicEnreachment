package artwork

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestThumbnailResizesNativeImagesWithoutUpscaling(t *testing.T) {
	for _, test := range []struct {
		name   string
		width  int
		height int
		encode func(*image.NRGBA) ([]byte, error)
	}{
		{name: "landscape PNG", width: 2050, height: 1025, encode: encodePNG},
		{name: "portrait JPEG", width: 1025, height: 2050, encode: encodeJPEG},
		{name: "small PNG", width: 40, height: 20, encode: encodePNG},
	} {
		t.Run(test.name, func(t *testing.T) {
			imageData, err := test.encode(image.NewNRGBA(image.Rect(0, 0, test.width, test.height)))
			if err != nil {
				t.Fatal(err)
			}
			input, output := filepath.Join(t.TempDir(), "input.img"), filepath.Join(t.TempDir(), "thumbnail.png")
			if err := os.WriteFile(input, imageData, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			if err := Thumbnail(context.Background(), input, output); err != nil {
				t.Fatal(err)
			}
			thumbnail, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			_, width, height, err := Inspect(thumbnail)
			if err != nil {
				t.Fatal(err)
			}
			if width > MaxPublishedDimension || height > MaxPublishedDimension || width > test.width || height > test.height {
				t.Fatalf("thumbnail dimensions %dx%d exceed bounds for %dx%d source", width, height, test.width, test.height)
			}
			if test.width > test.height && width <= height || test.height > test.width && height <= width {
				t.Fatalf("thumbnail lost source aspect orientation: %dx%d", width, height)
			}
			after, err := os.ReadFile(input)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("original input changed (read error: %v)", err)
			}
		})
	}
}

func TestThumbnailRejectsMalformedAndOversizedHeaders(t *testing.T) {
	valid, err := encodePNG(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatal(err)
	}
	tooLarge := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(tooLarge[16:20], MaxOriginalDimension+1)
	binary.BigEndian.PutUint32(tooLarge[29:33], crc32.ChecksumIEEE(tooLarge[12:29]))
	for _, inputData := range [][]byte{{1, 2, 3}, tooLarge} {
		directory := t.TempDir()
		input, output := filepath.Join(directory, "input"), filepath.Join(directory, "output.png")
		if err := os.WriteFile(input, inputData, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Thumbnail(context.Background(), input, output); err == nil {
			t.Fatal("expected invalid or oversized header error")
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatalf("unexpected output after rejection: %v", err)
		}
	}
}

func TestThumbnailDoesNotReplaceExistingOutput(t *testing.T) {
	data, err := encodePNG(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	input, output := filepath.Join(directory, "input.png"), filepath.Join(directory, "output.png")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Thumbnail(context.Background(), input, output); err == nil {
		t.Fatal("expected existing-output error")
	}
	contents, err := os.ReadFile(output)
	if err != nil || string(contents) != "keep me" {
		t.Fatalf("existing output changed: %q, %v", contents, err)
	}
}

func TestThumbnailUsesLiteralInputPathAndPrivateOutputMode(t *testing.T) {
	data, err := encodePNG(image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	input := filepath.Join(directory, "input[page=1].png")
	output := filepath.Join(directory, "thumbnail.png")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Thumbnail(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Inspect(mustReadFile(t, output)); err != nil {
		t.Fatal(err)
	}
	privateInputs, err := filepath.Glob(filepath.Join(directory, ".thumbnail-input-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(privateInputs) != 0 {
		t.Fatalf("private input staging remains after thumbnail generation: %v", privateInputs)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(output)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("thumbnail mode = %04o, want 0600", got)
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func encodePNG(img *image.NRGBA) ([]byte, error) {
	var output bytes.Buffer
	err := png.Encode(&output, img)
	return output.Bytes(), err
}

func encodeJPEG(img *image.NRGBA) ([]byte, error) {
	var output bytes.Buffer
	err := jpeg.Encode(&output, img, &jpeg.Options{Quality: 90})
	return output.Bytes(), err
}
