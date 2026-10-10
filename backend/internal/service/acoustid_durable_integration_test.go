//go:build integration

package service_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/acoustid"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

const (
	acoustIDRecordingA = "00112233-4455-4677-8899-aabbccddeeff"
	acoustIDRecordingB = "10112233-4455-4677-8899-aabbccddeeff"
	acoustIDRecordingC = "20112233-4455-4677-8899-aabbccddeeff"
)

func TestDurableAcoustIDLookupRiverPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	settingsRepository := persistence.NewSettingsRepository(db)
	registry := settings.New(settingsRepository, nil)
	key := "acoustid-secret-echo"
	if err := registry.SetAcoustIDApplicationKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	var httpCalls atomic.Int32
	transport := &acoustIDFixtureTransport{base: http.DefaultTransport}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		httpCalls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/v2/lookup" || request.URL.RawQuery != "" {
			t.Errorf("AcoustID request method/path/query = %s %s %q", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read AcoustID form: %v", err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("parse AcoustID form: %v", err)
		}
		transport.recordForm(form)
		status, response, entered, release := transport.snapshotResponse()
		if entered != nil {
			close(entered)
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, response)
	}))
	defer server.Close()
	transport.setTarget(server.URL)

	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	acoustIDClient := acoustid.NewClient(httpClient, nil) // production endpoint stays fixed
	factory := service.NewProviderFactory(registry, nil, nil, nil, acoustIDClient)
	cache := persistence.NewProviderCacheRepository(db)
	operationsRepository := persistence.NewSetupManagerRepository(db)
	operations := service.NewOperations(operationsRepository)
	fetch := service.NewProviderFetchService(registry, factory, cache).WithDurableOperations(operationsRepository, nil)
	worker := jobs.NewProviderFetchWorker(fetch, operations)
	riverClient, listenerPool, err := jobs.StartWithWorkers(ctx, testpostgres.URL(t, db), db.DB, func(workers *river.Workers) {
		river.AddWorker(workers, worker)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := riverClient.Stop(stopCtx); err != nil {
			t.Errorf("stop River: %v", err)
		} else {
			<-riverClient.Stopped()
		}
		listenerPool.Close()
	}()
	// The worker and the admission service share this instance; attach the real
	// River client after the registered worker has been constructed.
	fetch.WithDurableOperations(operationsRepository, riverClient)

	root := &persistence.SourceRoot{ConfiguredPath: "/srv/acoustid-fixture", DisplayName: "Music", Enabled: true}
	inventory := persistence.NewSourceInventoryRepository(db)
	if err := inventory.CreateSourceRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	location := persistence.SourceLocation{ID: uuid.New(), SourceRootID: root.ID, RelativePath: "track.flac", SizeBytes: 4096, Mtime: time.Now().UTC().Truncate(time.Microsecond), LastSeenScanGeneration: 1, ProbeStatus: persistence.SourceProbeStatusAudio}
	if _, err := db.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	establishAnalysisStartInventory(t, ctx, db, root)
	work := &persistence.SourceAnalysisWork{
		ID: uuid.New(), LocationID: location.ID, SourceRootID: root.ID, ConfiguredPath: root.ConfiguredPath,
		InventoryPath: *root.InventoryPath, RelativePath: location.RelativePath, SizeBytes: location.SizeBytes,
		Mtime: location.Mtime, OriginScanOperationID: uuid.New(),
	}
	initialFailure := "fixture pending fingerprint"
	if err := inventory.StoreSourceAnalysisWork(ctx, work, []persistence.SourceAnalysisStepInput{{Step: persistence.SourceStepFingerprint, State: "failed", SafeError: &initialFailure}}); err != nil {
		t.Fatalf("store current work: %v", err)
	}
	fingerprintResult := persistence.SourceFingerprintResult{
		ID: uuid.New(), WinningResultID: uuid.New(), FPCalcVersion: "1.5.1",
		VersionBanner: "fpcalc version 1.5.1 fixture", AlgorithmNamespace: "chromaprint",
		AlgorithmID: 2, Fingerprint: "captured-fingerprint-winner", ReportedDuration: 12.9,
		CalculatedAt: time.Now().UTC(), AppliedOperationID: uuid.New(), ParserContractVersion: 3,
	}
	if _, err := db.NewInsert().Model(&fingerprintResult).Exec(ctx); err != nil {
		t.Fatalf("persist selected fingerprint result: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE source_analysis_step SET state='succeeded',safe_error=NULL,success_reuse_origin='executed',success_fingerprint_result_id=? WHERE work_id=? AND step='fingerprint'`, fingerprintResult.ID, work.ID).Exec(ctx); err != nil {
		t.Fatalf("select successful fingerprint result: %v", err)
	}
	captured := new(persistence.SourceFingerprintResult)
	if err := db.NewSelect().Model(captured).Where("id = ?", fingerprintResult.ID).Scan(ctx); err != nil {
		t.Fatalf("read selected fingerprint result: %v", err)
	}
	if captured.ID != fingerprintResult.ID {
		t.Fatalf("captured result ID = %s, want %s", captured.ID, fingerprintResult.ID)
	}
	if _, err := fetch.StartAcoustIDLookup(ctx, uuid.New(), false); err == nil {
		t.Fatal("admitted lookup for nonexistent work")
	}
	if _, err := db.NewRaw(`UPDATE source_analysis_step SET state='failed',safe_error='fixture failure' WHERE work_id=? AND step='fingerprint'`, work.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fetch.StartAcoustIDLookup(ctx, work.ID, false); err == nil {
		t.Fatal("admitted lookup for a failed fingerprint step")
	}
	if _, err := db.NewRaw(`UPDATE source_analysis_step SET state='succeeded',safe_error=NULL,success_reuse_origin='executed',success_fingerprint_result_id=? WHERE work_id=? AND step='fingerprint'`, captured.ID, work.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	firstEntered, firstRelease := make(chan struct{}), make(chan struct{})
	transport.setResponse(http.StatusOK, acoustIDSuccessResponse(key), firstEntered, firstRelease)
	firstQueued, err := fetch.StartAcoustIDLookup(ctx, work.ID, false)
	if err != nil {
		t.Fatalf("admit first AcoustID lookup: %v", err)
	}
	awaitSignal(t, ctx, firstEntered, "first AcoustID HTTP request entry")
	// Change the persisted winner only after the admitted request has crossed the
	// worker boundary. The body must still contain the captured original value.
	if _, err := db.NewRaw(`UPDATE media_fingerprint_result SET fingerprint='replacement-winner' WHERE id=?`, captured.ID).Exec(ctx); err != nil {
		close(firstRelease)
		t.Fatalf("change persisted canonical winner after admission: %v", err)
	}
	close(firstRelease)
	first := awaitAcoustIDOperation(t, ctx, operationsRepository, firstQueued.ID)
	if first.State != "succeeded" || first.Stage != "provider_response_saved" || httpCalls.Load() != 1 {
		t.Fatalf("first AcoustID operation = %s/%s; HTTP calls=%d", first.State, first.Stage, httpCalls.Load())
	}
	var firstIntent struct {
		WorkID            uuid.UUID `json:"work_id"`
		FingerprintRowID  uuid.UUID `json:"fingerprint_row_id"`
		WinningResultID   uuid.UUID `json:"winning_result_id"`
		WinnerFingerprint string    `json:"winner_fingerprint"`
		LookupDuration    int       `json:"lookup_duration"`
		FPCalcVersion     string    `json:"fpcalc_version"`
		ParserVersion     int       `json:"parser_contract_version"`
	}
	if err := json.Unmarshal(first.InputSnapshot, &firstIntent); err != nil {
		t.Fatalf("decode persisted intent: %v", err)
	}
	if firstIntent.WorkID != work.ID || firstIntent.FingerprintRowID != captured.ID || firstIntent.WinningResultID != captured.WinningResultID ||
		firstIntent.WinnerFingerprint != captured.Fingerprint || firstIntent.LookupDuration != 12 || firstIntent.FPCalcVersion != "1.5.1" || firstIntent.ParserVersion != 3 {
		t.Fatalf("captured intent = %+v", firstIntent)
	}
	if posted := transport.lastForm(); posted.Get("client") != key || posted.Get("fingerprint") != captured.Fingerprint || posted.Get("duration") != "12" {
		t.Fatalf("AcoustID POST form = %#v", posted)
	}
	// A canonical winner changed after admission; worker still sent the exact
	// captured winner from the durable snapshot.
	if posted := transport.lastForm(); posted.Get("fingerprint") != captured.Fingerprint {
		t.Fatalf("worker reread changed canonical winner: %q", posted.Get("fingerprint"))
	}

	cacheKey := *first.ProviderCacheKey
	accepted, err := cache.GetCachedResponse(ctx, *first.ProviderSourceID, cacheKey)
	if err != nil || accepted == nil {
		t.Fatalf("accepted AcoustID response = %v, err=%v", accepted, err)
	}
	if strings.Contains(string(accepted.Payload), key) || !strings.Contains(string(accepted.Payload), "[REDACTED]") {
		t.Fatalf("raw AcoustID response not safely scrubbed: %s", accepted.Payload)
	}
	firstTitle := strings.Index(string(accepted.Payload), "ordered-first")
	secondTitle := strings.Index(string(accepted.Payload), "ordered-second")
	if firstTitle < 0 || secondTitle <= firstTitle {
		t.Fatalf("raw result order was not retained: %s", accepted.Payload)
	}
	if score, found, err := fetch.CachedAcoustIDEvidence(ctx, *first.ProviderSourceID, cacheKey, acoustIDRecordingA); err != nil || !found || score == nil || *score != 0.93 {
		t.Fatalf("MAX AcoustID score = %v found=%v err=%v; want 0.93", score, found, err)
	}
	if score, found, err := fetch.CachedAcoustIDEvidence(ctx, *first.ProviderSourceID, cacheKey, acoustIDRecordingB); err != nil || !found || score == nil || *score != 0 {
		t.Fatalf("zero AcoustID score = %v found=%v err=%v; want present zero", score, found, err)
	}
	if score, found, err := fetch.CachedAcoustIDEvidence(ctx, *first.ProviderSourceID, cacheKey, acoustIDRecordingC); err != nil || !found || score != nil {
		t.Fatalf("omitted AcoustID score = %v found=%v err=%v; want nil evidence", score, found, err)
	}
	if first.RiverJobID == nil {
		t.Fatal("first durable AcoustID operation has no River job")
	}
	duplicateTicket := persistence.ProviderRefreshTicket{
		ProviderSourceID: accepted.ProviderSourceID, ProviderID: accepted.ProviderID, CacheKey: accepted.CacheKey,
		Generation: accepted.Generation, ConfigurationIdentity: "acoustid-official-v1", FetchOrder: accepted.FetchOrder,
	}
	if err := cache.ApplyAcoustIDResponse(ctx, duplicateTicket, *first.ProviderConfigurationIdentity, time.Now().UTC(), []byte(`{"available":true,"results":[]}`), persistence.ProviderDeliveryFence{
		OperationID: first.ID, Attempt: first.Attempt, RiverJobID: *first.RiverJobID, ExecutionEpoch: first.ProviderExecutionEpoch,
	}); err == nil {
		t.Fatal("duplicate post-success AcoustID delivery overwrote accepted data")
	}

	// Credential deletion does not change input cache identity or source identity.
	if err := registry.DeleteAcoustIDApplicationKey(ctx); err != nil {
		t.Fatal(err)
	}
	// Re-establish the captured winner before exercising cache reuse without a
	// key: changed current fingerprint identity must not reuse the old cache entry.
	if _, err := db.NewRaw(`UPDATE media_fingerprint_result SET fingerprint=? WHERE id=?`, captured.Fingerprint, captured.ID).Exec(ctx); err != nil {
		t.Fatalf("restore current selected fingerprint for cache reuse: %v", err)
	}
	beforeReuseCalls := httpCalls.Load()
	reused := startAcoustIDOperation(t, ctx, fetch, operationsRepository, work.ID, false)
	if reused.State != "succeeded" || reused.Stage != "provider_cache_reused" || httpCalls.Load() != beforeReuseCalls || *reused.ProviderSourceID != *first.ProviderSourceID || *reused.ProviderCacheKey != cacheKey {
		t.Fatalf("ordinary cache reuse after key deletion = %s/%s calls=%d", reused.State, reused.Stage, httpCalls.Load())
	}
	missingResult := persistence.SourceFingerprintResult{
		ID: uuid.New(), WinningResultID: uuid.New(), FPCalcVersion: "1.5.1",
		VersionBanner: "fpcalc version 1.5.1 fixture", AlgorithmNamespace: "chromaprint",
		AlgorithmID: 2, Fingerprint: "different-no-key-fingerprint", ReportedDuration: 18,
		CalculatedAt: time.Now().UTC(), AppliedOperationID: uuid.New(), ParserContractVersion: 3,
	}
	if _, err := db.NewInsert().Model(&missingResult).Exec(ctx); err != nil {
		t.Fatalf("persist cache-miss fingerprint: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE source_analysis_step SET success_fingerprint_result_id=? WHERE work_id=? AND step='fingerprint'`, missingResult.ID, work.ID).Exec(ctx); err != nil {
		t.Fatalf("select cache-miss fingerprint: %v", err)
	}
	noKeyCalls := httpCalls.Load()
	noKey := startAcoustIDOperation(t, ctx, fetch, operationsRepository, work.ID, false)
	if noKey.State != "failed" || httpCalls.Load() != noKeyCalls {
		t.Fatalf("cache miss without key state=%s HTTP calls=%d (before=%d)", noKey.State, httpCalls.Load(), noKeyCalls)
	}

	// A failed explicit refresh records only a safe operation failure and leaves
	// the accepted response's bytes, provider revision and fetch timestamp alone.
	if err := registry.SetAcoustIDApplicationKey(ctx, "refresh-failure-key"); err != nil {
		t.Fatal(err)
	}
	transport.setResponse(http.StatusServiceUnavailable, `{"status":"error"}`, nil, nil)
	failure := startAcoustIDOperation(t, ctx, fetch, operationsRepository, work.ID, true)
	if failure.State != "failed" || failure.SafeError == nil || !strings.Contains(*failure.SafeError, "AcoustID") {
		t.Fatalf("failed refresh operation = %#v", failure)
	}
	assertAcoustIDResponseRetained(t, ctx, cache, first, accepted)

	// Credential replacement after the actual HTTP request starts invalidates the
	// write, while the old accepted response remains usable.
	if err := registry.SetAcoustIDApplicationKey(ctx, "inflight-old-key"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	transport.setResponse(http.StatusOK, acoustIDSuccessResponse("inflight-old-key"), entered, release)
	inflight, err := fetch.StartAcoustIDLookup(ctx, work.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, ctx, entered, "AcoustID HTTP request entry")
	if err := registry.SetAcoustIDApplicationKey(ctx, "inflight-new-key"); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	inflightDone := awaitAcoustIDOperation(t, ctx, operationsRepository, inflight.ID)
	if inflightDone.State != "failed" || httpCalls.Load() != 3 {
		t.Fatalf("stale in-flight write state=%s calls=%d", inflightDone.State, httpCalls.Load())
	}
	assertAcoustIDResponseRetained(t, ctx, cache, first, accepted)
	if err := registry.SetAcoustIDApplicationKey(ctx, "inflight-delete-key"); err != nil {
		t.Fatal(err)
	}
	entered, release = make(chan struct{}), make(chan struct{})
	transport.setResponse(http.StatusOK, acoustIDSuccessResponse("inflight-delete-key"), entered, release)
	inflightDelete, err := fetch.StartAcoustIDLookup(ctx, work.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, ctx, entered, "AcoustID HTTP request before key deletion")
	if err := registry.DeleteAcoustIDApplicationKey(ctx); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	deletedInFlight := awaitAcoustIDOperation(t, ctx, operationsRepository, inflightDelete.ID)
	if deletedInFlight.State != "failed" || httpCalls.Load() != 4 {
		t.Fatalf("deleted-key in-flight write state=%s calls=%d", deletedInFlight.State, httpCalls.Load())
	}
	assertAcoustIDResponseRetained(t, ctx, cache, first, accepted)

	// A competing generation invalidates an in-flight ticket; a forged later
	// epoch is also rejected atomically against the still-running delivery.
	if err := registry.SetAcoustIDApplicationKey(ctx, "generation-key"); err != nil {
		t.Fatal(err)
	}
	entered, release = make(chan struct{}), make(chan struct{})
	transport.setResponse(http.StatusOK, acoustIDSuccessResponse("generation-key"), entered, release)
	generationRace, err := fetch.StartAcoustIDLookup(ctx, work.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, ctx, entered, "competing-generation HTTP request entry")
	running, err := operationsRepository.GetOperation(ctx, generationRace.ID)
	if err != nil || running.ProviderExecutionEpoch < 1 || running.RiverJobID == nil {
		close(release)
		t.Fatalf("running provider delivery = %#v, err=%v", running, err)
	}
	revision := *running.ProviderConfigurationIdentity
	competingTicket, err := cache.BeginAcoustIDRefresh(ctx, *running.ProviderSourceID, *running.ProviderCacheKey, revision)
	if err != nil {
		close(release)
		t.Fatalf("reserve competing generation: %v", err)
	}
	fence := persistence.ProviderDeliveryFence{
		OperationID: running.ID, Attempt: running.Attempt, RiverJobID: *running.RiverJobID, ExecutionEpoch: running.ProviderExecutionEpoch + 1,
	}
	staleEpochErr := cache.ApplyAcoustIDResponse(ctx, competingTicket, revision, time.Now().UTC(), []byte(`{"available":true,"results":[]}`), fence)
	if staleEpochErr == nil {
		close(release)
		t.Fatal("cache accepted an AcoustID write from a stale execution epoch")
	}
	staleGenerationTicket := competingTicket
	staleGenerationTicket.Generation--
	fence.ExecutionEpoch = running.ProviderExecutionEpoch
	staleGenerationErr := cache.ApplyAcoustIDResponse(ctx, staleGenerationTicket, revision, time.Now().UTC(), []byte(`{"available":true,"results":[]}`), fence)
	if staleGenerationErr == nil {
		close(release)
		t.Fatal("cache accepted an AcoustID write from a stale refresh generation")
	}
	close(release)
	generationDone := awaitAcoustIDOperation(t, ctx, operationsRepository, generationRace.ID)
	if generationDone.State != "failed" {
		t.Fatalf("stale generation operation state=%s", generationDone.State)
	}
	assertAcoustIDResponseRetained(t, ctx, cache, first, accepted)
	if got, err := cache.GetCachedResponse(ctx, *first.ProviderSourceID, cacheKey); err != nil || got == nil || string(got.Payload) != string(accepted.Payload) {
		t.Fatalf("failed competing refresh changed accepted payload: response=%v err=%v", got, err)
	}
}

func acoustIDSuccessResponse(secret string) string {
	return `{"status":"ok","request_echo":{"client_secret":"` + secret + `"},"results":[` +
		`{"score":0.8,"recordings":[{"id":"` + acoustIDRecordingA + `","title":"ordered-first ` + secret + `"}],"client-` + secret + `-echo":"` + secret + `"},` +
		`{"score":0.93,"recordings":[{"id":"` + acoustIDRecordingA + `","title":"ordered-second"}]},` +
		`{"score":0,"recordings":[{"id":"` + acoustIDRecordingB + `"}]},` +
		`{"recordings":[{"id":"` + acoustIDRecordingC + `"}]}]}`
}

func startAcoustIDOperation(t *testing.T, ctx context.Context, fetch *service.ProviderFetchService, repository *persistence.SetupManagerRepository, workID uuid.UUID, refresh bool) *persistence.Operation {
	t.Helper()
	operation, err := fetch.StartAcoustIDLookup(ctx, workID, refresh)
	if err != nil {
		t.Fatalf("start durable AcoustID lookup: %v", err)
	}
	return awaitAcoustIDOperation(t, ctx, repository, operation.ID)
}

func awaitAcoustIDOperation(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, id uuid.UUID) *persistence.Operation {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		operation, err := repository.GetOperation(ctx, id)
		if err != nil {
			t.Fatalf("read durable AcoustID operation: %v", err)
		}
		if operation.State == "succeeded" || operation.State == "failed" {
			return operation
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for AcoustID operation %s: %v", id, ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertAcoustIDResponseRetained(t *testing.T, ctx context.Context, cache *persistence.ProviderCacheRepository, operation *persistence.Operation, original *persistence.ProviderCachedResponse) {
	t.Helper()
	retained, err := cache.GetCachedResponse(ctx, *operation.ProviderSourceID, *operation.ProviderCacheKey)
	if err != nil || retained == nil || string(retained.Payload) != string(original.Payload) || !sameAcoustIDTime(retained.FetchedAt, original.FetchedAt) || !sameAcoustIDString(retained.Revision, original.Revision) {
		t.Fatalf("accepted AcoustID response changed: retained=%#v original=%#v err=%v", retained, original, err)
	}
}

func sameAcoustIDTime(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func sameAcoustIDString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func awaitSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", label, ctx.Err())
	case <-signal:
	}
}

type acoustIDFixtureTransport struct {
	mu       sync.Mutex
	base     http.RoundTripper
	target   *url.URL
	status   int
	response string
	entered  chan struct{}
	release  chan struct{}
	forms    []url.Values
}

func (transport *acoustIDFixtureTransport) setTarget(target string) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	parsed, err := url.Parse(target)
	if err != nil {
		panic(err)
	}
	transport.target = parsed
	transport.status, transport.response = http.StatusOK, `{"status":"ok","results":[]}`
}

func (transport *acoustIDFixtureTransport) setResponse(status int, response string, entered, release chan struct{}) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.status, transport.response, transport.entered, transport.release = status, response, entered, release
}

func (transport *acoustIDFixtureTransport) snapshotResponse() (int, string, chan struct{}, chan struct{}) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.status, transport.response, transport.entered, transport.release
}

func (transport *acoustIDFixtureTransport) recordForm(form url.Values) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.forms = append(transport.forms, form)
}

func (transport *acoustIDFixtureTransport) lastForm() url.Values {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.forms) == 0 {
		return url.Values{}
	}
	return transport.forms[len(transport.forms)-1]
}

func (transport *acoustIDFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	target := transport.target
	transport.mu.Unlock()
	if request.URL.Host != "api.acoustid.org" || request.URL.Path != "/v2/lookup" {
		return nil, &url.Error{Op: "roundtrip", URL: request.URL.String(), Err: io.ErrUnexpectedEOF}
	}
	copyRequest := request.Clone(request.Context())
	copyURL := *request.URL
	copyURL.Scheme, copyURL.Host = target.Scheme, target.Host
	copyRequest.URL, copyRequest.Host = &copyURL, target.Host
	base := transport.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(copyRequest)
}
