package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type fallbackAdvancementRepository struct {
	*matchingAcquisitionRepositoryFixture
	receipt *persistence.MatchingFallbackCalculationReceipt
	next    *persistence.MatchingProviderRequest
}

func (repository *fallbackAdvancementRepository) GetMatchingFallbackCalculationReceipt(context.Context, uuid.UUID, int64) (*persistence.MatchingFallbackCalculationReceipt, error) {
	return repository.receipt, nil
}

func (repository *fallbackAdvancementRepository) AdvanceFallbackApplicabilityForDelivery(_ context.Context, _ persistence.RiverInserter, _ uuid.UUID, _ int, _ int64, _ persistence.MatchingDeliveryClaim, _ int64, next *persistence.MatchingProviderRequest) (bool, error) {
	repository.next = next
	return true, nil
}

type fallbackAdvancementProvider struct {
	prepared int
	kind     string
	query    string
	offset   int
	limit    int
}

func (provider *fallbackAdvancementProvider) CachedResponse(context.Context, string) (*persistence.ProviderCachedResponse, error) {
	return nil, nil
}

func (provider *fallbackAdvancementProvider) PrepareOperation(_ context.Context, kind, query string, offset, limit int, explicit bool) (*persistence.Operation, error) {
	provider.prepared++
	provider.kind, provider.query, provider.offset, provider.limit = kind, query, offset, limit
	key := "recording-search:0:10:" + query
	return &persistence.Operation{ID: uuid.New(), Kind: "provider_" + kind, State: "queued", ProviderCacheKey: &key, ProviderExplicitRefresh: explicit}, nil
}

func TestMatchingAcquisitionReplaysCalculationReceiptToAdvanceFallbackPlan(t *testing.T) {
	for _, test := range []struct {
		name        string
		edges       string
		wantNext    bool
		wantPrepare int
	}{
		{name: "zero advances to exact next query", edges: `[]`, wantNext: true, wantPrepare: 1},
		{name: "qualified parks for seal", edges: `[ {"confidence":0.7} ]`, wantPrepare: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			runID := uuid.New()
			intent := persistence.MatchingAcquisitionIntent{SchemaVersion: 3, FallbackQueryPlan: json.RawMessage(`{"schema_version":1,"stages":[{"kind":"album_track","query":"release AND recording","offset":0,"limit":10},{"kind":"track","query":"recording","offset":0,"limit":10}]}`)}
			repository := &fallbackAdvancementRepository{
				matchingAcquisitionRepositoryFixture: &matchingAcquisitionRepositoryFixture{snapshot: persistence.MatchingAcquisitionSnapshot{Intent: intent, Revision: 12, Phase: persistence.MatchingAcquisitionFallbackApplicability}},
				receipt:                              &persistence.MatchingFallbackCalculationReceipt{RunID: runID, AcquisitionRevision: 12, StageIndex: 0, Edges: json.RawMessage(test.edges)},
			}
			provider := &fallbackAdvancementProvider{}
			acquisition := NewMatchingAcquisitionService(repository, provider, matchingAcquisitionRiverFixture{})
			changed, acquired, err := acquisition.Advance(context.Background(), runID, 1, 9, persistence.MatchingDeliveryClaim{ExecutionEpoch: 2, RiverAttempt: 1})
			if err != nil || !changed || !acquired {
				t.Fatalf("Advance() = %t, %t, %v", changed, acquired, err)
			}
			if (repository.next != nil) != test.wantNext || provider.prepared != test.wantPrepare {
				t.Fatalf("next=%v prepared=%d; want next=%t prepared=%d", repository.next, provider.prepared, test.wantNext, test.wantPrepare)
			}
			if test.wantNext && (provider.kind != "recording_search" || provider.query != "recording" || provider.offset != 0 || provider.limit != 10 || repository.next.Operation.ProviderExplicitRefresh) {
				t.Fatalf("prepared fallback request=%+v arguments=%s/%d/%d", repository.next.Operation, provider.query, provider.offset, provider.limit)
			}
		})
	}
}
