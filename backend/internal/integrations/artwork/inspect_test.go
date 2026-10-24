package artwork

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func TestInspectPNGDimensionBoundsFromHeader(t *testing.T) {
	for _, test := range []struct {
		name    string
		width   uint32
		height  uint32
		wantErr bool
	}{
		{name: "maximum accepted", width: 10000, height: 10000},
		{name: "width too large", width: 10001, height: 1, wantErr: true},
		{name: "height too large", width: 1, height: 10001, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			format, width, height, err := Inspect(pngHeader(test.width, test.height))
			if (err != nil) != test.wantErr {
				t.Fatalf("Inspect() error = %v, wantErr %t", err, test.wantErr)
			}
			if err == nil && (format != "png" || width != int(test.width) || height != int(test.height)) {
				t.Fatalf("Inspect() = %q, %d x %d", format, width, height)
			}
		})
	}
}

func pngHeader(width, height uint32) []byte {
	var result bytes.Buffer
	result.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, width)
	_ = binary.Write(&chunk, binary.BigEndian, height)
	chunk.Write([]byte{8, 2, 0, 0, 0})
	_ = binary.Write(&result, binary.BigEndian, uint32(chunk.Len()))
	result.WriteString("IHDR")
	result.Write(chunk.Bytes())
	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(chunk.Bytes())
	_ = binary.Write(&result, binary.BigEndian, crc.Sum32())
	return result.Bytes()
}
