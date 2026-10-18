package persistence

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func publicationCommitEvidenceFixture(t *testing.T, version, policy int, assets json.RawMessage, observationPolicy int) (TrackPublicationAttempt, PublicationCommitOperationSnapshot, PublicationDeliveryGuard) {
	t.Helper()
	attempt, operation, guard := commitEvidenceFixture(t)
	input, err := DecodePublicationInputs(attempt.FrozenInputs)
	if err != nil {
		t.Fatal(err)
	}
	input.Version = version
	input.QualityPolicyVersion = policy
	attempt.FrozenInputs, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
		t.Fatal(err)
	}
	for index, raw := range entries {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		var kind string
		if err := json.Unmarshal(record["kind"], &kind); err != nil {
			t.Fatal(err)
		}
		if kind != "stage_observation" && kind != "publication_exposure" {
			continue
		}
		observationRaw, found := record["observation"]
		if !found {
			continue
		}
		var observation PublicationStageObservation
		if err := json.Unmarshal(observationRaw, &observation); err != nil {
			t.Fatal(err)
		}
		observation.ActualAssets = append(json.RawMessage(nil), assets...)
		observation.QualityPolicyVersion = observationPolicy
		record["observation"], err = json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}
		entries[index], err = json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
	}
	attempt.MutationJournal, err = json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return attempt, operation, guard
}

func TestAssessFirstPublicationCommitSeparatesAssetAndPolicyEvidence(t *testing.T) {
	validAssets := json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`)
	tests := []struct {
		name        string
		version     int
		policy      int
		assets      json.RawMessage
		observation int
		want        []string
	}{
		{name: "current valid matching assets and policy", version: PublicationInputsVersion, policy: publication.QualityPolicyVersion, assets: validAssets, observation: publication.QualityPolicyVersion, want: []string{"namespace_revalidation", "live_delivery_fence"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempt, operation, guard := publicationCommitEvidenceFixture(t, test.version, test.policy, test.assets, test.observation)
			got, err := AssessFirstPublicationCommit(attempt, operation, nil, guard)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.MissingRequirements, test.want) {
				t.Fatalf("missing requirements = %v, want %v", got.MissingRequirements, test.want)
			}
		})
	}
}

func TestAssessFirstPublicationCommitRejectsNullOrMismatchedAssetEvidence(t *testing.T) {
	tests := []struct {
		name        string
		assets      json.RawMessage
		observation int
	}{
		{name: "null assets", assets: json.RawMessage(`null`), observation: publication.QualityPolicyVersion},
		{name: "mismatched policy", assets: json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`), observation: publication.QualityPolicyVersion + 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempt, operation, guard := publicationCommitEvidenceFixture(t, PublicationInputsVersion, publication.QualityPolicyVersion, test.assets, test.observation)
			if _, err := AssessFirstPublicationCommit(attempt, operation, nil, guard); err == nil {
				t.Fatal("invalid stage evidence was accepted")
			}
		})
	}
}

func TestSamePublicationStageObservationComparesAssetEvidenceSemantically(t *testing.T) {
	base := PublicationStageObservation{Version: 1, RelativePath: "stage/file", ActualAudio: json.RawMessage(`{"x":1}`), ActualMetadata: json.RawMessage(`{"version":1,"tags":{}}`), ActualAssets: json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`), QualityPolicyVersion: publication.QualityPolicyVersion}
	reordered := base
	reordered.ActualAssets = json.RawMessage(` { "lyrics" : {"state":"absent"}, "cover":{"state":"absent"}, "version":1 } `)
	if !samePublicationStageObservation(base, reordered) {
		t.Fatal("semantically equal reordered asset JSON did not compare equal")
	}
	changed := base
	changed.ActualAssets = json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"embedded","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","format":"lrc"}}`)
	if samePublicationStageObservation(base, changed) {
		t.Fatal("changed asset outcome compared equal")
	}
	changed = base
	changed.QualityPolicyVersion++
	if samePublicationStageObservation(base, changed) {
		t.Fatal("changed quality policy compared equal")
	}
	absent, null := base, base
	absent.ActualAssets = nil
	null.ActualAssets = json.RawMessage(`null`)
	if samePublicationStageObservation(absent, null) {
		t.Fatal("absent assets and explicit JSON null compared equal")
	}
}
