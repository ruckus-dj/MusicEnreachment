package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type startupSnapshotFixture struct {
	snapshot persistence.PublicationStartupSnapshot
	err      error
}

func (fixture startupSnapshotFixture) LoadPublicationStartupSnapshot(context.Context) (persistence.PublicationStartupSnapshot, error) {
	return fixture.snapshot, fixture.err
}

func TestPublicationStartupBarrier(t *testing.T) {
	valid := persistence.PublicationStartupRecord{OperationID: uuid.New(), Phase: "queued", OperationState: "queued", EpochKnown: true, EpochMatches: true, JobAvailable: true, QueuedContractValid: true}
	for _, test := range []struct {
		name     string
		snapshot persistence.PublicationStartupSnapshot
		err      error
		wantErr  bool
	}{
		{name: "empty", snapshot: persistence.PublicationStartupSnapshot{}},
		{name: "valid queued", snapshot: persistence.PublicationStartupSnapshot{Records: []persistence.PublicationStartupRecord{valid}}},
		{name: "claimed executing", snapshot: persistence.PublicationStartupSnapshot{Records: []persistence.PublicationStartupRecord{{HasClaim: true}}}, wantErr: true},
		{name: "claimed quiescent terminal", snapshot: persistence.PublicationStartupSnapshot{Records: []persistence.PublicationStartupRecord{{HasClaim: true, Phase: "committed", OperationState: "succeeded", EpochKnown: true, EpochMatches: true}}}, wantErr: true},
		{name: "invalid queued contract", snapshot: persistence.PublicationStartupSnapshot{Records: []persistence.PublicationStartupRecord{{Phase: "queued", OperationState: "queued", EpochKnown: true, EpochMatches: true, JobAvailable: true}}}, wantErr: true},
		{name: "loader error", err: errors.New("private detail"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := NewPublicationStartup(startupSnapshotFixture{snapshot: test.snapshot, err: test.err}).CheckPublicationStartup(context.Background())
			if (err != nil) != test.wantErr {
				t.Fatalf("CheckPublicationStartup() error = %v, wantErr %v", err, test.wantErr)
			}
			if err != nil && test.err != nil && err.Error() == test.err.Error() {
				t.Fatal("loader error leaked")
			}
		})
	}
}
