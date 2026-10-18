package metadata

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.senan.xyz/taglib"
)

func TestWritePreservingFlacArtworkRoundTripsNativePicture(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "eg.flac"))
	if err != nil {
		t.Fatal(err)
	}
	fixture = removeFlacTestPictures(fixture)
	imageBytes := generatedPNG(t)
	body := flacTestPicture(3, "image/png", "正面の絵🎵", 123, 456, 7, 89, imageBytes)
	path := writeMetadataFixture(t, ".flac", addFlacTestPicture(fixture, body))

	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := SnapshotFlacArtwork(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if before.count != 1 || before.pictureType != 3 || before.description != "正面の絵🎵" || before.mime != "image/png" {
		t.Fatalf("native artwork audit = %#v", before)
	}

	capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	if inspection := capture.CoverInspection; inspection == nil || !inspection.Observed || inspection.Cover == nil || inspection.Cover.MIME != "image/png" || !bytes.Equal(inspection.Cover.Data, imageBytes) {
		t.Fatalf("TagLib reader cover inspection = %#v", capture.CoverInspection)
	}

	wantTags := map[string][]string{
		taglib.Title:  {"題名 – 雪と音楽 🎶"},
		taglib.Lyrics: {"一行目\nSecond line – 🎵\n"},
	}
	if err := WritePreservingFlacArtwork(context.Background(), path, wantTags, before); err != nil {
		t.Fatalf("WritePreservingFlacArtwork: %v", err)
	}
	after, err := SnapshotFlacArtwork(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if after.count != before.count || after.pictureHash != before.pictureHash || after.imageHash != before.imageHash || after.pictureType != before.pictureType || after.description != before.description || after.mime != before.mime || after.streamInfo != before.streamInfo || after.audioHash != before.audioHash {
		t.Fatalf("artwork/audio audit changed: before=%#v after=%#v", before, after)
	}
	gotTags, err := taglib.ReadTags(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, values := range wantTags {
		assertValues(t, gotTags, key, values)
	}
	gotBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(flacTestAudio(beforeBytes), flacTestAudio(gotBytes)) {
		t.Fatal("audio frames changed during tag write")
	}
}

func TestWritePreservingFlacArtworkRejectsReplacedIdenticalFile(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "eg.flac"))
	if err != nil {
		t.Fatal(err)
	}
	fixture = removeFlacTestPictures(fixture)
	path := writeMetadataFixture(t, ".flac", fixture)
	before, err := SnapshotFlacArtwork(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	oldPath := path + ".old"
	if err := os.Rename(path, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	baseline, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = WritePreservingFlacArtwork(context.Background(), path, map[string][]string{taglib.Title: {"must not write"}}, before)
	if err == nil || !strings.Contains(err.Error(), "FLAC changed since artwork snapshot") {
		t.Fatalf("write did not reject replacement by identity: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, baseline) {
		t.Fatal("rejected replacement file was modified")
	}
}

func TestSnapshotFlacArtworkRejectsAmbiguousArtworkWithoutWriting(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "eg.flac"))
	if err != nil {
		t.Fatal(err)
	}
	fixture = removeFlacTestPictures(fixture)
	imageBytes := generatedPNG(t)
	picture := flacTestPicture(3, "image/png", "cover", 123, 456, 7, 89, imageBytes)
	for _, test := range []struct {
		name           string
		file           []byte
		wantAuditError string
	}{
		{name: "duplicate native pictures", file: addFlacTestPicture(addFlacTestPicture(fixture, picture), picture), wantAuditError: "multiple FLAC PICTURE blocks"},
		{name: "mixed-case alternate Vorbis artwork", file: addFlacTestComment(addFlacTestPicture(fixture, picture), "cOvErArTmImE=image/png"), wantAuditError: "alternate FLAC artwork comment is unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Keep the proof and rejected content on the same path: the writer's
			// path guard passes, while its content-identity guard may reject first.
			path := writeMetadataFixture(t, ".flac", addFlacTestPicture(fixture, picture))
			before, err := SnapshotFlacArtwork(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, test.file, 0o600); err != nil {
				t.Fatal(err)
			}
			baseline, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := SnapshotFlacArtwork(context.Background(), path); err == nil || !strings.Contains(err.Error(), test.wantAuditError) {
				t.Fatal("ambiguous artwork passed the pre-write audit")
			}
			if err := WritePreservingFlacArtwork(context.Background(), path, map[string][]string{taglib.Title: {"must not write"}}, before); err == nil {
				t.Fatal("write accepted artwork that failed the baseline audit")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, baseline) {
				t.Fatal("rejected artwork file was modified")
			}
		})
	}
}

func flacTestPicture(typ uint32, mime, description string, width, height, depth, colors uint32, image []byte) []byte {
	var body []byte
	append32 := func(value uint32) {
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], value)
		body = append(body, raw[:]...)
	}
	append32(typ)
	append32(uint32(len(mime)))
	body = append(body, mime...)
	append32(uint32(len(description)))
	body = append(body, description...)
	append32(width)
	append32(height)
	append32(depth)
	append32(colors)
	append32(uint32(len(image)))
	return append(body, image...)
}

// addFlacTestPicture adds native metadata structurally to the real fixture; it
// does not generate or decode audio, and is only used by these tests.
func addFlacTestPicture(file, picture []byte) []byte {
	return insertFlacTestBlock(file, 6, picture)
}

func addFlacTestComment(file []byte, field string) []byte {
	comment := make([]byte, 0, 16+len(field))
	appendLength := func(length uint32) {
		var raw [4]byte
		binary.LittleEndian.PutUint32(raw[:], length)
		comment = append(comment, raw[:]...)
	}
	appendLength(uint32(len("fixture")))
	comment = append(comment, "fixture"...)
	appendLength(1)
	appendLength(uint32(len(field)))
	comment = append(comment, field...)
	return replaceFlacTestComment(file, comment)
}

func replaceFlacTestComment(file, comment []byte) []byte {
	metadataEnd := 4
	for {
		head := file[metadataEnd]
		size := int(file[metadataEnd+1])<<16 | int(file[metadataEnd+2])<<8 | int(file[metadataEnd+3])
		end := metadataEnd + 4 + size
		if head&0x7f == 4 {
			result := append([]byte(nil), file[:metadataEnd]...)
			result = append(result, head, byte(len(comment)>>16), byte(len(comment)>>8), byte(len(comment)))
			result = append(result, comment...)
			return append(result, file[end:]...)
		}
		metadataEnd = end
		if head&0x80 != 0 {
			return insertFlacTestBlock(file, 4, comment)
		}
	}
}

func insertFlacTestBlock(file []byte, typ byte, body []byte) []byte {
	metadataEnd := 4
	lastHeader := 0
	for {
		lastHeader = metadataEnd
		head := file[metadataEnd]
		size := int(file[metadataEnd+1])<<16 | int(file[metadataEnd+2])<<8 | int(file[metadataEnd+3])
		metadataEnd += 4 + size
		if head&0x80 != 0 {
			break
		}
	}
	result := append([]byte(nil), file[:metadataEnd]...)
	result[lastHeader] &= 0x7f
	block := []byte{0x80 | typ, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	result = append(result, block...)
	result = append(result, body...)
	return append(result, file[metadataEnd:]...)
}

func removeFlacTestPictures(file []byte) []byte {
	type metadataBlock struct {
		typ  byte
		data []byte
	}
	var blocks []metadataBlock
	metadataEnd := 4
	for {
		head := file[metadataEnd]
		size := int(file[metadataEnd+1])<<16 | int(file[metadataEnd+2])<<8 | int(file[metadataEnd+3])
		end := metadataEnd + 4 + size
		if head&0x7f != 6 {
			blocks = append(blocks, metadataBlock{typ: head & 0x7f, data: file[metadataEnd+4 : end]})
		}
		metadataEnd = end
		if head&0x80 != 0 {
			break
		}
	}
	result := append([]byte(nil), file[:4]...)
	for index, block := range blocks {
		head := block.typ
		if index == len(blocks)-1 {
			head |= 0x80
		}
		data := block.data
		result = append(result, head, byte(len(data)>>16), byte(len(data)>>8), byte(len(data)))
		result = append(result, data...)
	}
	return append(result, file[metadataEnd:]...)
}

func flacTestAudio(file []byte) []byte {
	metadataEnd := 4
	for {
		head := file[metadataEnd]
		size := int(file[metadataEnd+1])<<16 | int(file[metadataEnd+2])<<8 | int(file[metadataEnd+3])
		metadataEnd += 4 + size
		if head&0x80 != 0 {
			return file[metadataEnd:]
		}
	}
}
