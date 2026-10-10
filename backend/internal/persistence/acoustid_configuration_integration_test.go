package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestAcoustIDCredentialFenceWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSettingsRepository(database)

	first := "opaque-test-key-one"
	if err := repository.SetOrDeleteAcoustIDCredential(ctx, &first); err != nil {
		t.Fatalf("save first key: %v", err)
	}
	key, present, firstRevision, err := repository.ReadAcoustIDCredential(ctx)
	if err != nil || !present || key != first || firstRevision == "" {
		t.Fatalf("first credential snapshot = (%q, %t, %q), err=%v", key, present, firstRevision, err)
	}
	sourceID, providerID := assertAcoustIDRegistration(t, ctx, database)
	cacheKey := "acoustid-lookup-v1:fixture"
	if _, err := database.NewRaw(`INSERT INTO provider_response_cache(provider_source_id,provider_id,cache_key,payload,fetched_at,revision,generation,fetch_order)
		VALUES(?,?,?,?,now(),'accepted-response-revision',1,7)`, sourceID, providerID, cacheKey, `{"available":true,"results":[]}`).Exec(ctx); err != nil {
		t.Fatalf("seed accepted AcoustID response: %v", err)
	}

	// Replacing with identical content is a no-op for the fence identity.
	if err := repository.SetOrDeleteAcoustIDCredential(ctx, &first); err != nil {
		t.Fatalf("save unchanged key: %v", err)
	}
	_, _, unchangedRevision, err := repository.ReadAcoustIDCredential(ctx)
	if err != nil || unchangedRevision != firstRevision {
		t.Fatalf("unchanged key revision = %q, err=%v; want %q", unchangedRevision, err, firstRevision)
	}

	second := "opaque-test-key-two"
	if err := repository.SetOrDeleteAcoustIDCredential(ctx, &second); err != nil {
		t.Fatalf("replace key: %v", err)
	}
	key, present, secondRevision, err := repository.ReadAcoustIDCredential(ctx)
	if err != nil || !present || key != second || secondRevision == firstRevision {
		t.Fatalf("replacement snapshot = (%q, %t, %q), err=%v", key, present, secondRevision, err)
	}
	secondSourceID, _ := assertAcoustIDRegistration(t, ctx, database)
	if secondSourceID != sourceID {
		t.Fatalf("source changed across key replacement: %s -> %s", sourceID, secondSourceID)
	}
	assertAcoustIDCachePayload(t, ctx, database, sourceID, cacheKey, `{"results": [], "available": true}`, "accepted-response-revision", 1, 7)

	if err := repository.SetOrDeleteAcoustIDCredential(ctx, nil); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	key, present, deletedRevision, err := repository.ReadAcoustIDCredential(ctx)
	if err != nil || present || key != "" || deletedRevision == secondRevision {
		t.Fatalf("deleted snapshot = (%q, %t, %q), err=%v", key, present, deletedRevision, err)
	}
	deletedSourceID, _ := assertAcoustIDRegistration(t, ctx, database)
	if deletedSourceID != sourceID {
		t.Fatalf("source changed across key deletion: %s -> %s", sourceID, deletedSourceID)
	}
	assertAcoustIDCachePayload(t, ctx, database, sourceID, cacheKey, `{"results": [], "available": true}`, "accepted-response-revision", 1, 7)

	// Repeated deletion does not advance the revision when no key exists.
	if err := repository.SetOrDeleteAcoustIDCredential(ctx, nil); err != nil {
		t.Fatalf("repeat deletion: %v", err)
	}
	_, _, repeatedDeleteRevision, err := repository.ReadAcoustIDCredential(ctx)
	if err != nil || repeatedDeleteRevision != deletedRevision {
		t.Fatalf("repeat-delete revision = %q, err=%v; want %q", repeatedDeleteRevision, err, deletedRevision)
	}
}

func TestAcoustIDSameKeySaveRepairsOnlyExplicitly(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSettingsRepository(database)
	key := "opaque-inconsistent-key"
	if err := repository.SetOrDeleteAcoustIDCredential(ctx, &key); err != nil {
		t.Fatalf("seed credential before introducing inconsistent state: %v", err)
	}
	sourceID, providerID := assertAcoustIDRegistration(t, ctx, database)
	cacheKey := "acoustid-lookup-v1:inconsistent-revision-fixture"
	if _, err := database.NewRaw(`INSERT INTO provider_response_cache(provider_source_id,provider_id,cache_key,payload,fetched_at,revision,generation,fetch_order)
		VALUES(?,?,?,?,now(),'accepted-before-repair',2,13)`, sourceID, providerID, cacheKey, `{"accepted":true}`).Exec(ctx); err != nil {
		t.Fatalf("seed accepted response: %v", err)
	}
	if _, err := database.NewRaw(`DELETE FROM app_setting WHERE setting_name='internal_acoustid_config_identity'`).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	observed, present, revision, err := repository.ReadAcoustIDCredential(ctx)
	if err != nil || !present || observed != key || revision != "" {
		t.Fatalf("read inconsistent credential = (%q,%t,%q), err=%v", observed, present, revision, err)
	}
	var identityCount int
	if err := database.NewRaw(`SELECT count(*) FROM app_setting WHERE setting_name='internal_acoustid_config_identity'`).Scan(ctx, &identityCount); err != nil || identityCount != 0 {
		t.Fatalf("read repaired credential identity count=%d err=%v; reads must not backfill", identityCount, err)
	}

	if err := repository.SetOrDeleteAcoustIDCredential(ctx, &key); err != nil {
		t.Fatalf("explicitly save same key: %v", err)
	}
	observed, present, revision, err = repository.ReadAcoustIDCredential(ctx)
	if err != nil || !present || observed != key || revision == "" {
		t.Fatalf("explicit save did not establish revision: (%q,%t,%q), err=%v", observed, present, revision, err)
	}
	repairedSourceID, _ := assertAcoustIDRegistration(t, ctx, database)
	if repairedSourceID != sourceID {
		t.Fatalf("source ID changed while establishing revision: %s -> %s", sourceID, repairedSourceID)
	}
	assertAcoustIDCachePayload(t, ctx, database, sourceID, cacheKey, `{"accepted":true}`, "accepted-before-repair", 2, 13)
	var responseCount int
	if err := database.NewRaw(`SELECT count(*) FROM provider_response_cache WHERE provider_source_id=?`, sourceID).Scan(ctx, &responseCount); err != nil || responseCount != 1 {
		t.Fatalf("same-key revision establishment changed cache rows: count=%d err=%v", responseCount, err)
	}
	if err := repository.SetOrDeleteAcoustIDCredential(ctx, &key); err != nil {
		t.Fatalf("repeat same-key save: %v", err)
	}
	_, _, repeatedRevision, err := repository.ReadAcoustIDCredential(ctx)
	if err != nil || repeatedRevision != revision {
		t.Fatalf("repeat same-key save revision=%q err=%v; want %q", repeatedRevision, err, revision)
	}
}

func assertAcoustIDRegistration(t *testing.T, ctx context.Context, database *bun.DB) (uuid.UUID, uuid.UUID) {
	t.Helper()
	const endpoint = "https://api.acoustid.org/v2/lookup"
	var sourceID, providerID uuid.UUID
	var identity, namespace string
	var count int
	err := database.NewRaw(`SELECT source.id,source.provider_id,source.configuration_identity,source.namespace FROM provider_source AS source
		JOIN provider ON provider.id = source.provider_id
		WHERE provider.code = 'acoustid' AND source.endpoint = ?`, endpoint).Scan(ctx, &sourceID, &providerID, &identity, &namespace)
	namespaceHash := sha256.Sum256([]byte(endpoint))
	wantNamespace := "acoustid:" + hex.EncodeToString(namespaceHash[:])
	if err != nil || identity != "acoustid-official-v1" || namespace != wantNamespace {
		t.Fatalf("AcoustID source = (%s,%s,%q,%q), err=%v", sourceID, providerID, identity, namespace, err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM provider_source WHERE provider_id=? AND endpoint=?`, providerID, endpoint).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("AcoustID sources for official endpoint = %d, err=%v; want exactly one", count, err)
	}
	return sourceID, providerID
}

func assertAcoustIDCachePayload(t *testing.T, ctx context.Context, database *bun.DB, sourceID uuid.UUID, cacheKey, wantPayload, wantRevision string, wantGeneration, wantOrder int64) {
	t.Helper()
	var payload, revision string
	var generation, fetchOrder int64
	if err := database.NewRaw(`SELECT payload::text,revision,generation,fetch_order FROM provider_response_cache WHERE provider_source_id=? AND cache_key=?`, sourceID, cacheKey).Scan(ctx, &payload, &revision, &generation, &fetchOrder); err != nil {
		t.Fatalf("read accepted AcoustID cache state: %v", err)
	}
	var wantJSON, gotJSON any
	if err := json.Unmarshal([]byte(wantPayload), &wantJSON); err != nil {
		t.Fatalf("decode expected accepted payload: %v", err)
	}
	if err := json.Unmarshal([]byte(payload), &gotJSON); err != nil {
		t.Fatalf("decode accepted cache payload: %v", err)
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) || revision != wantRevision || generation != wantGeneration || fetchOrder != wantOrder {
		t.Fatalf("accepted AcoustID cache state = (%q,%q,%d,%d); want (%q,%q,%d,%d)", payload, revision, generation, fetchOrder, wantPayload, wantRevision, wantGeneration, wantOrder)
	}
}
