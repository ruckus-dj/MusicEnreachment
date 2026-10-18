package metadata

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestParseFLACPictureBoundsAndDigest(t *testing.T) {
	b := make([]byte, 0)
	append32 := func(v uint32) { var raw [4]byte; binary.BigEndian.PutUint32(raw[:], v); b = append(b, raw[:]...) }
	append32(3) // front cover
	append32(10)
	b = append(b, "image/jpeg"...)
	append32(4)
	b = append(b, "test"...)
	for range 4 {
		append32(1)
	}
	append32(3)
	b = append(b, 1, 2, 3)
	got, err := parseFLACPicture(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.typ != 3 || got.mime != "image/jpeg" || got.description != "test" {
		t.Fatalf("picture = %#v", got)
	}
	for _, malformed := range [][]byte{nil, b[:len(b)-1], append(append([]byte(nil), b...), 0)} {
		if _, err := parseFLACPicture(malformed); err == nil {
			t.Fatalf("accepted malformed picture %x", malformed)
		}
	}
	for _, typ := range []uint32{0, 20, 127} {
		candidate := append([]byte(nil), b...)
		binary.BigEndian.PutUint32(candidate[:4], typ)
		if _, err := parseFLACPicture(candidate); err == nil {
			t.Errorf("accepted picture type %d", typ)
		}
	}
	linked := append([]byte(nil), b...)
	binary.BigEndian.PutUint32(linked[4:8], 3)
	copy(linked[8:11], "-->")
	if _, err := parseFLACPicture(linked); err == nil {
		t.Fatal("accepted linked-image MIME")
	}
}

func TestHasPictureCommentCaseInsensitiveIncludingEmpty(t *testing.T) {
	for _, value := range []string{"METADATA_BLOCK_PICTURE", "CoverArt", "coverartmime"} {
		if !hasPictureComment(vorbisComment(value, "")) {
			t.Errorf("did not detect %q", value)
		}
	}
	if hasPictureComment(vorbisComment("TITLE", "ordinary coverart metadata_block_picture")) {
		t.Fatal("ordinary comment classified as artwork")
	}
	for name, malformed := range map[string][]byte{
		"length":       {1, 0},
		"invalid utf8": vorbisComment("TITLE", string([]byte{0xff})),
		"empty key":    vorbisComment("", "coverart"),
	} {
		if _, err := parseVorbisComments(malformed); err == nil {
			t.Errorf("accepted malformed %s", name)
		}
	}
}

func TestSnapshotRejectsMissingLastMetadataBlock(t *testing.T) {
	data := append([]byte("fLaC"), 0x00, 0x00, 0x00, 34)
	data = append(data, make([]byte, 34)...)
	path := filepath.Join(t.TempDir(), "truncated.flac")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotFlacArtwork(t.Context(), path); err == nil {
		t.Fatal("accepted stream missing its final metadata block")
	}
}

func TestFLACMetadataBudgetCountsEveryHeader(t *testing.T) {
	var budget uint64
	for range maxFLACMetadataBytes / 4 {
		var ok bool
		budget, ok = addFLACMetadataBudget(budget, 0)
		if !ok {
			t.Fatal("zero-length metadata header unexpectedly exceeded budget")
		}
	}
	if _, ok := addFLACMetadataBudget(budget, 0); ok {
		t.Fatal("accepted metadata header beyond the byte budget")
	}
}

func TestSnapshotRejectsReservedBlockType127(t *testing.T) {
	data := append([]byte("fLaC"), 0x00, 0x00, 0x00, 34)
	data = append(data, make([]byte, 34)...)
	data = append(data, 0xff, 0, 0, 0)
	path := filepath.Join(t.TempDir(), "reserved.flac")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotFlacArtwork(t.Context(), path); err == nil {
		t.Fatal("accepted reserved metadata block type 127")
	}
}

func vorbisComment(key, value string) []byte {
	var out []byte
	appendString := func(s string) {
		var n [4]byte
		binary.LittleEndian.PutUint32(n[:], uint32(len(s)))
		out = append(out, n[:]...)
		out = append(out, s...)
	}
	appendString("test vendor")
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], 1)
	out = append(out, count[:]...)
	appendString(key + "=" + value)
	return out
}

func TestWritePreservingFlacArtworkRejectsZeroSnapshot(t *testing.T) {
	if err := WritePreservingFlacArtwork(t.Context(), "some.flac", nil, FlacArtworkSnapshot{}); err == nil {
		t.Fatal("zero snapshot accepted")
	}
}
