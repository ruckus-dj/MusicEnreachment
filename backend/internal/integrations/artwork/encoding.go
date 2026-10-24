package artwork

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
)

var (
	pngSignature = []byte{137, 80, 78, 71, 13, 10, 26, 10}
	pngIHDR      = []byte("IHDR")
	pngIDAT      = []byte("IDAT")
	pngIEND      = []byte("IEND")
	pngCRCTable  = crc32.MakeTable(crc32.IEEE)
)

// ValidateEncoding checks the encoded envelope of an image whose declared
// format is already known. PNG data is walked chunk-by-chunk: chunk lengths are
// bounds-checked before slicing, every chunk checksum is verified, and the
// stream must contain a 13-byte IHDR first, at least one IDAT, and a zero-length
// final IEND. No IDAT payload is decompressed and no pixel buffer is allocated.
// Other registered formats keep the existing header-only validation.
func ValidateEncoding(data []byte, format string) error {
	if format == "png" {
		return validatePNGEncoding(data)
	}
	config, actual, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || actual != format || config.Width <= 0 || config.Height <= 0 {
		return errors.New("artwork image header does not match its declared format")
	}
	return nil
}

func validatePNGEncoding(data []byte) error {
	if len(data) < len(pngSignature) || !bytes.Equal(data[:len(pngSignature)], pngSignature) {
		return errors.New("artwork PNG encoding has an invalid signature")
	}
	offset := len(pngSignature)
	chunks := 0
	hasIDAT := false
	hasIEND := false
	for offset < len(data) {
		if hasIEND {
			return errors.New("artwork PNG encoding contains data after IEND")
		}
		if len(data)-offset < 8 {
			return errors.New("artwork PNG encoding is truncated")
		}
		length := binary.BigEndian.Uint32(data[offset : offset+4])
		chunkType := data[offset+4 : offset+8]
		payloadStart := offset + 8
		// uint64 arithmetic keeps a 32-bit chunk length from overflowing the
		// remaining-bytes bound before any payload or checksum is sliced.
		if uint64(length)+4 > uint64(len(data)-payloadStart) {
			return errors.New("artwork PNG encoding is truncated")
		}
		payloadEnd := payloadStart + int(length)
		expected := binary.BigEndian.Uint32(data[payloadEnd : payloadEnd+4])
		checksum := crc32.Update(0, pngCRCTable, chunkType)
		checksum = crc32.Update(checksum, pngCRCTable, data[payloadStart:payloadEnd])
		if checksum != expected {
			return errors.New("artwork PNG encoding has an invalid chunk checksum")
		}
		if chunks == 0 {
			if !bytes.Equal(chunkType, pngIHDR) || length != 13 {
				return errors.New("artwork PNG encoding must start with a 13-byte IHDR chunk")
			}
		} else if bytes.Equal(chunkType, pngIHDR) {
			return errors.New("artwork PNG encoding contains a misplaced IHDR chunk")
		}
		chunks++
		if bytes.Equal(chunkType, pngIDAT) {
			hasIDAT = true
		}
		if bytes.Equal(chunkType, pngIEND) {
			if length != 0 {
				return errors.New("artwork PNG encoding has a non-empty IEND chunk")
			}
			hasIEND = true
		}
		offset = payloadEnd + 4
	}
	if !hasIDAT {
		return errors.New("artwork PNG encoding has no IDAT chunk")
	}
	if !hasIEND {
		return errors.New("artwork PNG encoding has no IEND chunk")
	}
	return nil
}
