package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type manualPickerProviderFixture struct {
	kind, query   string
	offset, limit int
	operation     *persistence.Operation
}

func (provider *manualPickerProviderFixture) CachedResponse(context.Context, string) (*persistence.ProviderCachedResponse, error) {
	return nil, errors.New("unexpected cache read")
}

func (provider *manualPickerProviderFixture) PrepareOperation(_ context.Context, kind, query string, offset, limit int, _ bool) (*persistence.Operation, error) {
	provider.kind, provider.query, provider.offset, provider.limit = kind, query, offset, limit
	if provider.operation != nil && kind == "release_search" {
		// Mirror ProviderFetchService.PrepareOperation so a test can observe the
		// durable search-intent key derived from the query it was handed.
		key := fmt.Sprintf("release-search:%d:%d:%s", offset, limit, query)
		provider.operation.ProviderCacheKey = &key
	}
	return provider.operation, nil
}

type manualPickerRepositoryFixture struct {
	input  persistence.ManualPickerSearchAdmission
	result *persistence.Operation
	err    error
}

func (repository *manualPickerRepositoryFixture) AdmitManualPickerSearch(_ context.Context, _ persistence.RiverInserter, input persistence.ManualPickerSearchAdmission) (*persistence.Operation, error) {
	repository.input = input
	return repository.result, repository.err
}

func (*manualPickerRepositoryFixture) GetManualPickerSearchResult(context.Context, uuid.UUID, uuid.UUID) (*persistence.ManualPickerSearchResult, error) {
	return nil, nil
}

type manualPickerRiverFixture struct{}

func (manualPickerRiverFixture) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, nil
}

func TestManualPickerSearchStartPreservesLiteralQueryAndPage(t *testing.T) {
	child := &persistence.Operation{ID: uuid.New()}
	provider := &manualPickerProviderFixture{operation: child}
	repository := &manualPickerRepositoryFixture{result: child}
	service := NewManualPickerSearchService(repository, provider, manualPickerRiverFixture{})
	groupID, requestID := uuid.New(), uuid.New()
	query := `  artist:"North & South"  `
	got, err := service.Start(context.Background(), groupID, requestID, "group-revision", "artist_search", query, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got != child || provider.query != `artist:"North & South"` || provider.offset != 0 || provider.limit != 2 {
		t.Fatalf("prepared search = %+v query=%q page=%d/%d", got, provider.query, provider.offset, provider.limit)
	}
	if repository.input.Query != provider.query || repository.input.Offset != 0 || repository.input.Limit != 2 || repository.input.GroupID != groupID || repository.input.RequestID != requestID {
		t.Fatalf("admission did not retain explicit request: %+v", repository.input)
	}
}

func TestManualPickerSearchStartCompilesReleaseSearchTermsForProvider(t *testing.T) {
	child := &persistence.Operation{ID: uuid.New()}
	provider := &manualPickerProviderFixture{operation: child}
	repository := &manualPickerRepositoryFixture{result: child}
	service := NewManualPickerSearchService(repository, provider, manualPickerRiverFixture{})
	got, err := service.Start(context.Background(), uuid.New(), uuid.New(), "group-revision", "release_search", "  anacondaz выходи за меня  ", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	const compiled = `(artist:"anacondaz" OR artistname:"anacondaz" OR release:"anacondaz") AND ` +
		`(artist:"выходи" OR artistname:"выходи" OR release:"выходи") AND ` +
		`(artist:"за" OR artistname:"за" OR release:"за") AND ` +
		`(artist:"меня" OR artistname:"меня" OR release:"меня")`
	if got != child || provider.kind != "release_search" || provider.query != compiled || provider.offset != 0 || provider.limit != 10 {
		t.Fatalf("prepared search = %+v kind=%q query=%q page=%d/%d", got, provider.kind, provider.query, provider.offset, provider.limit)
	}
	if repository.input.Query != "anacondaz выходи за меня" {
		t.Fatalf("admission did not preserve the human query: %q", repository.input.Query)
	}
	// A provider key derived from the unfielded human text would share no cache
	// entry with the compiled search.
	wantKey := "release-search:0:10:" + compiled
	if got.ProviderCacheKey == nil || *got.ProviderCacheKey != wantKey {
		t.Fatalf("provider cache key = %v, want %q", got.ProviderCacheKey, wantKey)
	}
}

func TestManualPickerSearchStartValidatesPageBeforeProvider(t *testing.T) {
	provider := &manualPickerProviderFixture{operation: &persistence.Operation{ID: uuid.New()}}
	service := NewManualPickerSearchService(&manualPickerRepositoryFixture{}, provider, manualPickerRiverFixture{})
	_, err := service.Start(context.Background(), uuid.New(), uuid.New(), "revision", "release_search", "query", -1, 2)
	if !errors.Is(err, ErrManualPickerSearchInvalid) || provider.kind != "" {
		t.Fatalf("invalid page error = %v, provider kind = %q", err, provider.kind)
	}
}
