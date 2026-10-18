package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestValidateManualLyricsObservation(t *testing.T) {
	lyrics := "[00:01.00]line"
	digest := sha256.Sum256([]byte(lyrics))
	hash := hex.EncodeToString(digest[:])
	assets := PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion,
		Cover:   PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics:  PublicationLyricsOutcome{State: PublicationAssetEmbedded, SHA256: hash, Format: "lrc"},
	}
	if err := validateManualLyricsObservation(ManualLyricsValue, &hash, ManualLyricsObserved{MetadataPresent: true, MetadataValue: lyrics, Assets: assets}); err != nil {
		t.Fatalf("matching value rejected: %v", err)
	}
	if err := validateManualLyricsObservation(ManualLyricsValue, &hash, ManualLyricsObserved{MetadataPresent: true, MetadataValue: "[00:02.00]other", Assets: assets}); err == nil {
		t.Fatal("mismatching metadata accepted")
	}
	emptyAssets := PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion,
		Cover:   PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics:  PublicationLyricsOutcome{State: PublicationAssetAbsent},
	}
	if err := validateManualLyricsObservation(ManualLyricsExplicitEmpty, nil, ManualLyricsObserved{Assets: emptyAssets}); err != nil {
		t.Fatalf("observed absence rejected: %v", err)
	}
	warning := manualLyricsReadbackMismatchWarning
	if err := validateManualLyricsReceiptObservation(ManualLyricsValue, &hash, &warning, ManualLyricsObserved{Assets: emptyAssets}); err == nil {
		t.Fatal("warning fulfilled a manual value without observed lyrics")
	}
	if err := validateManualLyricsReceiptObservation(ManualLyricsExplicitEmpty, nil, &warning, ManualLyricsObserved{Assets: emptyAssets}); err != nil {
		t.Fatalf("warning invalidated fulfilled removal: %v", err)
	}
	if err := validateManualLyricsObservation(ManualLyricsExplicitEmpty, nil, ManualLyricsObserved{MetadataPresent: true, MetadataValue: "text", Assets: assets}); err == nil {
		t.Fatal("explicit empty accepted embedded lyrics")
	}
}

func TestEncodeObservedManualLyricsMetadata(t *testing.T) {
	value, present, err := EncodeObservedManualLyricsMetadata([]byte(`{"version":1,"tags":{"LYRICS":["line"]}}`))
	if err != nil || !present || value != "line" {
		t.Fatalf("decoded observed lyrics = %q, %t, %v", value, present, err)
	}
	if _, _, err := EncodeObservedManualLyricsMetadata([]byte(`{"tags":{"LYRICS":["a","b"]}}`)); err == nil {
		t.Fatal("multiple observed values accepted")
	}
	for _, raw := range []string{
		`null`,
		`{}`,
		`{"version":1,"tags":null}`,
		`{"version":99,"tags":{}}`,
		`{"version":1,"tags":{"LYRICS":"line"}}`,
		`{"version":1,"unsupported":{},"tags":{}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, _, err := EncodeObservedManualLyricsMetadata([]byte(raw)); err == nil {
				t.Fatalf("accepted malformed publication metadata %s", raw)
			}
		})
	}
}
