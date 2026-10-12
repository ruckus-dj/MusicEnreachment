package service

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestFallbackSearchManifestPreservesHitsAndExpandsReleaseUnion(t *testing.T) {
	const recordingPrefix = "1111111111114111811111111111"
	const releasePrefix = "2222222222224222822222222222"
	var recordings []string
	for hit := 0; hit < 10; hit++ {
		var releases []string
		for offset := 0; offset < 2; offset++ {
			release := fmt.Sprintf("%s%04x", releasePrefix, 0x10+hit+offset)
			// Deliberately vary provider casing; UUID identity is case-insensitive.
			if hit == 0 && offset == 0 {
				release = strings.ToUpper(release)
			}
			releases = append(releases, fmt.Sprintf(`{"id":%q,"score":0}`, release))
		}
		if hit == 9 {
			releases = append(releases, fmt.Sprintf(`{"id":%q,"score":37}`, fmt.Sprintf("%s%04x", releasePrefix, 0x1b)))
		}
		recordingID := fmt.Sprintf("%s%04x", recordingPrefix, hit)
		recordings = append(recordings, fmt.Sprintf(`{"id":%q,"score":%d,"releases":[%s]}`, recordingID, hit, strings.Join(releases, ",")))
	}
	payload := json.RawMessage(fmt.Sprintf(`{"count":10,"offset":0,"recordings":[%s]}`, strings.Join(recordings, ",")))
	ids, manifest, err := fallbackSearchManifest("provider_recording_search", payload)
	if err != nil {
		t.Fatalf("fallbackSearchManifest() error = %v", err)
	}
	if len(ids) != 12 {
		t.Fatalf("got %d distinct release IDs, want 12: %v", len(ids), ids)
	}
	var saved struct {
		AcceptedHits json.RawMessage `json:"accepted_hits"`
		ReleaseIDs   []string        `json:"release_ids"`
	}
	if err := json.Unmarshal(manifest, &saved); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if !jsonEqual(saved.AcceptedHits, payload) || len(saved.ReleaseIDs) != 12 {
		t.Fatalf("manifest did not preserve hit details and expanded identities: %s", manifest)
	}
}

func TestFallbackSearchManifestRejectsMalformedRecordingMBID(t *testing.T) {
	payload := json.RawMessage(`{"count":1,"offset":0,"recordings":[{"id":"not-a-uuid","releases":[]}]}`)
	if _, _, err := fallbackSearchManifest("provider_recording_search", payload); err == nil {
		t.Fatal("fallbackSearchManifest() accepted malformed recording MBID")
	}
}

func jsonEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}
