package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// These bounded fixture parsers return only encoded audio sample/frame payloads;
// container metadata and artwork are deliberately excluded from the digest.
func encodedAudioPayload(extension string, data []byte) ([]byte, error) {
	switch extension {
	case ".flac":
		offset, err := flacTestAudioOffset(data)
		if err != nil {
			return nil, err
		}
		return data[offset:], nil
	case ".mp3":
		return mp3TestAudioFrames(data)
	case ".m4a":
		return mp4TestAudioSamples(data)
	case ".mka":
		return matroskaTestAudioBlocks(data)
	default:
		return nil, fmt.Errorf("unsupported audio fixture extension %q", extension)
	}
}

func firstEncodedAudioByteOffset(extension string, data []byte) (int, error) {
	switch extension {
	case ".flac":
		return flacTestAudioOffset(data)
	case ".mp3":
		return mp3TestFirstFramePayload(data)
	case ".m4a":
		_, offsets, err := mp4TestAudioSamplesAndOffsets(data)
		if err != nil || len(offsets) == 0 {
			return 0, errors.New("M4A has no audio sample")
		}
		return offsets[0], nil
	case ".mka":
		_, offsets, err := matroskaTestAudioBlocksAndOffsets(data)
		if err != nil || len(offsets) == 0 {
			return 0, errors.New("MKA has no audio block payload")
		}
		return offsets[0], nil
	default:
		return 0, fmt.Errorf("unsupported audio fixture extension %q", extension)
	}
}

func flacTestAudioOffset(data []byte) (int, error) {
	if len(data) < 8 || !bytes.Equal(data[:4], []byte("fLaC")) {
		return 0, errors.New("not a FLAC stream")
	}
	for offset := 4; ; {
		if len(data)-offset < 4 {
			return 0, errors.New("truncated FLAC metadata header")
		}
		header := data[offset]
		size := int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		offset += 4
		if size > len(data)-offset {
			return 0, errors.New("truncated FLAC metadata block")
		}
		offset += size
		if header&0x80 != 0 {
			if offset >= len(data) {
				return 0, errors.New("FLAC contains no encoded audio")
			}
			return offset, nil
		}
	}
}

func mp3TestAudioFrames(data []byte) ([]byte, error) {
	payload, _, err := mp3TestAudioFramesAndOffset(data)
	return payload, err
}

func mp3TestAudioFramesAndOffset(data []byte) ([]byte, int, error) {
	start, err := mp3TestAudioStart(data)
	if err != nil {
		return nil, 0, err
	}
	end, err := mp3TestAudioEnd(data, start)
	if err != nil {
		return nil, 0, err
	}
	var payload []byte
	firstPayloadOffset := 0
	var expectedVersion, expectedLayer, expectedSampleRate int
	for offset := start; offset < end; {
		if end-offset < 4 {
			return nil, 0, errors.New("truncated MPEG audio frame header")
		}
		frameSize, valid := mp3TestFrameSize(data[offset:])
		if !valid {
			return nil, 0, fmt.Errorf("invalid MPEG audio frame at byte %d", offset)
		}
		if frameSize > end-offset {
			return nil, 0, errors.New("truncated MPEG audio frame")
		}
		version := int((data[offset+1] >> 3) & 3)
		layer := int((data[offset+1] >> 1) & 3)
		sampleRate := int((data[offset+2] >> 2) & 3)
		if len(payload) == 0 {
			expectedVersion, expectedLayer, expectedSampleRate = version, layer, sampleRate
			firstPayloadOffset = offset + 4
			if data[offset+1]&1 == 0 {
				firstPayloadOffset += 2 // CRC follows the MPEG header.
			}
		} else if version != expectedVersion || layer != expectedLayer || sampleRate != expectedSampleRate {
			return nil, 0, errors.New("MP3 fixture changes MPEG frame format midstream")
		}
		if firstPayloadOffset >= offset+frameSize {
			return nil, 0, errors.New("MPEG audio frame has no encoded payload")
		}
		payload = append(payload, data[offset:offset+frameSize]...)
		offset += frameSize
	}
	if len(payload) == 0 {
		return nil, 0, errors.New("MP3 contains no MPEG audio frames")
	}
	return payload, firstPayloadOffset, nil
}

func mp3TestFirstFramePayload(data []byte) (int, error) {
	_, offset, err := mp3TestAudioFramesAndOffset(data)
	return offset, err
}

func mp3TestAudioStart(data []byte) (int, error) {
	if len(data) < 3 || string(data[:3]) != "ID3" {
		return 0, nil
	}
	if len(data) < 10 || data[3] < 2 || data[3] > 4 || data[4] == 0xff {
		return 0, errors.New("invalid ID3v2 header")
	}
	sizeBytes := data[6:10]
	for _, b := range sizeBytes {
		if b&0x80 != 0 {
			return 0, errors.New("invalid ID3v2 synchsafe size")
		}
	}
	size := int(sizeBytes[0])<<21 | int(sizeBytes[1])<<14 | int(sizeBytes[2])<<7 | int(sizeBytes[3])
	end := 10 + size
	if end > len(data) {
		return 0, errors.New("ID3v2 tag exceeds MP3 file")
	}
	if data[5]&0x10 != 0 && (data[3] != 4 || size < 10 || string(data[end-10:end-7]) != "3DI") {
		return 0, errors.New("invalid ID3v2.4 footer")
	}
	return end, nil
}

func mp3TestAudioEnd(data []byte, start int) (int, error) {
	end := len(data)
	if end-start >= 128 && string(data[end-128:end-125]) == "TAG" {
		end -= 128
	}
	if end-start >= 32 && string(data[end-32:end-24]) == "APETAGEX" {
		footer := data[end-32 : end]
		version := binary.LittleEndian.Uint32(footer[8:12])
		size := binary.LittleEndian.Uint32(footer[12:16])
		if (version != 1000 && version != 2000) || size < 32 || uint64(size) > uint64(end-start) {
			return 0, errors.New("invalid APEv2 footer")
		}
		end -= int(size)
		if end-start >= 32 && string(data[end-32:end-24]) == "APETAGEX" {
			end -= 32 // Optional APEv2 header, outside the footer-declared size.
		}
	}
	if end <= start {
		return 0, errors.New("MP3 contains no audio region")
	}
	return end, nil
}

func mp3TestFrameSize(frame []byte) (int, bool) {
	if len(frame) < 4 || frame[0] != 0xff || frame[1]&0xe0 != 0xe0 {
		return 0, false
	}
	version := (frame[1] >> 3) & 3
	layer := (frame[1] >> 1) & 3
	bitrateIndex := frame[2] >> 4
	sampleIndex := (frame[2] >> 2) & 3
	if version == 1 || layer == 0 || bitrateIndex == 0 || bitrateIndex == 15 || sampleIndex == 3 {
		return 0, false
	}
	// The checked-in MP3 fixture is MPEG-1 Layer III. Reject other grammars
	// instead of guessing a frame boundary in a test preservation check.
	if version != 3 || layer != 1 {
		return 0, false
	}
	bitrates := [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	sampleRates := [...]int{44100, 48000, 32000}
	bitrate := bitrates[bitrateIndex] * 1000
	return 144*bitrate/sampleRates[sampleIndex] + int((frame[2]>>1)&1), true
}

func TestMP3TestAudioExtractorExcludesID3PayloadAndTrailingTags(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "eg.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	wantFrames, _, err := mp3TestAudioFramesAndOffset(fixture)
	if err != nil {
		t.Fatalf("extract source fixture frames: %v", err)
	}
	frameHeader := wantFrames[:4]
	// The fake frame sync is deliberately inside declared ID3v2 data. A second
	// sync is embedded in ID3v1 text at EOF; neither is an audio frame.
	id3Payload := append([]byte("artwork/text:"), frameHeader...)
	id3Payload = append(id3Payload, []byte(":TAG:metadata")...)
	var adversarial []byte
	adversarial = append(adversarial, []byte("ID3\x04\x00\x00")...)
	adversarial = append(adversarial, byte(len(id3Payload)>>21), byte(len(id3Payload)>>14)&0x7f, byte(len(id3Payload)>>7)&0x7f, byte(len(id3Payload))&0x7f)
	adversarial = append(adversarial, id3Payload...)
	audioOffset := len(adversarial)
	adversarial = append(adversarial, wantFrames...)
	id3v1 := make([]byte, 128)
	copy(id3v1, "TAG")
	copy(id3v1[3:], "tag text with fake sync ")
	copy(id3v1[32:], frameHeader)
	adversarial = append(adversarial, id3v1...)

	got, firstPayload, err := mp3TestAudioFramesAndOffset(adversarial)
	if err != nil {
		t.Fatalf("extract adversarial MP3: %v", err)
	}
	if !bytes.Equal(got, wantFrames) {
		t.Fatalf("ID3 bytes affected extracted frames: got %d bytes, want %d", len(got), len(wantFrames))
	}
	if firstPayload < audioOffset || firstPayload >= audioOffset+len(wantFrames) {
		t.Fatalf("first-frame corruption offset %d points outside encoded frames [%d,%d)", firstPayload, audioOffset, audioOffset+len(wantFrames))
	}
	before := sha256.Sum256(got)
	corrupted := append([]byte(nil), adversarial...)
	corrupted[firstPayload] ^= 0x01
	changed, _, err := mp3TestAudioFramesAndOffset(corrupted)
	if err != nil {
		t.Fatalf("extract corrupted audio: %v", err)
	}
	if after := sha256.Sum256(changed); after == before {
		t.Fatal("actual MPEG frame payload corruption did not change the extracted audio digest")
	}
}

type mp4TestBox struct {
	typ  string
	body []byte
	base int
}

func mp4TestBoxes(data []byte, absoluteBase int) ([]mp4TestBox, error) {
	var boxes []mp4TestBox
	for offset := 0; offset < len(data); {
		if len(data)-offset < 8 {
			return nil, errors.New("truncated ISO BMFF box header")
		}
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		header := 8
		switch size {
		case 1:
			if len(data)-offset < 16 {
				return nil, errors.New("truncated extended ISO BMFF box header")
			}
			size = binary.BigEndian.Uint64(data[offset+8:])
			header = 16
		case 0:
			size = uint64(len(data) - offset)
		}
		if size < uint64(header) || size > uint64(len(data)-offset) {
			return nil, errors.New("invalid ISO BMFF box size")
		}
		end := offset + int(size)
		boxes = append(boxes, mp4TestBox{typ: string(data[offset+4 : offset+8]), body: data[offset+header : end], base: absoluteBase + offset + header})
		offset = end
	}
	return boxes, nil
}

func mp4TestChildren(data []byte, base int) (map[string][]mp4TestBox, error) {
	boxes, err := mp4TestBoxes(data, base)
	if err != nil {
		return nil, err
	}
	children := make(map[string][]mp4TestBox, len(boxes))
	for _, box := range boxes {
		children[box.typ] = append(children[box.typ], box)
	}
	return children, nil
}

func mp4TestAudioSamples(data []byte) ([]byte, error) {
	payload, _, err := mp4TestAudioSamplesAndOffsets(data)
	return payload, err
}

func mp4TestAudioSamplesAndOffsets(data []byte) ([]byte, []int, error) {
	root, err := mp4TestChildren(data, 0)
	if err != nil {
		return nil, nil, err
	}
	if len(root["moov"]) != 1 {
		return nil, nil, errors.New("M4A must contain one movie box")
	}
	moov, err := mp4TestChildren(root["moov"][0].body, root["moov"][0].base)
	if err != nil {
		return nil, nil, err
	}
	var audioTables map[string][]mp4TestBox
	for _, trak := range moov["trak"] {
		track, err := mp4TestChildren(trak.body, trak.base)
		if err != nil {
			return nil, nil, err
		}
		if len(track["mdia"]) != 1 {
			continue
		}
		media, err := mp4TestChildren(track["mdia"][0].body, track["mdia"][0].base)
		if err != nil {
			return nil, nil, err
		}
		if len(media["hdlr"]) != 1 || len(media["hdlr"][0].body) < 12 || string(media["hdlr"][0].body[8:12]) != "soun" {
			continue
		}
		if len(media["minf"]) != 1 {
			return nil, nil, errors.New("M4A audio track has no media information")
		}
		minf, err := mp4TestChildren(media["minf"][0].body, media["minf"][0].base)
		if err != nil || len(minf["stbl"]) != 1 {
			return nil, nil, errors.New("M4A audio track has invalid sample table")
		}
		audioTables, err = mp4TestChildren(minf["stbl"][0].body, minf["stbl"][0].base)
		if err != nil {
			return nil, nil, err
		}
		break
	}
	if audioTables == nil {
		return nil, nil, errors.New("M4A has no audio track")
	}
	if len(audioTables["stsz"]) != 1 || (len(audioTables["stco"])+len(audioTables["co64"]) != 1) || len(audioTables["stsc"]) != 1 {
		return nil, nil, errors.New("M4A audio sample table is incomplete or ambiguous")
	}
	sizes, err := mp4TestSampleSizes(audioTables["stsz"][0].body)
	if err != nil {
		return nil, nil, err
	}
	var chunks []uint64
	if len(audioTables["stco"]) == 1 {
		chunks, err = mp4TestChunkOffsets(audioTables["stco"][0].body, false)
	} else {
		chunks, err = mp4TestChunkOffsets(audioTables["co64"][0].body, true)
	}
	if err != nil {
		return nil, nil, err
	}
	chunkMap, err := mp4TestSampleToChunk(audioTables["stsc"][0].body)
	if err != nil {
		return nil, nil, err
	}
	var payload []byte
	var offsets []int
	sampleIndex, mapIndex := 0, 0
	for chunkIndex, chunkOffset := range chunks {
		for mapIndex+1 < len(chunkMap) && chunkMap[mapIndex+1].firstChunk <= uint32(chunkIndex+1) {
			mapIndex++
		}
		if len(chunkMap) == 0 || chunkMap[mapIndex].firstChunk > uint32(chunkIndex+1) {
			return nil, nil, errors.New("M4A chunk has no sample mapping")
		}
		position := chunkOffset
		for n := uint32(0); n < chunkMap[mapIndex].samplesPerChunk; n++ {
			if sampleIndex >= len(sizes) || position > uint64(len(data)) || uint64(sizes[sampleIndex]) > uint64(len(data))-position {
				return nil, nil, errors.New("M4A sample lies outside file")
			}
			offsets = append(offsets, int(position))
			payload = append(payload, data[position:position+uint64(sizes[sampleIndex])]...)
			position += uint64(sizes[sampleIndex])
			sampleIndex++
		}
	}
	if sampleIndex != len(sizes) || len(payload) == 0 {
		return nil, nil, errors.New("M4A sample table does not account for every audio sample")
	}
	return payload, offsets, nil
}

func mp4TestSampleSizes(body []byte) ([]uint32, error) {
	if len(body) < 12 {
		return nil, errors.New("truncated M4A sample-size box")
	}
	sampleSize, count := binary.BigEndian.Uint32(body[4:8]), binary.BigEndian.Uint32(body[8:12])
	if uint64(count) > uint64(len(body))/4 {
		return nil, errors.New("invalid M4A sample count")
	}
	sizes := make([]uint32, count)
	if sampleSize != 0 {
		for i := range sizes {
			sizes[i] = sampleSize
		}
		return sizes, nil
	}
	if uint64(count)*4 != uint64(len(body)-12) {
		return nil, errors.New("invalid M4A sample-size entries")
	}
	for i := range sizes {
		sizes[i] = binary.BigEndian.Uint32(body[12+i*4:])
	}
	return sizes, nil
}

func mp4TestChunkOffsets(body []byte, wide bool) ([]uint64, error) {
	if len(body) < 8 {
		return nil, errors.New("truncated M4A chunk-offset box")
	}
	count := binary.BigEndian.Uint32(body[4:8])
	width := 4
	if wide {
		width = 8
	}
	if uint64(count)*uint64(width) != uint64(len(body)-8) {
		return nil, errors.New("invalid M4A chunk-offset entries")
	}
	chunks := make([]uint64, count)
	for i := range chunks {
		if wide {
			chunks[i] = binary.BigEndian.Uint64(body[8+i*width:])
		} else {
			chunks[i] = uint64(binary.BigEndian.Uint32(body[8+i*width:]))
		}
	}
	return chunks, nil
}

type mp4TestChunkMap struct{ firstChunk, samplesPerChunk uint32 }

func mp4TestSampleToChunk(body []byte) ([]mp4TestChunkMap, error) {
	if len(body) < 8 {
		return nil, errors.New("truncated M4A sample-to-chunk box")
	}
	count := binary.BigEndian.Uint32(body[4:8])
	if uint64(count)*12 != uint64(len(body)-8) || count == 0 {
		return nil, errors.New("invalid M4A sample-to-chunk entries")
	}
	entries := make([]mp4TestChunkMap, count)
	for i := range entries {
		p := body[8+i*12:]
		entries[i] = mp4TestChunkMap{firstChunk: binary.BigEndian.Uint32(p), samplesPerChunk: binary.BigEndian.Uint32(p[4:])}
		if entries[i].firstChunk == 0 || entries[i].samplesPerChunk == 0 || (i > 0 && entries[i-1].firstChunk >= entries[i].firstChunk) {
			return nil, errors.New("invalid M4A sample-to-chunk mapping")
		}
	}
	return entries, nil
}

const (
	matroskaTestTrackEntry  = 0xae
	matroskaTestTrackNumber = 0xd7
	matroskaTestTrackType   = 0x83
	matroskaTestSimpleBlock = 0xa3
	matroskaTestBlockGroup  = 0xa0
	matroskaTestBlock       = 0xa1
)

type matroskaTestElement struct {
	id    uint64
	data  []byte
	start int
}

func matroskaTestVint(data []byte, offset int, keepMarker bool) (uint64, int, error) {
	if offset >= len(data) || data[offset] == 0 {
		return 0, 0, errors.New("invalid EBML variable integer")
	}
	mask := byte(0x80)
	length := 1
	for data[offset]&mask == 0 {
		mask >>= 1
		length++
		if length > 8 {
			return 0, 0, errors.New("invalid EBML variable integer length")
		}
	}
	if length > len(data)-offset {
		return 0, 0, errors.New("truncated EBML variable integer")
	}
	value := uint64(data[offset])
	if !keepMarker {
		value &= uint64(mask - 1)
	}
	for i := 1; i < length; i++ {
		value = value<<8 | uint64(data[offset+i])
	}
	return value, length, nil
}

func matroskaTestElements(data []byte, base int) ([]matroskaTestElement, error) {
	var elements []matroskaTestElement
	for offset := 0; offset < len(data); {
		id, idLength, err := matroskaTestVint(data, offset, true)
		if err != nil {
			return nil, err
		}
		size, sizeLength, err := matroskaTestVint(data, offset+idLength, false)
		if err != nil {
			return nil, err
		}
		start := offset + idLength + sizeLength
		if size > uint64(len(data)-start) {
			return nil, errors.New("EBML element exceeds containing element")
		}
		end := start + int(size)
		elements = append(elements, matroskaTestElement{id: id, data: data[start:end], start: base + start})
		offset = end
	}
	return elements, nil
}

func matroskaTestAudioBlocks(data []byte) ([]byte, error) {
	payload, _, err := matroskaTestAudioBlocksAndOffsets(data)
	return payload, err
}

func matroskaTestAudioBlocksAndOffsets(data []byte) ([]byte, []int, error) {
	root, err := matroskaTestElements(data, 0)
	if err != nil {
		return nil, nil, err
	}
	var segment *matroskaTestElement
	for i := range root {
		if root[i].id == 0x18538067 {
			segment = &root[i]
			break
		}
	}
	if segment == nil {
		return nil, nil, errors.New("MKA has no Segment")
	}
	children, err := matroskaTestElements(segment.data, segment.start)
	if err != nil {
		return nil, nil, err
	}
	trackNumbers := make(map[uint64]bool)
	for _, child := range children {
		if child.id != 0x1654ae6b {
			continue
		}
		entries, err := matroskaTestElements(child.data, child.start)
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range entries {
			if entry.id != matroskaTestTrackEntry {
				continue
			}
			fields, err := matroskaTestElements(entry.data, entry.start)
			if err != nil {
				return nil, nil, err
			}
			var number uint64
			var isAudio bool
			for _, field := range fields {
				switch field.id {
				case matroskaTestTrackNumber:
					number = matroskaTestUnsigned(field.data)
				case matroskaTestTrackType:
					isAudio = matroskaTestUnsigned(field.data) == 2
				}
			}
			if isAudio {
				if number == 0 || trackNumbers[number] {
					return nil, nil, errors.New("MKA has invalid or duplicate audio track number")
				}
				trackNumbers[number] = true
			}
		}
	}
	if len(trackNumbers) != 1 {
		return nil, nil, fmt.Errorf("MKA fixture has %d audio tracks; expected exactly one", len(trackNumbers))
	}
	var payload []byte
	var offsets []int
	for _, child := range children {
		if child.id != 0x1f43b675 {
			continue
		}
		cluster, err := matroskaTestElements(child.data, child.start)
		if err != nil {
			return nil, nil, err
		}
		for _, element := range cluster {
			blocks := []matroskaTestElement{element}
			if element.id == matroskaTestBlockGroup {
				blocks, err = matroskaTestElements(element.data, element.start)
				if err != nil {
					return nil, nil, err
				}
			}
			for _, block := range blocks {
				if block.id != matroskaTestSimpleBlock && block.id != matroskaTestBlock {
					continue
				}
				track, trackLength, err := matroskaTestVint(block.data, 0, false)
				if err != nil || len(block.data)-trackLength < 3 {
					return nil, nil, errors.New("invalid Matroska block header")
				}
				if !trackNumbers[track] {
					continue
				}
				payloadStart := trackLength + 3
				if payloadStart >= len(block.data) {
					return nil, nil, errors.New("Matroska audio block has no frame payload")
				}
				payload = append(payload, block.data[payloadStart:]...)
				offsets = append(offsets, block.start+payloadStart)
			}
		}
	}
	if len(payload) == 0 {
		return nil, nil, errors.New("MKA has no encoded audio block payload")
	}
	return payload, offsets, nil
}

func matroskaTestUnsigned(data []byte) uint64 {
	var value uint64
	for _, b := range data {
		value = value<<8 | uint64(b)
	}
	return value
}
