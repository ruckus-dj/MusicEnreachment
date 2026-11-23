//go:build integration

package persistence_test

import (
	"context"
	"database/sql"

	"errors"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"

	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type fallbackHandoffRiverInserter struct {
	client                  persistence.RiverInserter
	failWake                bool
	wakeCalls               int
	failProviderFetchAt     int
	providerFetchInsertions int
}

func (inserter *fallbackHandoffRiverInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if inserter.client == nil {
		return nil, errors.New("fallback handoff River client is not ready")
	}
	result, err := inserter.client.InsertTx(ctx, tx, args, options)
	if err != nil {
		return result, err
	}
	if _, ok := args.(persistence.MatchingEvaluationWakeArgs); ok {
		inserter.wakeCalls++
		if inserter.failWake {
			return result, errors.New("injected matching wake insertion failure")
		}
	}
	if _, ok := args.(service.ProviderFetchOperationArgs); ok {
		inserter.providerFetchInsertions++
		if inserter.failProviderFetchAt > 0 && inserter.providerFetchInsertions == inserter.failProviderFetchAt {
			return result, errors.New("injected provider-fetch insertion failure")
		}
	}
	return result, nil
}
