package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type ProviderCacheRepository struct {
	db bun.IDB
}

const acoustIDCacheLockNamespace int32 = 0x41434f55 // "ACOU"

// lockAcoustIDCacheKeys serializes reservations, accepted writes, and matching
// publication for each official-source/cache-key pair. Callers must acquire
// these gates before taking provider, operation, or response-cache row locks.
func lockAcoustIDCacheKeys(ctx context.Context, tx bun.IDB, sourceID uuid.UUID, cacheKeys []string, shared bool) error {
	keys := append([]string(nil), cacheKeys...)
	sort.Strings(keys)
	var previous string
	for index, cacheKey := range keys {
		if cacheKey == "" {
			return fmt.Errorf("AcoustID cache key is required")
		}
		if index > 0 && cacheKey == previous {
			continue
		}
		digest := sha256.Sum256([]byte(sourceID.String() + "\x00" + cacheKey))
		key := int32(binary.BigEndian.Uint32(digest[:4]))
		if err := advisoryXactLock(ctx, tx, shared, acoustIDCacheLockNamespace, key); err != nil {
			return fmt.Errorf("lock AcoustID response cache key: %w", err)
		}
		previous = cacheKey
	}
	return nil
}

func officialAcoustIDSourceID() uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("acoustid-source:"+acoustIDOfficialEndpoint))
}

// NormalizeMusicBrainzEndpoint applies the same trailing-slash normalization
// used by the HTTP adapter before deriving provider-source identity.
func NormalizeMusicBrainzEndpoint(endpoint string) string {
	return strings.TrimSuffix(endpoint, "/")
}

// ResolveAcoustIDSource snapshots the credential revision and returns the one
// fixed official source. Credential values never enter provider-source identity.
func (repository *ProviderCacheRepository) ResolveAcoustIDSource(ctx context.Context, expectedRevision string) (ProviderSource, error) {
	var source ProviderSource
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "acoustid-config"); err != nil {
			return fmt.Errorf("resolve AcoustID source: lock credential configuration: %w", err)
		}
		var revision string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='internal_acoustid_config_identity'`).Scan(ctx, &revision); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("resolve AcoustID source: read credential revision: %w", err)
		}
		if revision != expectedRevision {
			return fmt.Errorf("resolve AcoustID source: credential configuration changed")
		}
		var err error
		source, err = ensureAcoustIDProviderSource(ctx, tx)
		if err != nil {
			return fmt.Errorf("resolve AcoustID source: register official source: %w", err)
		}
		return nil
	})
	if err != nil {
		return ProviderSource{}, err
	}
	return source, nil
}

// BeginAcoustIDRefresh fences generation reservation against key replacement.
func (repository *ProviderCacheRepository) BeginAcoustIDRefresh(ctx context.Context, sourceID uuid.UUID, cacheKey, expectedRevision string) (ProviderRefreshTicket, error) {
	var ticket ProviderRefreshTicket
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "acoustid-config"); err != nil {
			return err
		}
		if err := lockAcoustIDCacheKeys(ctx, tx, sourceID, []string{cacheKey}, false); err != nil {
			return fmt.Errorf("begin AcoustID refresh: %w", err)
		}
		var revision string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='internal_acoustid_config_identity'`).Scan(ctx, &revision); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("begin AcoustID refresh: read credential revision: %w", err)
		}
		if revision != expectedRevision {
			return fmt.Errorf("begin AcoustID refresh: credential configuration changed")
		}
		var source ProviderSource
		if err := tx.NewRaw(`SELECT id,provider_id,configuration_identity FROM provider_source WHERE id=? FOR SHARE`, sourceID).Scan(ctx, &source); err != nil {
			return err
		}
		if source.ConfigurationIdentity != acoustIDSourceIdentity {
			return fmt.Errorf("begin AcoustID refresh: source changed")
		}
		var generation, fetchOrder int64
		if err := tx.NewRaw(`INSERT INTO provider_response_cache(provider_source_id,provider_id,cache_key,generation,fetch_order)
			VALUES(?,?,?,1,nextval('provider_fetch_order_seq')) ON CONFLICT(provider_source_id,cache_key) DO UPDATE
			SET generation=provider_response_cache.generation+1,fetch_order=nextval('provider_fetch_order_seq')
			WHERE provider_response_cache.provider_id=EXCLUDED.provider_id RETURNING generation,fetch_order`, source.ID, source.ProviderID, cacheKey).Scan(ctx, &generation, &fetchOrder); err != nil {
			return fmt.Errorf("begin AcoustID refresh: reserve generation: %w", err)
		}
		ticket = ProviderRefreshTicket{ProviderSourceID: source.ID, ProviderID: source.ProviderID, CacheKey: cacheKey, Generation: generation, ConfigurationIdentity: acoustIDSourceIdentity, FetchOrder: fetchOrder}
		return nil
	})
	return ticket, err
}

// GetCurrentAcoustIDResponse reads an accepted cache entry only under the
// captured credential revision and fixed official source identity.
func (repository *ProviderCacheRepository) GetCurrentAcoustIDResponse(ctx context.Context, sourceID uuid.UUID, cacheKey string) (*ProviderCachedResponse, error) {
	var response *ProviderCachedResponse
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "acoustid-config"); err != nil {
			return err
		}
		if err := lockAcoustIDCacheKeys(ctx, tx, sourceID, []string{cacheKey}, true); err != nil {
			return fmt.Errorf("get cached AcoustID response: %w", err)
		}
		var sourceIdentity string
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=?`, sourceID).Scan(ctx, &sourceIdentity); err != nil {
			return fmt.Errorf("get cached AcoustID response: read source: %w", err)
		}
		if sourceIdentity != acoustIDSourceIdentity {
			return fmt.Errorf("get cached AcoustID response: source configuration changed")
		}
		cached := new(ProviderCachedResponse)
		err := tx.NewRaw(`SELECT provider_source_id,provider_id,cache_key,payload,fetched_at,revision,generation,fetch_order
			FROM provider_response_cache WHERE provider_source_id=? AND cache_key=?`, sourceID, cacheKey).Scan(ctx, cached)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get cached AcoustID response: read cache: %w", err)
		}
		response = cached
		return nil
	})
	return response, err
}

// GetAcoustIDMatchingSnapshot reads accepted official-source payloads without
// binding their eligibility to the current application-key revision. A missing
// response (or a reservation with no payload) is represented as nil.
func (repository *ProviderCacheRepository) GetAcoustIDMatchingSnapshot(ctx context.Context, cacheKeys []string) (AcoustIDCachedResponseSnapshot, error) {
	snapshot := AcoustIDCachedResponseSnapshot{Responses: make(map[string]*ProviderCachedResponse, len(cacheKeys))}
	for _, key := range cacheKeys {
		if key == "" {
			return AcoustIDCachedResponseSnapshot{}, fmt.Errorf("get AcoustID matching snapshot: cache keys must be non-empty")
		}
		if _, duplicate := snapshot.Responses[key]; duplicate {
			return AcoustIDCachedResponseSnapshot{}, fmt.Errorf("get AcoustID matching snapshot: duplicate cache key")
		}
		snapshot.Responses[key] = nil
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockAcoustIDCacheKeys(ctx, tx, officialAcoustIDSourceID(), cacheKeys, true); err != nil {
			return fmt.Errorf("get AcoustID matching snapshot: %w", err)
		}
		namespaceHash := sha256.Sum256([]byte(acoustIDOfficialEndpoint))
		namespace := "acoustid:" + hex.EncodeToString(namespaceHash[:])
		var source ProviderSource
		err := tx.NewRaw(`SELECT source.id, source.provider_id, source.namespace, source.endpoint, source.configuration_identity
			FROM provider_source source JOIN provider ON provider.id=source.provider_id
			WHERE provider.code=? AND source.namespace=?`, acoustIDProviderCode, namespace).Scan(ctx, &source)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get AcoustID matching snapshot: resolve official source: %w", err)
		}
		if source.ConfigurationIdentity != acoustIDSourceIdentity || source.Endpoint != acoustIDOfficialEndpoint {
			return fmt.Errorf("get AcoustID matching snapshot: official source identity changed")
		}
		snapshot.Source = source
		if len(cacheKeys) == 0 {
			return nil
		}
		var rows []ProviderCachedResponse
		if err := tx.NewSelect().TableExpr("provider_response_cache").Column("provider_source_id", "provider_id", "cache_key", "payload", "fetched_at", "revision", "generation", "fetch_order").Where("provider_source_id = ?", source.ID).Where("cache_key IN (?)", bun.List(cacheKeys)).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("get AcoustID matching snapshot: read accepted responses: %w", err)
		}
		for i := range rows {
			row := rows[i]
			if len(row.Payload) == 0 {
				continue
			}
			digest := sha256.Sum256(row.Payload)
			revision := hex.EncodeToString(digest[:])
			row.Revision = &revision
			snapshot.Responses[row.CacheKey] = &row
		}
		return nil
	})
	if err != nil {
		return AcoustIDCachedResponseSnapshot{}, err
	}
	return snapshot, nil
}

// ApplyAcoustIDResponse stores a successful raw response only for the captured
// credential revision, source, generation, and active provider delivery.
func (repository *ProviderCacheRepository) ApplyAcoustIDResponse(ctx context.Context, ticket ProviderRefreshTicket, expectedRevision string, fetchedAt time.Time, payload []byte, fence ProviderDeliveryFence) error {
	if ticket.ProviderSourceID == uuid.Nil || ticket.ProviderID == uuid.Nil || ticket.CacheKey == "" || ticket.Generation < 1 || ticket.FetchOrder < 1 || ticket.ConfigurationIdentity != acoustIDSourceIdentity || expectedRevision == "" || fetchedAt.IsZero() || fence.OperationID == uuid.Nil || fence.Attempt < 1 || fence.RiverJobID < 1 || fence.ExecutionEpoch < 1 {
		return fmt.Errorf("apply AcoustID response: refresh and delivery fences are required")
	}
	if err := validProviderJSON(payload, true); err != nil {
		return fmt.Errorf("apply AcoustID response: invalid payload: %w", err)
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "acoustid-config"); err != nil {
			return err
		}
		if err := lockAcoustIDCacheKeys(ctx, tx, ticket.ProviderSourceID, []string{ticket.CacheKey}, false); err != nil {
			return fmt.Errorf("apply AcoustID response: %w", err)
		}
		var revision string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='internal_acoustid_config_identity'`).Scan(ctx, &revision); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("apply AcoustID response: read credential revision: %w", err)
		}
		if revision != expectedRevision {
			return fmt.Errorf("apply AcoustID response: credential configuration changed")
		}
		var kind, state, identity, key string
		var attempt int
		var jobID sql.NullInt64
		var epoch int64
		var sourceID uuid.UUID
		if err := tx.NewRaw(`SELECT kind,state,attempt,river_job_id,provider_source_id,provider_configuration_identity,provider_cache_key,provider_execution_epoch
			FROM operation WHERE id=? FOR UPDATE`, fence.OperationID).Scan(ctx, &kind, &state, &attempt, &jobID, &sourceID, &identity, &key, &epoch); err != nil {
			return err
		}
		if kind != "provider_acoustid_lookup" || state != "running" || attempt != fence.Attempt || !jobID.Valid || jobID.Int64 != fence.RiverJobID || epoch != fence.ExecutionEpoch || sourceID != ticket.ProviderSourceID || identity != expectedRevision || key != ticket.CacheKey {
			return fmt.Errorf("apply AcoustID response: operation delivery is stale")
		}
		var sourceIdentity string
		var providerID uuid.UUID
		if err := tx.NewRaw(`SELECT provider_id,configuration_identity FROM provider_source WHERE id=? FOR UPDATE`, ticket.ProviderSourceID).Scan(ctx, &providerID, &sourceIdentity); err != nil || providerID != ticket.ProviderID || sourceIdentity != acoustIDSourceIdentity {
			return fmt.Errorf("apply AcoustID response: source configuration changed")
		}
		var generation, order int64
		if err := tx.NewRaw(`SELECT generation,fetch_order FROM provider_response_cache WHERE provider_source_id=? AND cache_key=? FOR UPDATE`, ticket.ProviderSourceID, ticket.CacheKey).Scan(ctx, &generation, &order); err != nil {
			return err
		}
		if generation != ticket.Generation || order != ticket.FetchOrder {
			return fmt.Errorf("apply AcoustID response: refresh generation is stale")
		}
		_, err := tx.NewRaw(`UPDATE provider_response_cache SET payload=?::jsonb,fetched_at=?,revision=NULL WHERE provider_source_id=? AND cache_key=?`, string(payload), fetchedAt, ticket.ProviderSourceID, ticket.CacheKey).Exec(ctx)
		return err
	})
}

func NewProviderCacheRepository(db *bun.DB) *ProviderCacheRepository {
	return &ProviderCacheRepository{db: db}
}

func (repository *ProviderCacheRepository) CreateProvider(ctx context.Context, provider *Provider) error {
	if provider.ID == uuid.Nil {
		provider.ID = uuid.New()
	}
	if strings.TrimSpace(provider.Code) == "" {
		return fmt.Errorf("create provider: code is required")
	}
	if _, err := repository.db.NewInsert().Model(provider).Exec(ctx); err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	return nil
}

func (repository *ProviderCacheRepository) CreateProviderSource(ctx context.Context, source *ProviderSource) error {
	if source.ID == uuid.Nil {
		source.ID = uuid.New()
	}
	if source.ProviderID == uuid.Nil || strings.TrimSpace(source.Namespace) == "" || source.Endpoint == "" || source.ConfigurationIdentity == "" {
		return fmt.Errorf("create provider source: provider, namespace, endpoint, and configuration identity are required")
	}
	if _, err := repository.db.NewInsert().Model(source).Exec(ctx); err != nil {
		return fmt.Errorf("create provider source: %w", err)
	}
	return nil
}

// ResolveMusicBrainzSource returns the source synchronized with the current
// saved configuration. It is read-only and safe to repeat.
func (repository *ProviderCacheRepository) ResolveMusicBrainzSource(ctx context.Context, endpoint, expectedIdentity string) (ProviderSource, error) {
	if endpoint == "" || expectedIdentity == "" {
		return ProviderSource{}, fmt.Errorf("resolve MusicBrainz provider source: endpoint and configuration identity are required")
	}
	var source ProviderSource
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("resolve MusicBrainz provider source: lock configuration: %w", err)
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
			return fmt.Errorf("resolve MusicBrainz provider source: read current identity: %w", err)
		}
		if identity != expectedIdentity {
			return fmt.Errorf("resolve MusicBrainz provider source: configuration changed")
		}
		if err := tx.NewRaw(`SELECT id, provider_id, namespace, endpoint, configuration_identity
			FROM provider_source WHERE provider_id=(SELECT id FROM provider WHERE code='musicbrainz')
			AND endpoint=? AND configuration_identity=?`, endpoint, identity).Scan(ctx, &source); err != nil {
			return fmt.Errorf("resolve MusicBrainz provider source: read source: %w", err)
		}
		return nil
	})
	if err != nil {
		return ProviderSource{}, err
	}
	return source, nil
}

// BeginRefresh reserves a generation before network work starts. The repository
// performs no HTTP and holds no transaction open for the caller's request.
func (repository *ProviderCacheRepository) BeginRefresh(ctx context.Context, sourceID uuid.UUID, cacheKey, expectedConfigurationIdentity string) (ProviderRefreshTicket, error) {
	if sourceID == uuid.Nil || cacheKey == "" || expectedConfigurationIdentity == "" {
		return ProviderRefreshTicket{}, fmt.Errorf("begin provider refresh: source, key, and configuration identity are required")
	}
	var ticket ProviderRefreshTicket
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("begin provider refresh: lock configuration: %w", err)
		}
		var currentIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
			return fmt.Errorf("begin provider refresh: read current configuration: %w", err)
		}
		if currentIdentity != expectedConfigurationIdentity {
			return fmt.Errorf("begin provider refresh: configuration changed")
		}
		var source ProviderSource
		if err := tx.NewRaw(`SELECT id, provider_id, namespace, endpoint, configuration_identity
			FROM provider_source WHERE id = ? FOR SHARE`, sourceID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("begin provider refresh: read source: %w", err)
		}
		if source.ConfigurationIdentity != expectedConfigurationIdentity {
			return fmt.Errorf("begin provider refresh: source configuration changed")
		}
		var generation, fetchOrder int64
		if err := tx.NewRaw(`INSERT INTO provider_response_cache
			(provider_source_id, provider_id, cache_key, generation, fetch_order)
			VALUES (?, ?, ?, 1, nextval('provider_fetch_order_seq'))
			ON CONFLICT (provider_source_id, cache_key) DO UPDATE
			SET generation = provider_response_cache.generation + 1,
			fetch_order = nextval('provider_fetch_order_seq')
			WHERE provider_response_cache.provider_id = EXCLUDED.provider_id
			RETURNING generation, fetch_order`, sourceID, source.ProviderID, cacheKey).Scan(ctx, &generation, &fetchOrder); err != nil {
			return fmt.Errorf("begin provider refresh: reserve generation: %w", err)
		}
		ticket = ProviderRefreshTicket{ProviderSourceID: sourceID, ProviderID: source.ProviderID, CacheKey: cacheKey, Generation: generation, ConfigurationIdentity: expectedConfigurationIdentity, FetchOrder: fetchOrder}
		return nil
	})
	if err != nil {
		return ProviderRefreshTicket{}, err
	}
	return ticket, nil
}

func (repository *ProviderCacheRepository) GetCachedResponse(ctx context.Context, sourceID uuid.UUID, cacheKey string) (*ProviderCachedResponse, error) {
	if sourceID != officialAcoustIDSourceID() {
		response := new(ProviderCachedResponse)
		err := repository.db.NewRaw(`SELECT provider_source_id, provider_id, cache_key, payload, fetched_at, revision, generation, fetch_order
			FROM provider_response_cache WHERE provider_source_id = ? AND cache_key = ?`, sourceID, cacheKey).Scan(ctx, response)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("get cached provider response: %w", err)
		}
		return response, nil
	}
	var response *ProviderCachedResponse
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockAcoustIDCacheKeys(ctx, tx, sourceID, []string{cacheKey}, true); err != nil {
			return fmt.Errorf("get cached provider response: %w", err)
		}
		cached := new(ProviderCachedResponse)
		err := tx.NewRaw(`SELECT provider_source_id, provider_id, cache_key, payload, fetched_at, revision, generation, fetch_order
			FROM provider_response_cache WHERE provider_source_id = ? AND cache_key = ?`, sourceID, cacheKey).Scan(ctx, cached)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get cached provider response: %w", err)
		}
		response = cached
		return nil
	})
	return response, err
}

// GetCurrentCachedResponse is a cache-only read fenced by the current saved
// MusicBrainz configuration. It performs no refresh and cannot return a prior
// endpoint's cache after a concurrent configuration switch.
func (repository *ProviderCacheRepository) GetCurrentCachedResponse(ctx context.Context, sourceID uuid.UUID, cacheKey, expectedIdentity string) (*ProviderCachedResponse, error) {
	var response *ProviderCachedResponse
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("get current cached provider response: lock configuration: %w", err)
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
			return fmt.Errorf("get current cached provider response: read configuration: %w", err)
		}
		if identity != expectedIdentity {
			return fmt.Errorf("get current cached provider response: configuration changed")
		}
		var sourceIdentity string
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=?`, sourceID).Scan(ctx, &sourceIdentity); err != nil {
			return fmt.Errorf("get current cached provider response: read source: %w", err)
		}
		if sourceIdentity != expectedIdentity {
			return fmt.Errorf("get current cached provider response: source configuration changed")
		}
		result := new(ProviderCachedResponse)
		err := tx.NewRaw(`SELECT provider_source_id, provider_id, cache_key, payload, fetched_at, revision, generation, fetch_order
			FROM provider_response_cache WHERE provider_source_id=? AND cache_key=?`, sourceID, cacheKey).Scan(ctx, result)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get current cached provider response: read cache: %w", err)
		}
		response = result
		return nil
	})
	return response, err
}

// CaptureSuccessfulManualLookupReuse seals the exact cache response observed by
// a durable manual lookup delivery. Cache/configuration gates are acquired
// before the operation row so a concurrent refresh cannot substitute a newer
// response between the cache read and receipt capture.
func (repository *ProviderCacheRepository) CaptureSuccessfulManualLookupReuse(ctx context.Context, fence ProviderDeliveryFence, expected *ProviderCachedResponse) (bool, error) {
	if expected == nil || fence.OperationID == uuid.Nil || fence.Attempt < 1 || fence.RiverJobID < 1 || fence.ExecutionEpoch < 1 || expected.ProviderSourceID == uuid.Nil || expected.CacheKey == "" {
		return false, fmt.Errorf("capture manual lookup cache reuse: delivery and cached response are required")
	}
	var manual bool
	if err := repository.db.NewRaw(`SELECT EXISTS(SELECT 1 FROM manual_matching_admission WHERE lookup_operation_id=? AND cache_key=?)`, fence.OperationID, expected.CacheKey).Scan(ctx, &manual); err != nil {
		return false, fmt.Errorf("capture manual lookup cache reuse: check admission: %w", err)
	}
	if !manual {
		return false, nil
	}
	payloadDigest := sha256.Sum256(expected.Payload)
	var captured bool
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("capture manual lookup cache reuse: lock configuration: %w", err)
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
			return fmt.Errorf("capture manual lookup cache reuse: read configuration: %w", err)
		}
		var source ProviderSource
		if err := tx.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=? FOR SHARE`, expected.ProviderSourceID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("capture manual lookup cache reuse: read source: %w", err)
		}
		if identity == "" || identity != source.ConfigurationIdentity {
			return fmt.Errorf("capture manual lookup cache reuse: provider configuration changed")
		}
		current := new(ProviderCachedResponse)
		if err := tx.NewRaw(`SELECT provider_source_id,provider_id,cache_key,payload,fetched_at,revision,generation,fetch_order
			FROM provider_response_cache WHERE provider_source_id=? AND cache_key=? FOR SHARE`, source.ID, expected.CacheKey).Scan(ctx, current); err != nil {
			return fmt.Errorf("capture manual lookup cache reuse: read cached response: %w", err)
		}
		if current.ProviderID != expected.ProviderID || current.CacheKey != expected.CacheKey || current.Revision != nil && expected.Revision != nil && *current.Revision != *expected.Revision || (current.Revision == nil) != (expected.Revision == nil) || !equalPayload(current.Payload, expected.Payload) {
			return fmt.Errorf("capture manual lookup cache reuse: cached response changed")
		}
		var kind, state, configurationIdentity, cacheKey string
		var attempt int
		var executionEpoch int64
		var riverJobID sql.NullInt64
		var sourceID uuid.UUID
		if err := tx.NewRaw(`SELECT kind,state,attempt,river_job_id,provider_source_id,provider_configuration_identity,provider_cache_key,provider_execution_epoch
			FROM operation WHERE id=? FOR UPDATE`, fence.OperationID).Scan(ctx, &kind, &state, &attempt, &riverJobID, &sourceID, &configurationIdentity, &cacheKey, &executionEpoch); err != nil {
			return fmt.Errorf("capture manual lookup cache reuse: read delivery: %w", err)
		}
		if kind != "provider_release_lookup" || state != "running" || attempt != fence.Attempt || !riverJobID.Valid || riverJobID.Int64 != fence.RiverJobID || executionEpoch != fence.ExecutionEpoch || sourceID != source.ID || configurationIdentity != identity || cacheKey != expected.CacheKey {
			return fmt.Errorf("capture manual lookup cache reuse: delivery is stale")
		}
		var requestID, selectedMBID uuid.UUID
		if err := tx.NewRaw(`SELECT request_id,selected_release_mbid FROM manual_matching_admission WHERE lookup_operation_id=? AND cache_key=?`, fence.OperationID, expected.CacheKey).Scan(ctx, &requestID, &selectedMBID); err != nil {
			return fmt.Errorf("capture manual lookup cache reuse: read manual admission: %w", err)
		}
		if expected.CacheKey != "release-lookup:"+strings.ToLower(selectedMBID.String()) || len(expected.Payload) == 0 {
			return fmt.Errorf("capture manual lookup cache reuse: selected release response is invalid")
		}
		snapshot, err := readMusicBrainzMatchingSnapshot(ctx, tx, source.Endpoint, identity, []string{selectedMBID.String()}, []string{expected.CacheKey}, false)
		if err != nil {
			return fmt.Errorf("capture manual lookup cache reuse: read accepted projection: %w", err)
		}
		if len(snapshot.Releases) != 1 || snapshot.Releases[0].ProviderKey != selectedMBID.String() || !snapshot.Releases[0].Complete {
			return fmt.Errorf("capture manual lookup cache reuse: accepted release projection is incomplete")
		}
		accepted := snapshot.Responses[expected.CacheKey]
		if accepted == nil || !equalPayload(accepted.Payload, expected.Payload) || !equalOptionalString(accepted.Revision, expected.Revision) {
			return fmt.Errorf("capture manual lookup cache reuse: accepted response changed")
		}
		projectionDigest, err := hex.DecodeString(snapshot.AcceptedProjectionRevision)
		if err != nil || len(projectionDigest) != sha256.Size {
			return fmt.Errorf("capture manual lookup cache reuse: accepted projection revision is invalid")
		}
		result, err := tx.NewRaw(`INSERT INTO manual_matching_lookup_seal
			(request_id,lookup_operation_id,provider_source_id,configuration_identity,cache_key,payload_sha256,cache_revision,projection_sha256)
			VALUES(?,?,?,?,?,?,NULLIF(?,''),?) ON CONFLICT(request_id) DO NOTHING`, requestID, fence.OperationID, source.ID, identity,
			expected.CacheKey, payloadDigest[:], optionalStringValue(expected.Revision), projectionDigest).Exec(ctx)
		if err != nil {
			return fmt.Errorf("capture manual lookup cache reuse: save accepted receipt: %w", err)
		}
		if n, err := result.RowsAffected(); err == nil && n == 1 {
			captured = true
		}
		var storedOperation uuid.UUID
		var storedSource uuid.UUID
		var storedIdentity, storedKey string
		var storedPayload, storedProjection []byte
		var storedRevision sql.NullString
		if err := tx.NewRaw(`SELECT lookup_operation_id,provider_source_id,configuration_identity,cache_key,payload_sha256,cache_revision,projection_sha256
			FROM manual_matching_lookup_seal WHERE request_id=?`, requestID).Scan(ctx, &storedOperation, &storedSource, &storedIdentity, &storedKey, &storedPayload, &storedRevision, &storedProjection); err != nil {
			return fmt.Errorf("capture manual lookup cache reuse: verify accepted receipt: %w", err)
		}
		if storedOperation != fence.OperationID || storedSource != source.ID || storedIdentity != identity || storedKey != expected.CacheKey || !equalPayload(storedPayload, payloadDigest[:]) || !equalOptionalString(nullStringPointer(storedRevision), expected.Revision) || !equalPayload(storedProjection, projectionDigest) {
			return fmt.Errorf("capture manual lookup cache reuse: another response is already captured")
		}
		return nil
	})
	return captured, err
}

func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func equalPayload(left, right []byte) bool {
	return string(left) == string(right)
}

// ApplySuccessfulResponse commits the successful raw response and its normalized
// graph only while both the reserved generation and source configuration remain
// current. Failed requests must not call this method.
func (repository *ProviderCacheRepository) ApplySuccessfulResponse(ctx context.Context, response ProviderSuccessfulResponse) error {
	ticket := response.Ticket
	if ticket.ProviderSourceID == uuid.Nil || ticket.ProviderID == uuid.Nil || ticket.CacheKey == "" || ticket.Generation < 1 || ticket.FetchOrder < 1 || ticket.ConfigurationIdentity == "" || response.FetchedAt.IsZero() {
		return fmt.Errorf("apply successful provider response: refresh ticket and fetch time are required")
	}
	if err := validProviderJSON(response.Payload, true); err != nil {
		return fmt.Errorf("apply successful provider response: invalid payload: %w", err)
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("apply successful provider response: lock configuration: %w", err)
		}
		var currentIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
			return fmt.Errorf("apply successful provider response: read current configuration: %w", err)
		}
		if currentIdentity != ticket.ConfigurationIdentity {
			return fmt.Errorf("apply successful provider response: configuration changed")
		}
		if response.Delivery != nil {
			fence := response.Delivery
			if fence.OperationID == uuid.Nil || fence.Attempt < 1 || fence.RiverJobID < 1 || fence.ExecutionEpoch < 1 {
				return fmt.Errorf("apply successful provider response: invalid operation delivery fence")
			}
			var kind, state, configurationIdentity, cacheKey string
			var attempt int
			var executionEpoch int64
			var riverJobID sql.NullInt64
			var sourceID uuid.UUID
			if err := tx.NewRaw(`SELECT kind, state, attempt, river_job_id, provider_source_id, provider_configuration_identity, provider_cache_key, provider_execution_epoch
					FROM operation WHERE id=? FOR UPDATE`, fence.OperationID).Scan(ctx, &kind, &state, &attempt, &riverJobID, &sourceID, &configurationIdentity, &cacheKey, &executionEpoch); err != nil {
				return fmt.Errorf("apply successful provider response: read operation delivery: %w", err)
			}
			if !isProviderOperationKind(kind) || state != "running" || attempt != fence.Attempt || !riverJobID.Valid || riverJobID.Int64 != fence.RiverJobID || executionEpoch != fence.ExecutionEpoch ||
				sourceID != ticket.ProviderSourceID || configurationIdentity != ticket.ConfigurationIdentity || cacheKey != ticket.CacheKey {
				return fmt.Errorf("apply successful provider response: operation delivery is stale")
			}
		}
		var source ProviderSource
		if err := tx.NewRaw(`SELECT id, provider_id, endpoint, configuration_identity FROM provider_source WHERE id = ? FOR UPDATE`, ticket.ProviderSourceID).Scan(ctx, &source); err != nil {
			return fmt.Errorf("apply successful provider response: read source: %w", err)
		}
		if source.ProviderID != ticket.ProviderID || source.ConfigurationIdentity != ticket.ConfigurationIdentity {
			return fmt.Errorf("apply successful provider response: source configuration changed")
		}
		var currentGeneration, currentFetchOrder int64
		if err := tx.NewRaw(`SELECT generation, fetch_order FROM provider_response_cache
			WHERE provider_source_id = ? AND cache_key = ? FOR UPDATE`, ticket.ProviderSourceID, ticket.CacheKey).Scan(ctx, &currentGeneration, &currentFetchOrder); err != nil {
			return fmt.Errorf("apply successful provider response: read refresh generation: %w", err)
		}
		if currentGeneration != ticket.Generation || currentFetchOrder != ticket.FetchOrder {
			return fmt.Errorf("apply successful provider response: refresh generation is stale")
		}
		if _, err := tx.NewRaw(`UPDATE provider_response_cache SET payload = ?::jsonb, fetched_at = ?, revision = NULLIF(?, '')
				WHERE provider_source_id = ? AND cache_key = ? AND generation = ? AND fetch_order = ?`, string(response.Payload), response.FetchedAt, response.Revision, ticket.ProviderSourceID, ticket.CacheKey, ticket.Generation, ticket.FetchOrder).Exec(ctx); err != nil {
			return fmt.Errorf("apply successful provider response: save response: %w", err)
		}
		if err := applyProviderSourceGraph(ctx, tx, ticket, response.Graph); err != nil {
			return fmt.Errorf("apply successful provider response: save source projection: %w", err)
		}
		if err := applyProviderGraph(ctx, tx, ticket, response.Graph); err != nil {
			return fmt.Errorf("apply successful provider response: save graph: %w", err)
		}
		if response.Delivery != nil {
			var selectedMBID uuid.UUID
			manualErr := tx.NewRaw(`SELECT admission.selected_release_mbid FROM manual_matching_admission admission
				JOIN operation ON operation.id=admission.lookup_operation_id
				WHERE admission.lookup_operation_id=? AND admission.cache_key=? AND operation.kind='provider_release_lookup'`, response.Delivery.OperationID, ticket.CacheKey).Scan(ctx, &selectedMBID)
			if manualErr != nil && !errors.Is(manualErr, sql.ErrNoRows) {
				return fmt.Errorf("apply successful provider response: resolve manual lookup: %w", manualErr)
			}
			if manualErr == nil {
				if ticket.CacheKey != "release-lookup:"+strings.ToLower(selectedMBID.String()) || !response.Graph.Complete {
					return fmt.Errorf("apply successful provider response: manual release projection is incomplete or mismatched")
				}
				projectionSnapshot, err := readMusicBrainzMatchingSnapshot(ctx, tx, source.Endpoint, ticket.ConfigurationIdentity,
					[]string{selectedMBID.String()}, []string{ticket.CacheKey}, false)
				if err != nil {
					return fmt.Errorf("apply successful provider response: read accepted manual projection: %w", err)
				}
				if len(projectionSnapshot.Releases) != 1 || projectionSnapshot.Releases[0].ProviderKey != selectedMBID.String() || !projectionSnapshot.Releases[0].Complete {
					return fmt.Errorf("apply successful provider response: accepted manual release projection is incomplete")
				}
				acceptedResponse := projectionSnapshot.Responses[ticket.CacheKey]
				if acceptedResponse == nil || len(acceptedResponse.Payload) == 0 {
					return fmt.Errorf("apply successful provider response: accepted manual response is missing")
				}
				projectionDigest, err := hex.DecodeString(projectionSnapshot.AcceptedProjectionRevision)
				if err != nil || len(projectionDigest) != sha256.Size {
					return fmt.Errorf("apply successful provider response: accepted manual projection revision is invalid")
				}
				payloadDigest := sha256.Sum256(acceptedResponse.Payload)
				if _, err := tx.NewRaw(`INSERT INTO manual_matching_lookup_seal
					(request_id,lookup_operation_id,provider_source_id,configuration_identity,cache_key,payload_sha256,cache_revision,projection_sha256)
					SELECT admission.request_id,admission.lookup_operation_id,?,?,?, ?,NULLIF(?,''),?
					FROM manual_matching_admission admission
					WHERE admission.lookup_operation_id=? AND admission.cache_key=? AND EXISTS (
						SELECT 1 FROM operation WHERE id=admission.lookup_operation_id AND kind='provider_release_lookup')
					ON CONFLICT (request_id) DO UPDATE SET lookup_operation_id=EXCLUDED.lookup_operation_id,
					provider_source_id=EXCLUDED.provider_source_id,configuration_identity=EXCLUDED.configuration_identity,
					cache_key=EXCLUDED.cache_key,payload_sha256=EXCLUDED.payload_sha256,cache_revision=EXCLUDED.cache_revision,
					projection_sha256=EXCLUDED.projection_sha256
					WHERE manual_matching_lookup_seal.sealed_at IS NULL`, ticket.ProviderSourceID, ticket.ConfigurationIdentity, ticket.CacheKey,
					payloadDigest[:], response.Revision, projectionDigest, response.Delivery.OperationID, ticket.CacheKey).Exec(ctx); err != nil {
					return fmt.Errorf("apply successful provider response: capture manual lookup response: %w", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func applyProviderSourceGraph(ctx context.Context, tx bun.Tx, ticket ProviderRefreshTicket, graph ProviderGraph) error {
	for _, entity := range graph.Entities {
		if entity.ProviderKey == "" || (entity.Kind != "artist" && entity.Kind != "release" && entity.Kind != "recording") || entity.Authority < 0 {
			return fmt.Errorf("source entity kind, provider key, and authority are required")
		}
		fields, err := providerJSON(entity.Fields, true)
		if err != nil {
			return fmt.Errorf("source entity fields: %w", err)
		}
		raw, err := providerJSON(entity.RawSource, false)
		if err != nil {
			return fmt.Errorf("source entity source: %w", err)
		}
		_, err = tx.NewRaw(`INSERT INTO provider_source_entity
			(provider_source_id, provider_id, entity_kind, provider_key, name, fields, field_evidence, raw_source, authority, complete, fetch_order)
			VALUES (?, ?, ?, ?, '', '{}'::jsonb, '{}'::jsonb, ?::jsonb, ?, false, ?)
			ON CONFLICT (provider_source_id, entity_kind, provider_key) DO NOTHING`,
			ticket.ProviderSourceID, ticket.ProviderID, entity.Kind, entity.ProviderKey, raw, entity.Authority, ticket.FetchOrder).Exec(ctx)
		if err != nil {
			return err
		}
		var currentFields, currentEvidence string
		if err := tx.NewRaw(`SELECT fields, field_evidence FROM provider_source_entity
			WHERE provider_source_id=? AND entity_kind=? AND provider_key=? FOR UPDATE`, ticket.ProviderSourceID, entity.Kind, entity.ProviderKey).Scan(ctx, &currentFields, &currentEvidence); err != nil {
			return err
		}
		storedFields := map[string]json.RawMessage{}
		storedEvidence := map[string]ProviderFieldEvidence{}
		if err := json.Unmarshal([]byte(currentFields), &storedFields); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(currentEvidence), &storedEvidence); err != nil {
			return err
		}
		incomingFields := map[string]json.RawMessage{}
		if err := json.Unmarshal(json.RawMessage(fields), &incomingFields); err != nil {
			return err
		}
		changed := false
		applyField := func(key string, value json.RawMessage, supplied bool) {
			if !supplied {
				return
			}
			incomingAuthority := entity.Authority
			if evidence, ok := entity.FieldEvidence[key]; ok {
				incomingAuthority = evidence.Authority
			}
			previous, exists := storedEvidence[key]
			if !exists || incomingAuthority > previous.Authority ||
				(incomingAuthority == previous.Authority && ticket.FetchOrder > previous.FetchOrder) {
				storedFields[key] = value
				storedEvidence[key] = ProviderFieldEvidence{Authority: incomingAuthority, FetchOrder: ticket.FetchOrder}
				changed = true
			}
		}
		for key, value := range incomingFields {
			applyField(key, value, true)
		}
		if entity.Name != "" {
			applyField("name", json.RawMessage(strconv.Quote(entity.Name)), true)
		}
		if entity.Complete {
			for _, key := range []string{"__tracks", "__credits"} {
				previous, exists := storedEvidence[key]
				if !exists || entity.Authority > previous.Authority ||
					(entity.Authority == previous.Authority && ticket.FetchOrder > previous.FetchOrder) {
					storedEvidence[key] = ProviderFieldEvidence{Authority: entity.Authority, FetchOrder: ticket.FetchOrder}
					changed = true
				}
			}
		}
		encodedFields, err := json.Marshal(storedFields)
		if err != nil {
			return err
		}
		encodedEvidence, err := json.Marshal(storedEvidence)
		if err != nil {
			return err
		}
		if changed {
			if _, err := tx.NewRaw(`UPDATE provider_source_entity SET fields=?::jsonb, field_evidence=?::jsonb,
				name=COALESCE(NULLIF((?::jsonb->>'name'), ''), name), raw_source=?::jsonb,
				authority=GREATEST(authority, ?), complete=complete OR ?, fetch_order=GREATEST(fetch_order, ?), updated_at=now()
				WHERE provider_source_id=? AND entity_kind=? AND provider_key=?`, string(encodedFields), string(encodedEvidence), string(encodedFields), raw,
				entity.Authority, entity.Complete, ticket.FetchOrder, ticket.ProviderSourceID, entity.Kind, entity.ProviderKey).Exec(ctx); err != nil {
				return err
			}
		} else if entity.Complete {
			if _, err := tx.NewRaw(`UPDATE provider_source_entity SET complete=true WHERE provider_source_id=? AND entity_kind=? AND provider_key=?`, ticket.ProviderSourceID, entity.Kind, entity.ProviderKey).Exec(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func validProviderJSON(value json.RawMessage, allowArray bool) error {
	if len(value) == 0 || !json.Valid(value) {
		return fmt.Errorf("JSON value is required")
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return err
	}
	switch decoded.(type) {
	case map[string]any:
		return nil
	case []any:
		if allowArray {
			return nil
		}
	}
	return fmt.Errorf("expected JSON object or array")
}

func providerJSON(value json.RawMessage, objectOnly bool) (string, error) {
	if len(value) == 0 {
		return "{}", nil
	}
	if err := validProviderJSON(value, !objectOnly); err != nil {
		return "", err
	}
	return string(value), nil
}

func applyProviderGraph(ctx context.Context, tx bun.Tx, ticket ProviderRefreshTicket, graph ProviderGraph) error {
	providerID := ticket.ProviderID
	artistIDs := make(map[uuid.UUID]uuid.UUID, len(graph.Artists))
	releaseIDs := make(map[uuid.UUID]uuid.UUID, len(graph.Releases))
	recordingIDs := make(map[uuid.UUID]uuid.UUID, len(graph.Recordings))
	for index := range graph.Artists {
		artist := &graph.Artists[index]
		if artist.ID == uuid.Nil || artist.ProviderKey == "" || artist.Name == "" {
			return fmt.Errorf("artist id, provider key, and name are required")
		}
		metadata, err := providerJSON(artist.Metadata, true)
		if err != nil {
			return fmt.Errorf("artist metadata: %w", err)
		}
		raw, err := providerJSON(artist.RawSource, false)
		if err != nil {
			return fmt.Errorf("artist source: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_artist(id, provider_id, provider_key, name, metadata, raw_source)
			VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb)
			ON CONFLICT (provider_id, provider_key) DO UPDATE SET
			name=COALESCE(NULLIF(provider_artist.name, ''), EXCLUDED.name),
			metadata=CASE WHEN ? THEN EXCLUDED.metadata ELSE provider_artist.metadata END,
			raw_source=CASE WHEN ? THEN EXCLUDED.raw_source ELSE provider_artist.raw_source END, updated_at=now()`,
			artist.ID, providerID, artist.ProviderKey, artist.Name, metadata, raw, len(artist.Metadata) > 0, len(artist.RawSource) > 0).Exec(ctx); err != nil {
			return err
		}
		inputID := artist.ID
		if err := tx.NewRaw(`SELECT id FROM provider_artist WHERE provider_id=? AND provider_key=?`, providerID, artist.ProviderKey).Scan(ctx, &artist.ID); err != nil {
			return err
		}
		artistIDs[inputID] = artist.ID
	}
	for index := range graph.Releases {
		release := &graph.Releases[index]
		if release.ID == uuid.Nil || release.ProviderKey == "" || release.Title == "" {
			return fmt.Errorf("release id, provider key, and title are required")
		}
		metadata, err := providerJSON(release.Metadata, true)
		if err != nil {
			return fmt.Errorf("release metadata: %w", err)
		}
		raw, err := providerJSON(release.RawSource, false)
		if err != nil {
			return fmt.Errorf("release source: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_release(id, provider_id, provider_key, title, metadata, raw_source)
			VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb)
			ON CONFLICT (provider_id, provider_key) DO UPDATE SET
			title=COALESCE(NULLIF(provider_release.title, ''), EXCLUDED.title),
			metadata=CASE WHEN ? THEN EXCLUDED.metadata ELSE provider_release.metadata END,
			raw_source=CASE WHEN ? THEN EXCLUDED.raw_source ELSE provider_release.raw_source END, updated_at=now()`,
			release.ID, providerID, release.ProviderKey, release.Title, metadata, raw, len(release.Metadata) > 0, len(release.RawSource) > 0).Exec(ctx); err != nil {
			return err
		}
		inputID := release.ID
		if err := tx.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, providerID, release.ProviderKey).Scan(ctx, &release.ID); err != nil {
			return err
		}
		releaseIDs[inputID] = release.ID
	}
	for index := range graph.Recordings {
		recording := &graph.Recordings[index]
		if recording.ID == uuid.Nil || recording.ProviderKey == "" || recording.Title == "" {
			return fmt.Errorf("recording id, provider key, and title are required")
		}
		metadata, err := providerJSON(recording.Metadata, true)
		if err != nil {
			return fmt.Errorf("recording metadata: %w", err)
		}
		raw, err := providerJSON(recording.RawSource, false)
		if err != nil {
			return fmt.Errorf("recording source: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_recording(id, provider_id, provider_key, title, metadata, raw_source)
			VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb)
			ON CONFLICT (provider_id, provider_key) DO UPDATE SET
			title=COALESCE(NULLIF(provider_recording.title, ''), EXCLUDED.title),
			metadata=CASE WHEN ? THEN EXCLUDED.metadata ELSE provider_recording.metadata END,
			raw_source=CASE WHEN ? THEN EXCLUDED.raw_source ELSE provider_recording.raw_source END, updated_at=now()`,
			recording.ID, providerID, recording.ProviderKey, recording.Title, metadata, raw, len(recording.Metadata) > 0, len(recording.RawSource) > 0).Exec(ctx); err != nil {
			return err
		}
		inputID := recording.ID
		if err := tx.NewRaw(`SELECT id FROM provider_recording WHERE provider_id=? AND provider_key=?`, providerID, recording.ProviderKey).Scan(ctx, &recording.ID); err != nil {
			return err
		}
		recordingIDs[inputID] = recording.ID
	}
	for index := range graph.Credits {
		credit := &graph.Credits[index]
		if id, ok := releaseIDs[credit.ReleaseID]; ok {
			credit.ReleaseID = id
		}
		if id, ok := artistIDs[credit.ArtistID]; ok {
			credit.ArtistID = id
		}
	}
	for index := range graph.Tracks {
		track := &graph.Tracks[index]
		if id, ok := releaseIDs[track.ReleaseID]; ok {
			track.ReleaseID = id
		}
		if id, ok := recordingIDs[track.RecordingID]; ok {
			track.RecordingID = id
		}
	}
	type acceptedCollection struct {
		releaseID uuid.UUID
		authority int
		credits   bool
		tracks    bool
	}
	acceptedCollections := make(map[uuid.UUID]acceptedCollection)
	for _, entity := range graph.Entities {
		if entity.Kind == "release" {
			releaseID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("musicbrainz:release:"+strings.ToLower(entity.ProviderKey)))
			var raw string
			if err := tx.NewRaw(`SELECT field_evidence FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, ticket.ProviderSourceID, entity.ProviderKey).Scan(ctx, &raw); err != nil {
				return err
			}
			var evidence map[string]ProviderFieldEvidence
			if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
				return err
			}
			credits := evidence["__credits"]
			tracks := evidence["__tracks"]
			acceptedCollections[releaseID] = acceptedCollection{
				releaseID: releaseID,
				authority: entity.Authority,
				credits:   credits.Authority == entity.Authority && credits.FetchOrder == ticket.FetchOrder,
				tracks:    tracks.Authority == entity.Authority && tracks.FetchOrder == ticket.FetchOrder,
			}
		}
	}
	// Keep the global track table as an identity registry only. Collection order
	// and presence belong to the source projection below, never to this row.
	for _, track := range graph.Tracks {
		if track.ID == uuid.Nil || track.ReleaseID == uuid.Nil || track.RecordingID == uuid.Nil || track.Medium < 0 || track.Position < 0 {
			return fmt.Errorf("valid release track position is required")
		}
		if id, ok := releaseIDs[track.ReleaseID]; ok {
			track.ReleaseID = id
		}
		if id, ok := recordingIDs[track.RecordingID]; ok {
			track.RecordingID = id
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_release_track(id, provider_id, release_id, recording_id, medium, position, displayed_number, present, authority, fetch_order)
			VALUES (?, ?, ?, ?, NULL, NULL, ?, false, 0, 0) ON CONFLICT (provider_id, id) DO NOTHING`,
			track.ID, providerID, track.ReleaseID, track.RecordingID, track.DisplayedNumber).Exec(ctx); err != nil {
			return err
		}
		var identity struct {
			ReleaseID   uuid.UUID `bun:"release_id"`
			RecordingID uuid.UUID `bun:"recording_id"`
		}
		if err := tx.NewRaw(`SELECT release_id, recording_id FROM provider_release_track WHERE provider_id=? AND id=?`, providerID, track.ID).Scan(ctx, &identity); err != nil {
			return err
		}
		if identity.ReleaseID != track.ReleaseID || identity.RecordingID != track.RecordingID {
			return fmt.Errorf("provider track identity conflicts with its existing provider key")
		}
	}
	for _, credit := range graph.Credits {
		if credit.ReleaseID == uuid.Nil || credit.ArtistID == uuid.Nil || credit.Position < 0 {
			return fmt.Errorf("valid ordered release credit is required")
		}
	}
	for _, accepted := range acceptedCollections {
		if accepted.credits {
			if _, err := tx.NewRaw(`DELETE FROM provider_source_release_artist_credit WHERE provider_source_id=? AND release_id=?`, ticket.ProviderSourceID, accepted.releaseID).Exec(ctx); err != nil {
				return err
			}
		}
		if accepted.tracks {
			if _, err := tx.NewRaw(`DELETE FROM provider_source_release_track WHERE provider_source_id=? AND release_id=?`, ticket.ProviderSourceID, accepted.releaseID).Exec(ctx); err != nil {
				return err
			}
		}
	}
	for _, credit := range graph.Credits {
		if id, ok := releaseIDs[credit.ReleaseID]; ok {
			credit.ReleaseID = id
		}
		if id, ok := artistIDs[credit.ArtistID]; ok {
			credit.ArtistID = id
		}
		accepted, ok := acceptedCollections[credit.ReleaseID]
		if !ok || !accepted.credits {
			continue
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_source_release_artist_credit(provider_source_id, provider_id, release_id, position, artist_id, name, name_join_phrase, authority, fetch_order)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, ticket.ProviderSourceID, providerID, credit.ReleaseID, credit.Position, credit.ArtistID, credit.Name, credit.NameJoinPhrase, accepted.authority, ticket.FetchOrder).Exec(ctx); err != nil {
			return err
		}
	}
	for _, track := range graph.Tracks {
		if id, ok := releaseIDs[track.ReleaseID]; ok {
			track.ReleaseID = id
		}
		if id, ok := recordingIDs[track.RecordingID]; ok {
			track.RecordingID = id
		}
		accepted, ok := acceptedCollections[track.ReleaseID]
		if !ok || !accepted.tracks {
			continue
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_source_release_track(provider_source_id, provider_id, track_id, release_id, recording_id, medium, position, displayed_number, authority, fetch_order)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, ticket.ProviderSourceID, providerID, track.ID, track.ReleaseID, track.RecordingID, track.Medium, track.Position, track.DisplayedNumber, accepted.authority, ticket.FetchOrder).Exec(ctx); err != nil {
			return err
		}
	}
	// Preserve the original global collection behavior for legacy graph callers
	// that do not supply source entities. Normalized provider responses always
	// use the source-scoped path above.
	if len(graph.Entities) == 0 {
		legacyRank := 0
		if graph.Complete {
			legacyRank = 32767
		}
		if graph.Complete {
			for _, release := range graph.Releases {
				if _, err := tx.NewRaw(`UPDATE provider_release_track SET present=false, medium=NULL, position=NULL, authority=?, fetch_order=? WHERE provider_id=? AND release_id=?
					AND (authority < ? OR (authority = ? AND fetch_order < ?))`, legacyRank, ticket.FetchOrder, providerID, release.ID, legacyRank, legacyRank, ticket.FetchOrder).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewRaw(`UPDATE provider_release_artist_credit SET present=false, authority=?, fetch_order=? WHERE provider_id=? AND release_id=?
					AND (authority < ? OR (authority = ? AND fetch_order < ?))`, legacyRank, ticket.FetchOrder, providerID, release.ID, legacyRank, legacyRank, ticket.FetchOrder).Exec(ctx); err != nil {
					return err
				}
			}
			if _, err := tx.NewRaw(`SET CONSTRAINTS ALL DEFERRED`).Exec(ctx); err != nil {
				return err
			}
		}
		for _, credit := range graph.Credits {
			if _, err := tx.NewRaw(`INSERT INTO provider_release_artist_credit(provider_id, release_id, position, artist_id, name_join_phrase, present, authority, fetch_order)
				VALUES (?, ?, ?, ?, ?, true, ?, ?) ON CONFLICT (provider_id, release_id, position) DO UPDATE
				SET artist_id=EXCLUDED.artist_id, name_join_phrase=EXCLUDED.name_join_phrase, present=true, authority=EXCLUDED.authority, fetch_order=EXCLUDED.fetch_order
				WHERE EXCLUDED.authority > provider_release_artist_credit.authority OR (EXCLUDED.authority = provider_release_artist_credit.authority AND (EXCLUDED.fetch_order > provider_release_artist_credit.fetch_order OR (EXCLUDED.fetch_order = provider_release_artist_credit.fetch_order AND NOT provider_release_artist_credit.present)))`,
				providerID, credit.ReleaseID, credit.Position, credit.ArtistID, credit.NameJoinPhrase, legacyRank, ticket.FetchOrder).Exec(ctx); err != nil {
				return err
			}
		}
		for _, track := range graph.Tracks {
			if _, err := tx.NewRaw(`INSERT INTO provider_release_track(id, provider_id, release_id, recording_id, medium, position, displayed_number, present, authority, fetch_order)
				VALUES (?, ?, ?, ?, ?, ?, ?, true, ?, ?) ON CONFLICT (provider_id, id) DO UPDATE SET
				release_id=EXCLUDED.release_id, recording_id=EXCLUDED.recording_id, medium=EXCLUDED.medium, position=EXCLUDED.position,
				displayed_number=EXCLUDED.displayed_number, present=true, authority=EXCLUDED.authority, fetch_order=EXCLUDED.fetch_order
				WHERE EXCLUDED.authority > provider_release_track.authority OR (EXCLUDED.authority = provider_release_track.authority AND (EXCLUDED.fetch_order > provider_release_track.fetch_order OR (EXCLUDED.fetch_order = provider_release_track.fetch_order AND NOT provider_release_track.present)))`,
				track.ID, providerID, track.ReleaseID, track.RecordingID, track.Medium, track.Position, track.DisplayedNumber, legacyRank, ticket.FetchOrder).Exec(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
