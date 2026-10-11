package service

import (
	"context"
	"database/sql"
	"errors"
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

func TestManualPickerSearchStartValidatesPageBeforeProvider(t *testing.T) {
	provider := &manualPickerProviderFixture{operation: &persistence.Operation{ID: uuid.New()}}
	service := NewManualPickerSearchService(&manualPickerRepositoryFixture{}, provider, manualPickerRiverFixture{})
	_, err := service.Start(context.Background(), uuid.New(), uuid.New(), "revision", "release_search", "query", -1, 2)
	if !errors.Is(err, ErrManualPickerSearchInvalid) || provider.kind != "" {
		t.Fatalf("invalid page error = %v, provider kind = %q", err, provider.kind)
	}
}
