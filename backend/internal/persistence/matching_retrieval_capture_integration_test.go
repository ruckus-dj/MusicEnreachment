//go:build integration

package persistence_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestGetMatchingAcquisitionMetadataCaptureValidatesImmutableIntentPostgres(t *testing.T) {
	t.Parallel()
	fixture := newMatchingAcquisitionGuardFixture(t)

	var original []byte
	if err := fixture.db.NewRaw(`SELECT intent FROM matching_acquisition WHERE run_id=?`, fixture.parentID).Scan(fixture.ctx, &original); err != nil {
		t.Fatal(err)
	}
	if _, _, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, fixture.parentID, "admitted"); err != nil || !exists {
		t.Fatalf("valid admission capture rejected: exists=%v err=%v", exists, err)
	}

	t.Run("capture mutation with recomputed capture digest", func(t *testing.T) {
		var candidate persistence.MatchingAcquisitionIntent
		if err := json.Unmarshal(original, &candidate); err != nil {
			t.Fatal(err)
		}
		candidate.CapturedMembers[0].Tags = json.RawMessage(`{"title":["tampered"]}`)
		captureBytes, err := json.Marshal(candidate.CapturedMembers)
		if err != nil {
			t.Fatal(err)
		}
		captureDigest := sha256.Sum256(captureBytes)
		candidate.CaptureDigest = fmt.Sprintf("%x", captureDigest[:])
		encoded, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE matching_acquisition SET intent=?::jsonb WHERE run_id=?`, string(encoded), fixture.parentID).Exec(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, fixture.parentID, "admitted"); err == nil || exists {
			t.Fatalf("capture changed without immutable intent digest rejection: exists=%v err=%v", exists, err)
		}
	})

	if _, err := fixture.db.NewRaw(`UPDATE matching_acquisition SET intent=?::jsonb WHERE run_id=?`, string(original), fixture.parentID).Exec(fixture.ctx); err != nil {
		t.Fatal(err)
	}

	t.Run("plan digest detached from matching run", func(t *testing.T) {
		var candidate persistence.MatchingAcquisitionIntent
		if err := json.Unmarshal(original, &candidate); err != nil {
			t.Fatal(err)
		}
		candidate.PlanDigest = fmt.Sprintf("%x", sha256.Sum256([]byte("different plan")))
		for index := range candidate.CapturedMembers {
			candidate.CapturedMembers[index].Tags = canonicalMatchingCaptureTestJSON(t, candidate.CapturedMembers[index].Tags)
		}
		for index := range candidate.InitialProviderInputs {
			candidate.InitialProviderInputs[index].Input = canonicalMatchingCaptureTestJSON(t, candidate.InitialProviderInputs[index].Input)
		}
		encoded, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		intentDigest := sha256.Sum256(encoded)
		if _, err := fixture.db.NewRaw(`UPDATE matching_acquisition SET intent=?::jsonb, intent_digest=? WHERE run_id=?`, string(encoded), intentDigest[:], fixture.parentID).Exec(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, exists, err := fixture.repository.GetMatchingAcquisitionMetadataCapture(fixture.ctx, fixture.parentID, "admitted"); err == nil || exists {
			t.Fatalf("intent with detached plan digest accepted: exists=%v err=%v", exists, err)
		}
	})
}

func canonicalMatchingCaptureTestJSON(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
