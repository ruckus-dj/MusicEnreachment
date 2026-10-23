package metadata

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"go.senan.xyz/taglib"
)

func TestCoverMIMEIdentifiesTIFFSignatures(t *testing.T) {
	classicLittle := minimalLittleEndianTIFF1x1()
	classicBig := []byte{'M', 'M', 0, 42, 0, 0, 0, 8}
	bigTIFFLittle := make([]byte, 16)
	copy(bigTIFFLittle, "II")
	binary.LittleEndian.PutUint16(bigTIFFLittle[2:], 43)
	binary.LittleEndian.PutUint16(bigTIFFLittle[4:], 8)
	binary.LittleEndian.PutUint64(bigTIFFLittle[8:], 16)
	bigTIFFBig := make([]byte, 16)
	copy(bigTIFFBig, "MM")
	binary.BigEndian.PutUint16(bigTIFFBig[2:], 43)
	binary.BigEndian.PutUint16(bigTIFFBig[4:], 8)
	binary.BigEndian.PutUint64(bigTIFFBig[8:], 16)

	for _, test := range []struct {
		name string
		data []byte
		want string
	}{
		{name: "classic little-endian", data: classicLittle, want: "image/tiff"},
		{name: "classic big-endian", data: classicBig, want: "image/tiff"},
		{name: "BigTIFF little-endian", data: bigTIFFLittle, want: "image/tiff"},
		{name: "BigTIFF big-endian", data: bigTIFFBig, want: "image/tiff"},
		{name: "PNG stays PNG", data: generatedPNG(t), want: "image/png"},
		{name: "unknown bytes stay unknown", data: []byte{0x00, 0x01, 0x02, 0x03}, want: "application/octet-stream"},
		{name: "truncated TIFF header", data: []byte{'I', 'I', 42, 0}, want: "application/octet-stream"},
		{name: "BigTIFF with wrong offset size", data: []byte{'I', 'I', 43, 0, 4, 0, 0, 0, 16, 0, 0, 0, 0, 0, 0, 0}, want: "application/octet-stream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := coverMIME(test.data); got != test.want {
				t.Fatalf("coverMIME(%x) = %q, want %q", test.data, got, test.want)
			}
		})
	}

	if got := http.DetectContentType(classicLittle); got != "application/octet-stream" {
		t.Fatalf("net/http now recognizes TIFF directly (%q); revisit the helper", got)
	}
}

func TestReadCoverInspectionReportsTIFFMIMEAndPreservesBytes(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "eg.flac"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeMetadataFixture(t, ".flac", fixture)
	if err := ClearArtwork(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	tiff := minimalLittleEndianTIFF1x1()
	if err := taglib.WriteImageOptions(path, tiff, 0, "Front Cover", "tiff source picture", "image/tiff"); err != nil {
		t.Fatal(err)
	}
	properties, err := taglib.ReadProperties(path)
	if err != nil || len(properties.Images) != 1 {
		t.Fatalf("TIFF fixture image count = %d, %v", len(properties.Images), err)
	}

	capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Properties != nil {
		t.Fatalf("properties were read without being requested: %#v", capture.Properties)
	}
	if capture.CoverInspection == nil || !capture.CoverInspection.Observed || capture.CoverInspection.Cover == nil {
		t.Fatalf("inspection = %#v", capture.CoverInspection)
	}
	if capture.CoverInspection.Cover.MIME != "image/tiff" {
		t.Fatalf("cover MIME = %q", capture.CoverInspection.Cover.MIME)
	}
	if !bytes.Equal(capture.CoverInspection.Cover.Data, tiff) {
		t.Fatalf("cover bytes changed: got %d bytes, want %d", len(capture.CoverInspection.Cover.Data), len(tiff))
	}
}

// minimalLittleEndianTIFF1x1 builds a valid classic little-endian TIFF with a
// single 8-bit grayscale pixel and a single strip.
func minimalLittleEndianTIFF1x1() []byte {
	const entries = 9
	const pixelOffset = 8 + 2 + entries*12 + 4
	entry := func(tag, valueType uint16, value uint32) []byte {
		encoded := make([]byte, 12)
		binary.LittleEndian.PutUint16(encoded[0:], tag)
		binary.LittleEndian.PutUint16(encoded[2:], valueType)
		binary.LittleEndian.PutUint32(encoded[4:], 1)
		binary.LittleEndian.PutUint32(encoded[8:], value)
		return encoded
	}

	data := make([]byte, 8)
	copy(data, "II")
	binary.LittleEndian.PutUint16(data[2:], 42)
	binary.LittleEndian.PutUint32(data[4:], 8)
	data = binary.LittleEndian.AppendUint16(data, entries)
	for _, value := range [][3]uint32{
		{256, 4, 1},           // ImageWidth
		{257, 4, 1},           // ImageLength
		{258, 3, 8},           // BitsPerSample
		{259, 3, 1},           // Compression: none
		{262, 3, 1},           // PhotometricInterpretation: BlackIsZero
		{273, 4, pixelOffset}, // StripOffsets
		{277, 3, 1},           // SamplesPerPixel
		{278, 4, 1},           // RowsPerStrip
		{279, 4, 1},           // StripByteCounts
	} {
		data = append(data, entry(uint16(value[0]), uint16(value[1]), value[2])...)
	}
	data = binary.LittleEndian.AppendUint32(data, 0) // next IFD
	return append(data, 0x00)                        // one black pixel
}
