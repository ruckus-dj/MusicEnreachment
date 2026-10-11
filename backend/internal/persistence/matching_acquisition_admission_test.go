package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
)

type noCallMatchingAdmissionInserter struct {
	calls int
}

func (inserter *noCallMatchingAdmissionInserter) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	inserter.calls++
	return nil, nil
}

func TestAdmitMatchingRunRejectsMultiGroupAcquisitionBeforeEnqueue(t *testing.T) {
	parent := &Operation{Kind: "matching_evaluation_v1"}
	inserter := &noCallMatchingAdmissionInserter{}
	repository := &SetupManagerRepository{}
	_, err := repository.AdmitMatchingRun(context.Background(), inserter, MatchingAdmission{
		AdmissionKey:             "multi-group-acquisition",
		Targets:                  []MatchingTarget{{GroupID: uuid.New(), Revision: "a"}, {GroupID: uuid.New(), Revision: "b"}},
		Plan:                     json.RawMessage(`{}`),
		PolicyVersion:            matchingpolicy.CurrentVersion,
		AcquisitionIntentVersion: matchingAcquisitionIntentVersion,
		Parent:                   parent,
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one target group") {
		t.Fatalf("AdmitMatchingRun() error = %v; want one-group acquisition rejection", err)
	}
	if parent.ID != uuid.Nil || inserter.calls != 0 {
		t.Fatalf("parent ID=%s inserts=%d; want no ID allocation or enqueued jobs", parent.ID, inserter.calls)
	}
}
