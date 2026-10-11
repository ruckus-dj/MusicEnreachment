package api

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type manualPickerAPIFixture struct {
	kind, query   string
	offset, limit int
	operation     *persistence.Operation
}

func (provider *manualPickerAPIFixture) CachedResponse(context.Context, string) (*persistence.ProviderCachedResponse, error) {
	return nil, nil
}

func (provider *manualPickerAPIFixture) PrepareOperation(_ context.Context, kind, query string, offset, limit int, _ bool) (*persistence.Operation, error) {
	provider.kind, provider.query, provider.offset, provider.limit = kind, query, offset, limit
	return provider.operation, nil
}

type manualPickerAPIRepository struct{ operation *persistence.Operation }

func (repository manualPickerAPIRepository) AdmitManualPickerSearch(_ context.Context, _ persistence.RiverInserter, _ persistence.ManualPickerSearchAdmission) (*persistence.Operation, error) {
	return repository.operation, nil
}

func (manualPickerAPIRepository) GetManualPickerSearchResult(context.Context, uuid.UUID, uuid.UUID) (*persistence.ManualPickerSearchResult, error) {
	return nil, nil
}

type manualPickerAPIRiver struct{}

func (manualPickerAPIRiver) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, nil
}

func TestManualPickerSearchRouteAcceptsExplicitFirstPage(t *testing.T) {
	operation := &persistence.Operation{ID: uuid.New()}
	provider := &manualPickerAPIFixture{operation: operation}
	searches := service.NewManualPickerSearchService(manualPickerAPIRepository{operation}, provider, manualPickerAPIRiver{})
	handler := HandlerWithDependencies(Dependencies{ManualPickerSearch: searches})
	request := `{"expected_group_revision":"revision","request_id":"` + uuid.NewString() + `","entity":"artist","query":"  artist:test  ","offset":0,"limit":2}`
	recorder := incomingGroupsRequest(t, handler, http.MethodPost, "/incoming-groups/"+uuid.NewString()+"/manual-search", request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("explicit first page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if provider.kind != "artist_search" || provider.query != "artist:test" || provider.offset != 0 || provider.limit != 2 {
		t.Fatalf("provider request kind=%q query=%q page=%d/%d", provider.kind, provider.query, provider.offset, provider.limit)
	}
}
