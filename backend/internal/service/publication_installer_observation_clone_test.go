package service

import (
	"encoding/json"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestAbsentTargetInstallerConstructorClonesValidatedObservation(t *testing.T) {
	fixture := newAbsentInstallerFixture(t, persistence.PublicationExposureRecorded, nil)
	input := persistence.PublicationStageObservation{
		Version: 1, RelativePath: "stage/candidate", PayloadSHA256: intentDigest(absentInstallerPayload),
		ActualAudio: json.RawMessage(`{"audio":1}`), ActualMetadata: json.RawMessage(`{"metadata":2}`),
		ActualAssets: json.RawMessage(`{"assets":3}`), QualityPolicyVersion: 4,
	}
	size, mtime := int64(10), int64(20)
	input.Ownership = persistence.ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}
	clone, err := newAbsentTargetInstaller(fixture.scope, fixture.repo, fixture.installer.guard, fixture.intent, input, fixture.stage, fixture.root, fixture.stageDir, fixture.root, "candidate", "installed.bin")
	if err != nil {
		t.Fatal(err)
	}
	input.ActualAudio[2] = 'X'
	input.ActualMetadata[2] = 'X'
	input.ActualAssets[2] = 'X'
	*input.Ownership.ExpectedSize = 99
	*input.Ownership.ExpectedMTime = 98
	got := clone.validated
	if string(got.ActualAudio) != `{"audio":1}` || string(got.ActualMetadata) != `{"metadata":2}` || string(got.ActualAssets) != `{"assets":3}` || got.QualityPolicyVersion != 4 {
		t.Fatalf("installer retained mutable JSON evidence: %+v", got)
	}
	if got.Ownership.ExpectedSize == input.Ownership.ExpectedSize || got.Ownership.ExpectedMTime == input.Ownership.ExpectedMTime || *got.Ownership.ExpectedSize != 10 || *got.Ownership.ExpectedMTime != 20 {
		t.Fatalf("installer retained aliased ownership pointers: %+v", got.Ownership)
	}
}
