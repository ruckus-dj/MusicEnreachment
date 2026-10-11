package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var ErrManualPickerSearchInvalid = errors.New("manual picker search is invalid")

type manualPickerSearchRepository interface {
	AdmitManualPickerSearch(context.Context, persistence.RiverInserter, persistence.ManualPickerSearchAdmission) (*persistence.Operation, error)
	GetManualPickerSearchResult(context.Context, uuid.UUID, uuid.UUID) (*persistence.ManualPickerSearchResult, error)
}

type ManualPickerSearchService struct {
	repository manualPickerSearchRepository
	providers  matchingAcquisitionProvider
	river      persistence.RiverInserter
}

func NewManualPickerSearchService(repository manualPickerSearchRepository, providers matchingAcquisitionProvider, river persistence.RiverInserter) *ManualPickerSearchService {
	return &ManualPickerSearchService{repository: repository, providers: providers, river: river}
}

func (service *ManualPickerSearchService) Start(ctx context.Context, groupID, requestID uuid.UUID, expectedGroupRevision, kind, query string, offset, limit int) (*persistence.Operation, error) {
	if service == nil || service.repository == nil || service.providers == nil || service.river == nil || groupID == uuid.Nil || requestID == uuid.Nil || expectedGroupRevision == "" ||
		(kind != "artist_search" && kind != "release_search" && kind != "artist_release_browse") || offset < 0 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: group, request, revision, entity, and page are required", ErrManualPickerSearchInvalid)
	}
	query = strings.TrimSpace(query)
	providerKind := kind
	if kind == "artist_release_browse" {
		id, err := uuid.Parse(query)
		if err != nil || id == uuid.Nil || !strings.EqualFold(query, id.String()) {
			return nil, fmt.Errorf("%w: selected artist MBID is invalid", ErrManualPickerSearchInvalid)
		}
		query = strings.ToLower(id.String())
	} else if query == "" {
		return nil, fmt.Errorf("%w: query is required", ErrManualPickerSearchInvalid)
	}
	child, err := service.providers.PrepareOperation(ctx, providerKind, query, offset, limit, false)
	if err != nil {
		return nil, fmt.Errorf("prepare manual picker provider search: %w", err)
	}
	args := ProviderFetchOperationArgs{OperationID: child.ID}
	canonical, err := json.Marshal(struct {
		GroupID       uuid.UUID `json:"group_id"`
		GroupRevision string    `json:"group_revision"`
		Kind          string    `json:"kind"`
		Query         string    `json:"query"`
		Offset        int       `json:"offset"`
		Limit         int       `json:"limit"`
	}{groupID, expectedGroupRevision, kind, query, offset, limit})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	return service.repository.AdmitManualPickerSearch(ctx, service.river, persistence.ManualPickerSearchAdmission{
		RequestID: requestID, GroupID: groupID, GroupRevision: expectedGroupRevision, Kind: kind, Digest: digest,
		Query: query, Offset: offset, Limit: limit, Operation: child, Args: args,
	})
}

func (service *ManualPickerSearchService) Result(ctx context.Context, groupID, operationID uuid.UUID) (*persistence.ManualPickerSearchResult, error) {
	if service == nil || service.repository == nil || groupID == uuid.Nil || operationID == uuid.Nil {
		return nil, fmt.Errorf("manual picker search result is unavailable")
	}
	return service.repository.GetManualPickerSearchResult(ctx, groupID, operationID)
}

var _ river.JobArgs = ProviderFetchOperationArgs{}
