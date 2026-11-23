package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type genreCatalogStore interface {
	State(context.Context) (persistence.GenreCatalogState, error)
}

type genreCatalogDeliveryStore interface {
	DispatchDueRefresh(context.Context, persistence.GenreCatalogJobEnqueuer, time.Time, time.Time) (int64, bool, error)
	ClaimRefresh(context.Context, int64) (string, bool, error)
	BeginClaimedRefresh(context.Context, int64, string, string, time.Time) (bool, error)
	ReplaceFenced(context.Context, int64, string, string, json.RawMessage, time.Time) (bool, error)
	RecordFailureFenced(context.Context, int64, string, string, time.Time, string) (bool, error)
}

type genreCatalogRetiringDeliveryStore interface {
	DispatchDueRefreshWithRetired(context.Context, persistence.GenreCatalogJobEnqueuer, time.Time, time.Time) (int64, bool, uuid.UUID, error)
}

type musicBrainzConfigReader interface {
	GetMusicBrainzConfig(context.Context) (settings.MusicBrainzConfig, error)
}

type setupCompletionReader interface {
	SetupCompleted(context.Context) (bool, error)
}

// genreCatalogSetupAdmissionStore admits the Setup-only durable refresh pinned
// to the exact verified MusicBrainz configuration identity.
type genreCatalogSetupAdmissionStore interface {
	AdmitSetupRefresh(context.Context, persistence.GenreCatalogJobEnqueuer, string) (uuid.UUID, error)
}

type GenreCatalogStatus struct {
	Count             int                `json:"count"`
	UpdatedAt         *time.Time         `json:"updated_at"`
	LastAttemptAt     *time.Time         `json:"last_attempt_at"`
	LastError         *string            `json:"last_error"`
	RefreshStatus     string             `json:"refresh_status"`
	ActiveOperationID *string            `json:"active_operation_id"`
	Items             []GenreCatalogItem `json:"items"`
}

// GenreCatalogSetupState is the Setup-only catalog proof. Ready is true only
// when a complete catalog was published for the exact verified MusicBrainz
// configuration identity, so a catalog for another configuration can neither
// unlock nor report its count to the wizard.
type GenreCatalogSetupState struct {
	Ready  bool   `json:"ready"`
	Status string `json:"status" enum:"idle,loading,ready,failed"`
	Count  int    `json:"count"`
}

type GenreCatalogService struct {
	store    genreCatalogStore
	registry musicBrainzConfigReader
	provider *ProviderFactory
	setup    setupCompletionReader
	queue    interface {
		EnqueueRefresh(context.Context, persistence.GenreCatalogJobEnqueuer) error
	}
	river      persistence.GenreCatalogJobEnqueuer
	operations *Operations
}

func (catalog *GenreCatalogService) WithOperations(operations *Operations) *GenreCatalogService {
	catalog.operations = operations
	return catalog
}

func (catalog *GenreCatalogService) WithSetupCompletion(reader setupCompletionReader) *GenreCatalogService {
	catalog.setup = reader
	return catalog
}

func NewGenreCatalogService(store genreCatalogStore, registry musicBrainzConfigReader, provider *ProviderFactory) *GenreCatalogService {
	return &GenreCatalogService{store: store, registry: registry, provider: provider}
}

func (catalog *GenreCatalogService) WithRefreshQueue(queue interface {
	EnqueueRefresh(context.Context, persistence.GenreCatalogJobEnqueuer) error
}, river persistence.GenreCatalogJobEnqueuer) *GenreCatalogService {
	catalog.queue, catalog.river = queue, river
	return catalog
}

func (catalog *GenreCatalogService) StartRefresh(ctx context.Context) (OperationSnapshot, error) {
	if catalog.queue == nil || catalog.river == nil || catalog.operations == nil {
		return OperationSnapshot{}, fmt.Errorf("genre catalog refresh queue is unavailable")
	}
	if catalog.setup != nil {
		completed, err := catalog.setup.SetupCompleted(ctx)
		if err != nil {
			return OperationSnapshot{}, fmt.Errorf("read setup completion for genre catalog refresh: %w", err)
		}
		if !completed {
			return OperationSnapshot{}, fmt.Errorf("genre catalog refresh requires completed setup")
		}
	}
	if err := catalog.queue.EnqueueRefresh(ctx, catalog.river); err != nil {
		return OperationSnapshot{}, err
	}
	state, err := catalog.store.State(ctx)
	if err != nil {
		return OperationSnapshot{}, fmt.Errorf("read admitted genre catalog refresh: %w", err)
	}
	if state.RefreshRequest.OperationID == uuid.Nil {
		return OperationSnapshot{}, fmt.Errorf("admitted genre catalog refresh has no operation")
	}
	catalog.operations.notify(state.RefreshRequest.OperationID)
	snapshot, err := catalog.operations.Snapshot(ctx, state.RefreshRequest.OperationID)
	if err != nil {
		return OperationSnapshot{}, fmt.Errorf("read admitted genre catalog operation: %w", err)
	}
	return snapshot, nil
}

// SetupState reports whether Setup may continue for the current verified
// MusicBrainz configuration. Only a catalog published for that exact identity
// is ready; a loading request must also belong to it, and counts from any other
// configuration are never exposed.
func (catalog *GenreCatalogService) SetupState(ctx context.Context, identity string, verified bool) (GenreCatalogSetupState, error) {
	state, err := catalog.store.State(ctx)
	if err != nil {
		return GenreCatalogSetupState{}, fmt.Errorf("read genre catalog setup state: %w", err)
	}
	if !verified || identity == "" {
		return GenreCatalogSetupState{Status: "idle"}, nil
	}
	if state.UpdatedAt != nil && state.PublishedConfigurationIdentity != nil && *state.PublishedConfigurationIdentity == identity {
		return GenreCatalogSetupState{Ready: true, Status: "ready", Count: state.ItemCount}, nil
	}
	request := state.RefreshRequest
	if request.Status == "queued" || request.Status == "running" {
		if request.ConfigurationIdentity == identity || (request.ConfigurationIdentity == "" && state.ConfigurationIdentity == identity) {
			return GenreCatalogSetupState{Status: "loading"}, nil
		}
	}
	attemptIdentity := state.ConfigurationIdentity
	if request.ConfigurationIdentity != "" {
		attemptIdentity = request.ConfigurationIdentity
	}
	if attemptIdentity == identity && state.LastError != nil {
		return GenreCatalogSetupState{Status: "failed"}, nil
	}
	return GenreCatalogSetupState{Status: "idle"}, nil
}

// StartSetupRefresh admits the initial Setup catalog download for the exact
// verified configuration identity. Admission is durable and coalesces onto an
// already active Setup request for the same identity.
func (catalog *GenreCatalogService) StartSetupRefresh(ctx context.Context, identity string) (OperationSnapshot, error) {
	if catalog.river == nil || catalog.operations == nil {
		return OperationSnapshot{}, fmt.Errorf("genre catalog refresh queue is unavailable")
	}
	store, ok := catalog.store.(genreCatalogSetupAdmissionStore)
	if !ok {
		return OperationSnapshot{}, fmt.Errorf("genre catalog setup admission is unavailable")
	}
	operationID, err := store.AdmitSetupRefresh(ctx, catalog.river, identity)
	if err != nil {
		return OperationSnapshot{}, fmt.Errorf("admit setup genre catalog refresh: %w", err)
	}
	catalog.operations.notify(operationID)
	snapshot, err := catalog.operations.Snapshot(ctx, operationID)
	if err != nil {
		return OperationSnapshot{}, fmt.Errorf("read admitted setup genre catalog operation: %w", err)
	}
	return snapshot, nil
}

// DispatchDueRefresh recovers lost deliveries. It only admits a scheduled
// refresh after Setup, so an unconfigured instance never contacts the provider
// without an explicit Setup request.
func (catalog *GenreCatalogService) DispatchDueRefresh(ctx context.Context) error {
	store, ok := catalog.store.(genreCatalogDeliveryStore)
	if catalog.setup == nil || catalog.queue == nil || catalog.river == nil || !ok {
		return fmt.Errorf("genre catalog refresh dispatcher is unavailable")
	}
	completed, err := catalog.setup.SetupCompleted(ctx)
	if err != nil {
		return fmt.Errorf("read setup completion for genre catalog refresh: %w", err)
	}
	if !completed {
		if recovery, ok := catalog.store.(interface {
			RetireLostSetupRefresh(context.Context) (uuid.UUID, error)
		}); ok {
			retiredID, err := recovery.RetireLostSetupRefresh(ctx)
			if err != nil {
				return fmt.Errorf("retire lost setup genre catalog refresh: %w", err)
			}
			if retiredID != uuid.Nil && catalog.operations != nil {
				catalog.operations.Notify(retiredID)
			}
		}
		return nil
	}
	now := time.Now().UTC()
	var retiredID uuid.UUID
	if retiringStore, ok := catalog.store.(genreCatalogRetiringDeliveryStore); ok {
		_, _, retiredID, err = retiringStore.DispatchDueRefreshWithRetired(ctx, catalog.river, now, now.Add(7*24*time.Hour))
	} else {
		_, _, err = store.DispatchDueRefresh(ctx, catalog.river, now, now.Add(7*24*time.Hour))
	}
	if err != nil {
		return fmt.Errorf("dispatch due genre catalog refresh: %w", err)
	}
	if retiredID != uuid.Nil && catalog.operations != nil {
		catalog.operations.Notify(retiredID)
	}
	return nil
}

func (catalog *GenreCatalogService) Status(ctx context.Context) (GenreCatalogStatus, error) {
	state, err := catalog.store.State(ctx)
	if err != nil {
		return GenreCatalogStatus{}, err
	}
	var items []GenreCatalogItem
	if err := json.Unmarshal(state.Items, &items); err != nil {
		return GenreCatalogStatus{}, fmt.Errorf("decode current genre catalog: %w", err)
	}
	var activeID *string
	if (state.RefreshRequest.Status == "queued" || state.RefreshRequest.Status == "running") && state.RefreshRequest.OperationID != uuid.Nil {
		id := state.RefreshRequest.OperationID.String()
		activeID = &id
	}
	return GenreCatalogStatus{Count: len(items), UpdatedAt: state.UpdatedAt, LastAttemptAt: state.LastAttemptAt, LastError: state.LastError, RefreshStatus: state.RefreshRequest.Status, ActiveOperationID: activeID, Items: items}, nil
}

func (catalog *GenreCatalogService) Search(ctx context.Context, query string, limit int) ([]GenreCatalogItem, error) {
	if limit < 1 || limit > 100 || strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("invalid genre search")
	}
	status, err := catalog.Status(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var exact, partial []GenreCatalogItem
	for _, item := range status.Items {
		name, source := strings.ToLower(item.Name), strings.ToLower(item.SourceName)
		if name == query || source == query {
			exact = append(exact, item)
		} else if strings.Contains(name, query) || strings.Contains(source, query) {
			partial = append(partial, item)
		}
	}
	less := func(items []GenreCatalogItem) {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Name != items[j].Name {
				return items[i].Name < items[j].Name
			}
			return items[i].ID < items[j].ID
		})
	}
	less(exact)
	less(partial)
	items := append(exact, partial...)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// RefreshDelivery claims this River delivery before reading runtime settings.
// The token fences every terminal write, including writes made after a worker
// retry or a newer delivery has claimed the durable singleton request.
func (catalog *GenreCatalogService) RefreshDelivery(ctx context.Context, operationID uuid.UUID, jobID int64) error {
	store, ok := catalog.store.(genreCatalogDeliveryStore)
	if !ok {
		return fmt.Errorf("genre catalog delivery store is unavailable")
	}
	if catalog.operations != nil {
		operation, err := catalog.operations.Get(ctx, operationID)
		if err != nil {
			return fmt.Errorf("read genre catalog operation: %w", err)
		}
		if operation.Kind != "refresh_genre_catalog_v1" || (operation.State != "queued" && operation.State != "running") || operation.RiverJobID == nil || *operation.RiverJobID != jobID {
			return nil
		}
	}
	token, claimed, err := store.ClaimRefresh(ctx, jobID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	if catalog.operations != nil {
		catalog.operations.Notify(operationID)
	}
	state, stateErr := catalog.store.State(ctx)
	if stateErr != nil {
		return catalog.failDelivery(ctx, operationID, jobID, token, "", fmt.Errorf("read genre catalog refresh state: %w", stateErr))
	}
	config, err := catalog.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return catalog.failDelivery(ctx, operationID, jobID, token, state.ConfigurationIdentity, fmt.Errorf("read MusicBrainz genre configuration: %w", err))
	}
	// A Setup admission pins the exact verified configuration identity. It must
	// never substitute the currently saved configuration: a stale or unverified
	// pin settles as a fenced failure before any provider request.
	pinnedIdentity := state.RefreshRequest.ConfigurationIdentity
	identity := config.Identity
	if pinnedIdentity != "" {
		identity = pinnedIdentity
		if config.Identity != pinnedIdentity || config.VerifiedAt == nil {
			started, beginErr := store.BeginClaimedRefresh(ctx, jobID, token, pinnedIdentity, time.Now().UTC())
			if beginErr != nil {
				return catalog.failDelivery(ctx, operationID, jobID, token, pinnedIdentity, beginErr)
			}
			if !started {
				return nil
			}
			return catalog.failDelivery(ctx, operationID, jobID, token, pinnedIdentity, fmt.Errorf("pinned MusicBrainz genre configuration is stale or unverified"))
		}
	}
	started, err := store.BeginClaimedRefresh(ctx, jobID, token, identity, time.Now().UTC())
	if err != nil {
		return catalog.failDelivery(ctx, operationID, jobID, token, identity, err)
	}
	if !started {
		return nil
	}
	provider, err := catalog.provider.MusicBrainzForConfig(ctx, config)
	if err != nil {
		return catalog.failDelivery(ctx, operationID, jobID, token, identity, fmt.Errorf("create MusicBrainz genre provider: %w", err))
	}
	items, err := LoadGenreCatalog(ctx, provider)
	if err != nil {
		return catalog.failDelivery(ctx, operationID, jobID, token, identity, err)
	}
	current, err := catalog.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return catalog.failDelivery(ctx, operationID, jobID, token, identity, fmt.Errorf("recheck MusicBrainz genre configuration: %w", err))
	}
	if current.Identity != identity || (pinnedIdentity != "" && current.VerifiedAt == nil) {
		return catalog.failDelivery(ctx, operationID, jobID, token, identity, fmt.Errorf("MusicBrainz configuration changed during genre catalog refresh"))
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return catalog.failDelivery(ctx, operationID, jobID, token, identity, fmt.Errorf("encode genre catalog: %w", err))
	}
	return catalog.publishDelivery(ctx, operationID, jobID, token, identity, raw)
}

func (catalog *GenreCatalogService) publishDelivery(ctx context.Context, operationID uuid.UUID, jobID int64, token, identity string, items json.RawMessage) error {
	store, ok := catalog.store.(genreCatalogDeliveryStore)
	if !ok {
		return fmt.Errorf("genre catalog delivery store is unavailable")
	}
	updated, err := store.ReplaceFenced(ctx, jobID, token, identity, items, time.Now().UTC())
	if err != nil {
		return catalog.failDelivery(ctx, operationID, jobID, token, identity, err)
	}
	if !updated {
		return catalog.failDelivery(ctx, operationID, jobID, token, identity, fmt.Errorf("genre catalog refresh was superseded"))
	}
	if catalog.operations != nil && operationID != uuid.Nil {
		catalog.operations.Notify(operationID)
	}
	return nil
}

func (catalog *GenreCatalogService) failDelivery(ctx context.Context, operationID uuid.UUID, jobID int64, token, identity string, cause error) error {
	const safeMessage = "MusicBrainz genre catalog refresh failed"
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	store, ok := catalog.store.(genreCatalogDeliveryStore)
	if !ok {
		return errors.Join(cause, fmt.Errorf("genre catalog delivery store is unavailable"))
	}
	recorded, recordErr := store.RecordFailureFenced(recordCtx, jobID, token, identity, time.Now().UTC(), safeMessage)
	if recorded && catalog.operations != nil && operationID != uuid.Nil {
		catalog.operations.Notify(operationID)
	}
	return errors.Join(cause, recordErr)
}
