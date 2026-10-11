package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var ErrManualReleaseSelectionInvalid = errors.New("manual release selection is invalid")

type manualReleaseAdmissionRepository interface {
	AdmitManualReleaseMatchingRun(context.Context, persistence.RiverInserter, uuid.UUID, uuid.UUID, string, *string, uuid.UUID, persistence.MatchingProviderRequest, persistence.ManualMatchingRunPlan) (*persistence.Operation, error)
}

type ManualReleaseSelectionService struct {
	repository manualReleaseAdmissionRepository
	groups     *IncomingGroups
	providers  matchingAcquisitionProvider
	river      persistence.RiverInserter
}

func NewManualReleaseSelectionService(repository manualReleaseAdmissionRepository, groups *IncomingGroups, providers matchingAcquisitionProvider, river persistence.RiverInserter) *ManualReleaseSelectionService {
	return &ManualReleaseSelectionService{repository: repository, groups: groups, providers: providers, river: river}
}

// Start captures the current group and source snapshot before atomically admitting
// a typed provider lookup and its manual matching run. Callers cannot supply plan data.
func (s *ManualReleaseSelectionService) Start(ctx context.Context, groupID, requestID uuid.UUID, expectedGroupRevision string, expectedEvaluationRevision *string, releaseMBID uuid.UUID) (*persistence.Operation, error) {
	if s == nil || s.repository == nil || s.groups == nil || s.providers == nil || s.river == nil || groupID == uuid.Nil || requestID == uuid.Nil || releaseMBID == uuid.Nil || expectedGroupRevision == "" || expectedEvaluationRevision != nil && *expectedEvaluationRevision == "" {
		return nil, fmt.Errorf("%w: group, revisions, request, and release are required", ErrManualReleaseSelectionInvalid)
	}
	snapshot, err := s.groups.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("read current incoming group snapshot: %w", err)
	}
	var group *IncomingGroup
	for index := range snapshot.Groups {
		if snapshot.Groups[index].ID == groupID {
			group = &snapshot.Groups[index]
			break
		}
	}
	if group == nil || group.Revision != expectedGroupRevision {
		return nil, persistence.ErrIncomingMatchConflict
	}
	memberSet := make(map[uuid.UUID]bool, len(group.Members))
	for _, member := range group.Members {
		memberSet[member] = true
	}
	files := make([]IncomingFile, 0, len(memberSet))
	for _, file := range snapshot.Files {
		if memberSet[file.VariantID] {
			files = append(files, file)
		}
	}
	if len(files) != len(memberSet) {
		return nil, persistence.ErrIncomingMatchConflict
	}
	child, err := s.providers.PrepareOperation(ctx, "release_lookup", releaseMBID.String(), 0, 0, false)
	if err != nil {
		return nil, fmt.Errorf("prepare selected MusicBrainz release lookup: %w", err)
	}
	args := ProviderFetchOperationArgs{OperationID: child.ID}
	planJSON, err := json.Marshal(struct {
		SchemaVersion int       `json:"schema_version"`
		Mode          string    `json:"mode"`
		ReleaseMBID   uuid.UUID `json:"release_mbid"`
	}{1, "manual_release_selection", releaseMBID})
	if err != nil {
		return nil, fmt.Errorf("encode manual release selection plan: %w", err)
	}
	groupJSON, err := json.Marshal(struct {
		GroupID  uuid.UUID   `json:"group_id"`
		Revision string      `json:"revision"`
		Members  []uuid.UUID `json:"members"`
	}{group.ID, group.Revision, group.Members})
	if err != nil {
		return nil, fmt.Errorf("encode manual release group context: %w", err)
	}
	if child.ProviderSourceID == nil || child.ProviderID == nil || child.ProviderConfigurationIdentity == nil || child.ProviderEndpoint == "" {
		return nil, fmt.Errorf("prepared MusicBrainz release lookup has no captured provider fence")
	}
	sourceJSON, err := json.Marshal(struct {
		ProviderID            uuid.UUID      `json:"provider_id"`
		SourceID              uuid.UUID      `json:"provider_source_id"`
		Endpoint              string         `json:"endpoint"`
		ConfigurationIdentity string         `json:"configuration_identity"`
		Files                 []IncomingFile `json:"files"`
	}{*child.ProviderID, *child.ProviderSourceID, child.ProviderEndpoint, *child.ProviderConfigurationIdentity, files})
	if err != nil {
		return nil, fmt.Errorf("encode manual release source context: %w", err)
	}
	return s.repository.AdmitManualReleaseMatchingRun(ctx, s.river, requestID, groupID, expectedGroupRevision, expectedEvaluationRevision, releaseMBID,
		persistence.MatchingProviderRequest{Operation: child, Args: args}, persistence.ManualMatchingRunPlan{
			GroupingEpoch: snapshot.Epoch, PolicyVersion: matchingpolicy.CurrentVersion,
			Plan: planJSON, GroupContext: groupJSON, SourceContext: sourceJSON,
		})
}
