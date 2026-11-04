package service

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// scriptedPublicationExecutorRepository exposes only explicit callbacks. An
// unconfigured call fails immediately instead of simulating persistence rules.
type scriptedPublicationExecutorRepository struct {
	guards                      []persistence.PublicationDeliveryGuard
	committedResult             *persistence.PublicationResult
	getOperation                func(context.Context, uuid.UUID) (*persistence.Operation, error)
	readAttempt                 func(context.Context, uuid.UUID) (*persistence.TrackPublicationAttempt, error)
	claim                       func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error)
	preparationInputs           func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error)
	manualLyricsInputs          func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error)
	coverInput                  func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationAttemptCoverInput, bool, error)
	sourceHold                  func(context.Context, persistence.PublicationDeliveryGuard) (uuid.UUID, bool, error)
	checkSourceHold             func(context.Context, persistence.PublicationDeliveryGuard, uuid.UUID) (persistence.PublicationSourceHold, bool, error)
	obsoleteOutput              func(context.Context, persistence.PublicationDeliveryGuard, string) (*persistence.PublicationObsoleteOutputCandidate, error)
	resultCurrent               func(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
	commit                      func(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationResult) (bool, error)
	publicationToolsForDelivery func(context.Context, persistence.PublicationDeliveryGuard) ([]persistence.SourceAnalysisToolSelection, *persistence.ToolInstallation, string, bool, error)
	checkDelivery               func(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
}

var errUnexpectedPublicationRepositoryCall = errors.New("unexpected publication repository call")

var _ publicationExecutorRepository = (*scriptedPublicationExecutorRepository)(nil)

func (repository *scriptedPublicationExecutorRepository) record(guard persistence.PublicationDeliveryGuard) {
	repository.guards = append(repository.guards, guard)
}

func (repository *scriptedPublicationExecutorRepository) GetOperation(ctx context.Context, id uuid.UUID) (*persistence.Operation, error) {
	if repository.getOperation == nil {
		return nil, errUnexpectedPublicationRepositoryCall
	}
	return repository.getOperation(ctx, id)
}
func (repository *scriptedPublicationExecutorRepository) ReadPublicationAttempt(ctx context.Context, id uuid.UUID) (*persistence.TrackPublicationAttempt, error) {
	if repository.readAttempt == nil {
		return nil, errUnexpectedPublicationRepositoryCall
	}
	return repository.readAttempt(ctx, id)
}
func (repository *scriptedPublicationExecutorRepository) ClaimPublicationDelivery(ctx context.Context, guard persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
	repository.record(guard)
	if repository.claim == nil {
		return persistence.PublicationDeliveryBlocked, errUnexpectedPublicationRepositoryCall
	}
	return repository.claim(ctx, guard)
}
func (repository *scriptedPublicationExecutorRepository) PublicationPreparationInputs(ctx context.Context, guard persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
	repository.record(guard)
	if repository.preparationInputs == nil {
		return persistence.PublicationInputs{}, false, errUnexpectedPublicationRepositoryCall
	}
	return repository.preparationInputs(ctx, guard)
}
func (repository *scriptedPublicationExecutorRepository) PublicationManualLyricsPreparationInputs(ctx context.Context, guard persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
	repository.record(guard)
	if repository.manualLyricsInputs == nil {
		return persistence.PublicationInputs{}, false, errUnexpectedPublicationRepositoryCall
	}
	return repository.manualLyricsInputs(ctx, guard)
}
func (repository *scriptedPublicationExecutorRepository) PublicationAttemptCoverInputForDelivery(ctx context.Context, guard persistence.PublicationDeliveryGuard) (persistence.PublicationAttemptCoverInput, bool, error) {
	repository.record(guard)
	if repository.coverInput == nil {
		return persistence.PublicationAttemptCoverInput{}, false, errUnexpectedPublicationRepositoryCall
	}
	return repository.coverInput(ctx, guard)
}
func (repository *scriptedPublicationExecutorRepository) PublicationSourceHoldForDelivery(ctx context.Context, guard persistence.PublicationDeliveryGuard) (uuid.UUID, bool, error) {
	repository.record(guard)
	if repository.sourceHold == nil {
		return uuid.Nil, false, errUnexpectedPublicationRepositoryCall
	}
	return repository.sourceHold(ctx, guard)
}
func (repository *scriptedPublicationExecutorRepository) CheckPublicationSourceHoldForDelivery(ctx context.Context, guard persistence.PublicationDeliveryGuard, id uuid.UUID) (persistence.PublicationSourceHold, bool, error) {
	repository.record(guard)
	if repository.checkSourceHold == nil {
		return persistence.PublicationSourceHold{}, false, errUnexpectedPublicationRepositoryCall
	}
	return repository.checkSourceHold(ctx, guard, id)
}
func (repository *scriptedPublicationExecutorRepository) ReadPublicationObsoleteOutputCandidate(ctx context.Context, guard persistence.PublicationDeliveryGuard, path string) (*persistence.PublicationObsoleteOutputCandidate, error) {
	repository.record(guard)
	if repository.obsoleteOutput == nil {
		return nil, errUnexpectedPublicationRepositoryCall
	}
	return repository.obsoleteOutput(ctx, guard, path)
}
func (repository *scriptedPublicationExecutorRepository) CheckPublicationResultCurrent(ctx context.Context, guard persistence.PublicationDeliveryGuard) (bool, error) {
	repository.record(guard)
	if repository.resultCurrent == nil {
		return false, errUnexpectedPublicationRepositoryCall
	}
	return repository.resultCurrent(ctx, guard)
}
func (repository *scriptedPublicationExecutorRepository) CommitPublicationResult(ctx context.Context, guard persistence.PublicationDeliveryGuard, result persistence.PublicationResult) (bool, error) {
	repository.record(guard)
	repository.committedResult = &result
	if repository.commit == nil {
		return false, errUnexpectedPublicationRepositoryCall
	}
	return repository.commit(ctx, guard, result)
}
func (repository *scriptedPublicationExecutorRepository) PublicationToolsForDelivery(ctx context.Context, guard persistence.PublicationDeliveryGuard) ([]persistence.SourceAnalysisToolSelection, *persistence.ToolInstallation, string, bool, error) {
	repository.record(guard)
	if repository.publicationToolsForDelivery == nil {
		return nil, nil, "", false, errUnexpectedPublicationRepositoryCall
	}
	return repository.publicationToolsForDelivery(ctx, guard)
}
func (repository *scriptedPublicationExecutorRepository) CheckPublicationDelivery(ctx context.Context, guard persistence.PublicationDeliveryGuard) (bool, error) {
	repository.record(guard)
	if repository.checkDelivery == nil {
		return false, errUnexpectedPublicationRepositoryCall
	}
	return repository.checkDelivery(ctx, guard)
}

type scriptedSettingsStore struct {
	mu     sync.RWMutex
	values map[string]string
}

func newScriptedSettingsStore(values map[string]string) *scriptedSettingsStore {
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return &scriptedSettingsStore{values: copy}
}
func (store *scriptedSettingsStore) Get(_ context.Context, key string) (string, bool, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	value, exists := store.values[key]
	return value, exists, nil
}
func (store *scriptedSettingsStore) Set(_ context.Context, key, value string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.values[key] = value
	return nil
}
func (store *scriptedSettingsStore) SetMany(_ context.Context, values map[string]string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for key, value := range values {
		store.values[key] = value
	}
	return nil
}
func (store *scriptedSettingsStore) SetIfAbsent(_ context.Context, key, value string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.values[key]; !exists {
		store.values[key] = value
	}
	return store.values[key], nil
}
func (store *scriptedSettingsStore) InitializePlatform(_ context.Context, goos, goarch string) (string, string, bool, error) {
	return goos, goarch, true, nil
}
