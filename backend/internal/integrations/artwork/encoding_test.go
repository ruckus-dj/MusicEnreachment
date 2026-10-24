package artwork

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"testing"
)

func TestValidateEncodingAcceptsCompletePNGEnvelope(t *testing.T) {
	if err := ValidateEncoding(validEncodedPNG(), "png"); err != nil {
		t.Fatalf("ValidateEncoding() error = %v", err)
	}
}

func TestValidateEncodingRejectsTruncatedPNGEnvelope(t *testing.T) {
	data := validEncodedPNG()
	for _, cut := range []int{len(data) - 1, len(data) - 5, len(data) - 12, len(data) - 13, len(data) - 25, len(data) - 26} {
		if cut <= len(pngSignature) || cut >= len(data) {
			t.Fatalf("test cut %d is outside the encoded envelope", cut)
		}
		if err := ValidateEncoding(data[:cut], "png"); err == nil {
			t.Errorf("truncated envelope at byte %d was accepted", cut)
		}
	}
}

func TestValidateEncodingRejectsInvalidPNGChunkChecksum(t *testing.T) {
	data := validEncodedPNG()
	offset := bytes.Index(data, pngIDAT)
	if offset < 0 {
		t.Fatal("test envelope has no IDAT chunk")
	}
	data[offset+len(pngIDAT)] ^= 0xff
	if err := ValidateEncoding(data, "png"); err == nil {
		t.Fatal("envelope with a corrupted IDAT checksum was accepted")
	}
}

func TestValidateEncodingRejectsOversizedPNGChunkLength(t *testing.T) {
	for _, length := range []uint32{0xffffffff, 0x7fffffff} {
		var data bytes.Buffer
		data.Write(pngSignature)
		data.Write(encodedPNGChunk("IHDR", validPNGIHDR()))
		var header [8]byte
		binary.BigEndian.PutUint32(header[0:4], length)
		copy(header[4:8], pngIDAT)
		data.Write(header[:])
		data.Write([]byte{0})
		if err := ValidateEncoding(data.Bytes(), "png"); err == nil {
			t.Errorf("chunk length %#x beyond the encoded data was accepted", length)
		}
	}
}

func TestValidateEncodingRejectsIncompletePNGChunkSequence(t *testing.T) {
	ihdr := encodedPNGChunk("IHDR", validPNGIHDR())
	tests := []struct {
		name string
		data []byte
	}{
		{name: "missing IDAT", data: encodedPNGWithChunks(ihdr, encodedPNGChunk("IEND", nil))},
		{name: "missing IEND", data: encodedPNGWithChunks(ihdr, encodedPNGChunk("IDAT", []byte{0}))},
		{name: "non-empty IEND", data: encodedPNGWithChunks(ihdr, encodedPNGChunk("IDAT", []byte{0}), encodedPNGChunk("IEND", []byte{0}))},
		{name: "misplaced IHDR", data: encodedPNGWithChunks(encodedPNGChunk("IDAT", []byte{0}), ihdr, encodedPNGChunk("IEND", nil))},
		{name: "short IHDR", data: encodedPNGWithChunks(encodedPNGChunk("IHDR", make([]byte, 12)), encodedPNGChunk("IDAT", []byte{0}), encodedPNGChunk("IEND", nil))},
		{name: "data after IEND", data: append(validEncodedPNG(), 0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateEncoding(test.data, "png"); err == nil {
				t.Fatal("invalid PNG chunk sequence was accepted")
			}
		})
	}
}

func TestValidateEncodingKeepsOtherFormatsHeaderOnly(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEncoding(encoded.Bytes(), "jpeg"); err != nil {
		t.Fatalf("ValidateEncoding() rejected a valid JPEG header: %v", err)
	}
	withTrailer := append(append([]byte(nil), encoded.Bytes()...), 0, 1, 2)
	if err := ValidateEncoding(withTrailer, "jpeg"); err != nil {
		t.Fatalf("header-only JPEG validation rejected trailing bytes: %v", err)
	}
	if err := ValidateEncoding(validEncodedPNG(), "jpeg"); err == nil {
		t.Fatal("header of a different registered format was accepted")
	}
}

func validPNGIHDR() []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], 1)
	binary.BigEndian.PutUint32(ihdr[4:8], 1)
	ihdr[8], ihdr[9] = 8, 2
	return ihdr
}

func validEncodedPNG() []byte {
	return encodedPNGWithChunks(
		encodedPNGChunk("IHDR", validPNGIHDR()),
		encodedPNGChunk("IDAT", []byte{0}),
		encodedPNGChunk("IEND", nil),
	)
}

func encodedPNGWithChunks(chunks ...[]byte) []byte {
	var result bytes.Buffer
	result.Write(pngSignature)
	for _, chunk := range chunks {
		result.Write(chunk)
	}
	return result.Bytes()
}

func encodedPNGChunk(chunkType string, payload []byte) []byte {
	var result bytes.Buffer
	_ = binary.Write(&result, binary.BigEndian, uint32(len(payload)))
	result.WriteString(chunkType)
	result.Write(payload)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write([]byte(chunkType))
	_, _ = checksum.Write(payload)
	_ = binary.Write(&result, binary.BigEndian, checksum.Sum32())
	return result.Bytes()
}
