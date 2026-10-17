package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestDecodePublicationAssetOutcomes(t *testing.T) {
	size := int64(12)
	valid := PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion,
		Cover:   PublicationCoverOutcome{State: PublicationAssetEmbedded, SHA256: testAssetDigest("cover"), MIME: "image/jpeg", SizeBytes: &size},
		Lyrics:  PublicationLyricsOutcome{State: PublicationAssetAbsent},
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePublicationAssetOutcomes(encoded)
	if err != nil {
		t.Fatal(err)
	}
	*decoded.Cover.SizeBytes = 99
	if *valid.Cover.SizeBytes != 12 {
		t.Fatal("decoded outcomes share pointer storage")
	}

	for _, raw := range []string{
		`{"version":0,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`,
		`{"version":1,"cover":{"state":"absent","size_bytes":0},"lyrics":{"state":"absent"}}`,
		`{"version":1,"cover":{"state":"embedded","sha256":"bad","mime":"image/jpeg","size_bytes":1},"lyrics":{"state":"absent"}}`,
		`{"version":1,"cover":{"state":"embedded","sha256":"` + testAssetDigest("cover") + `","mime":"text/plain","size_bytes":1},"lyrics":{"state":"absent"}}`,
		`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"embedded","sha256":"` + testAssetDigest("lyrics") + `","format":"txt"}}`,
		`{"version":1,"cover":{"state":"absent","unknown":true},"lyrics":{"state":"absent"}}`,
		`{"version":1,"cover":null,"lyrics":{"state":"absent"}}`,
		`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}} {}`,
	} {
		if _, err := DecodePublicationAssetOutcomes(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted invalid outcomes: %s", raw)
		}
	}
}

func TestPublicationAssetOutcomesValidateAgainstExactSyncedLyrics(t *testing.T) {
	lyrics := "[ar:Artist]\n[00:01.20]line\n"
	outcomes := PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion,
		Cover:   PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics:  PublicationLyricsOutcome{State: PublicationAssetEmbedded, SHA256: testAssetDigest(lyrics), Format: "lrc"},
	}
	if err := outcomes.ValidateAgainstMetadata(map[string][]string{"LYRICS": {lyrics}}, nil); err != nil {
		t.Fatalf("valid lyrics rejected: %v", err)
	}

	unicodeCRLF := "[ar:音楽家]\r\n[00:01.20]歌\r\n"
	unicodeOutcomes := outcomes
	unicodeOutcomes.Lyrics.SHA256 = testAssetDigest(unicodeCRLF)
	if err := unicodeOutcomes.ValidateAgainstMetadata(map[string][]string{"LYRICS": {unicodeCRLF}}, nil); err != nil {
		t.Fatalf("valid Unicode CRLF lyrics rejected: %v", err)
	}

	for _, tags := range []map[string][]string{
		{"LYRICS": {"[ar:Artist]\n[00:01.20]line"}}, // newline normalization changes digest
		{"LYRICS": {lyrics, lyrics}},
		{"LYRICS": {"not synced lyrics"}},
	} {
		if err := outcomes.ValidateAgainstMetadata(tags, nil); err == nil {
			t.Errorf("accepted mismatched or invalid metadata: %#v", tags)
		}
	}
	for _, invalid := range []string{"", " \t\r\n"} {
		invalidOutcomes := outcomes
		invalidOutcomes.Lyrics.SHA256 = testAssetDigest(invalid)
		if err := invalidOutcomes.ValidateAgainstMetadata(map[string][]string{"LYRICS": {invalid}}, nil); err == nil {
			t.Errorf("accepted empty synced lyrics %q with matching digest", invalid)
		}
	}
	invalidUTF8 := string([]byte{'[', '0', '0', ':', '0', '1', '.', '2', '0', ']', 0xff})
	invalidUTF8Outcomes := outcomes
	invalidUTF8Outcomes.Lyrics.SHA256 = testAssetDigest(invalidUTF8)
	if err := invalidUTF8Outcomes.ValidateAgainstMetadata(map[string][]string{"LYRICS": {invalidUTF8}}, nil); err == nil {
		t.Fatal("accepted invalid UTF-8 timestamped lyrics with matching digest")
	}
	if err := outcomes.ValidateAgainstMetadata(map[string][]string{"LYRICS": {lyrics}}, []byte("different")); err == nil {
		t.Fatal("accepted lyric bytes that differ from read-back metadata")
	}

	absent := PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion,
		Cover:   PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics:  PublicationLyricsOutcome{State: PublicationAssetAbsent},
	}
	if err := absent.ValidateAgainstMetadata(map[string][]string{"LYRICS": {""}}, nil); err != nil {
		t.Fatalf("empty absent lyrics metadata rejected: %v", err)
	}
	if err := absent.ValidateAgainstMetadata(map[string][]string{"LYRICS": {" "}}, nil); err == nil {
		t.Fatal("accepted non-empty lyrics for absent outcome")
	}
}

func testAssetDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
