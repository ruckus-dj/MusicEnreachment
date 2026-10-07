//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestToolTransitionHTTPResponsesPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	databaseURL := testpostgres.URL(t, database)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	repository := persistence.NewSetupManagerRepository(database)
	store := persistence.NewSettingsRepository(database)
	registry := settings.New(store, nil)
	platform, err := registry.InitializePlatform(ctx, settings.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	toolsRoot, outputRoot := t.TempDir(), t.TempDir()
	if err := registry.SetToolsDirectory(ctx, toolsRoot, outputRoot); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetOutputDirectory(ctx, outputRoot, toolsRoot); err != nil {
		t.Fatal(err)
	}
	if err := registry.CompleteSetup(ctx); err != nil {
		t.Fatal(err)
	}

	client, listenerPool, err := jobs.StartWithWorkers(ctx, databaseURL, database.DB, func(workers *river.Workers) {
		river.AddWorker(workers, &transitionBlockingWorker{})
	})
	if err != nil {
		t.Fatalf("start River integration client: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := client.Stop(stopCtx); err != nil {
			t.Errorf("stop River client: %v", err)
		}
		listenerPool.Close()
	})

	setup := service.NewSetup(store, registry, platform, repository, nil)
	catalog := transitionCatalog{release: tools.Release{Identity: "1.6.1", Artifacts: []tools.Artifact{{Name: "fpcalc.zip"}}}}
	installOperations := service.NewInstallOperations(repository, catalog, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, client, registry)
	moveTools := service.NewMoveTools(repository, registry, tools.Platform{GOOS: "linux", GOARCH: "amd64"}, client)
	installations := service.NewInstallations(repository, settings.Platform{GOOS: "linux", GOARCH: "amd64"}, registry, transitionVerifier{}, registry)
	operations := service.NewOperations(repository)
	router := chi.NewRouter()
	humaAPI := api.New(router)
	api.RegisterAll(humaAPI, api.Dependencies{
		Setup: setup, InstallOperations: installOperations, MoveTools: moveTools,
		Installations: installations, Operations: operations,
	})

	response := transitionRequest(t, router, http.MethodPost, "/tools/installations/preflight", `{"package_kind":"fpcalc","release_identity":"1.6.1"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("install preflight status=%d body=%s", response.Code, response.Body.String())
	}
	var installPlan api.InstallPreflightBody
	if err := json.Unmarshal(response.Body.Bytes(), &installPlan); err != nil {
		t.Fatal(err)
	}
	if installPlan.Token == "" || len(installPlan.Targets) != 1 || len(installPlan.Conflicts) != 0 {
		t.Fatalf("install preflight response = %#v", installPlan)
	}
	response = transitionRequest(t, router, http.MethodPost, "/tools/installations/preflight", `{"package_kind":"fpcalc","release_identity":"unknown"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("invalid install preflight status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertTransitionErrorBody(t, response, response.Code)

	response = transitionRequest(t, router, http.MethodPost, "/tools/installations", `{"preflight_token":"not-a-token"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("invalid install start status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertTransitionErrorBody(t, response, response.Code)
	response = transitionRequest(t, router, http.MethodPost, "/tools/installations", `{"preflight_token":"`+installPlan.Token+`"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("install start status=%d body=%s", response.Code, response.Body.String())
	}
	var started api.OperationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.Kind != "install" || started.State != "queued" || started.TargetInstallationID == nil {
		t.Fatalf("install start snapshot = %#v", started)
	}
	response = transitionRequest(t, router, http.MethodPost, "/tools/installations", `{"preflight_token":"`+installPlan.Token+`"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("reused install token status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertTransitionErrorBody(t, response, response.Code)
	if _, err := repository.GetInstallation(ctx, *started.TargetInstallationID); err != nil {
		t.Fatalf("successful install start did not persist target: %v", err)
	}
	// Close this operation through the real service state machine before testing
	// the independently exclusive move operation.
	if err := operations.Fail(ctx, started.ID, "test_cleanup", "safe fixture failure"); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkInstallationFailed(ctx, *started.TargetInstallationID); err != nil {
		t.Fatal(err)
	}
	response = transitionRequest(t, router, http.MethodDelete, "/operations/"+started.ID.String(), "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("dismiss fixture install operation status=%d body=%s", response.Code, response.Body.String())
	}

	createTransitionInstallation(t, ctx, repository, toolsRoot, "fpcalc", "2.0", false)
	newRoot := t.TempDir()
	response = transitionRequest(t, router, http.MethodPost, "/tools/move/preflight", `{"new_tools_directory":"`+newRoot+`","remove_old_files":false}`)
	if response.Code != http.StatusOK {
		t.Fatalf("move preflight status=%d body=%s", response.Code, response.Body.String())
	}
	var movePlan api.MovePreflightBody
	if err := json.Unmarshal(response.Body.Bytes(), &movePlan); err != nil {
		t.Fatal(err)
	}
	if movePlan.Token == "" || movePlan.FileCount != 1 || len(movePlan.Conflicts) != 0 {
		t.Fatalf("move preflight response = %#v", movePlan)
	}
	response = transitionRequest(t, router, http.MethodPost, "/tools/move/preflight", `{"new_tools_directory":"`+toolsRoot+`","remove_old_files":false}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("overlapping move preflight status=%d want=%d body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	assertTransitionErrorBody(t, response, response.Code)
	response = transitionRequest(t, router, http.MethodPost, "/tools/move", `{"preflight_token":"not-a-token"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("invalid move start status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertTransitionErrorBody(t, response, response.Code)
	response = transitionRequest(t, router, http.MethodPost, "/tools/move", `{"preflight_token":"`+movePlan.Token+`"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("move start status=%d body=%s", response.Code, response.Body.String())
	}
	var moveOperation api.OperationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &moveOperation); err != nil {
		t.Fatal(err)
	}
	if moveOperation.Kind != "move_tools_root" || moveOperation.State != "queued" {
		t.Fatalf("move start snapshot = %#v", moveOperation)
	}
	// Retire the queued move via the operation state machine so its durable
	// exclusivity lock does not contaminate activation/deletion scenarios.
	if err := operations.Fail(ctx, moveOperation.ID, "test_cleanup", "safe fixture failure"); err != nil {
		t.Fatal(err)
	}
	response = transitionRequest(t, router, http.MethodDelete, "/operations/"+moveOperation.ID.String(), "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("dismiss fixture move operation status=%d body=%s", response.Code, response.Body.String())
	}

	activatable := createTransitionInstallation(t, ctx, repository, toolsRoot, "fpcalc", "3.0", false)
	response = transitionRequest(t, router, http.MethodPost, "/tools/installations/"+activatable.ID.String()+"/activate", `{"package_kind":"fpcalc"}`)
	if response.Code != http.StatusNoContent {
		t.Fatalf("activate ready installation status=%d body=%s", response.Code, response.Body.String())
	}
	response = transitionRequest(t, router, http.MethodPost, "/tools/installations/"+uuid.NewString()+"/activate", `{"package_kind":"fpcalc"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("activate unknown installation status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertTransitionErrorBody(t, response, response.Code)

	deletable := createTransitionInstallation(t, ctx, repository, toolsRoot, "fpcalc", "4.0", false)
	response = transitionRequest(t, router, http.MethodDelete, "/tools/installations/"+deletable.ID.String(), `{"package_kind":"fpcalc"}`)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete inactive installation status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := repository.GetInstallation(ctx, deletable.ID); err == nil {
		t.Fatal("deleted installation row remains")
	}
	active, err := registry.ReadRuntimeSettings(ctx)
	if err != nil || active.ActiveFPCalcInstallation == "" {
		t.Fatalf("active installation setting = %#v, err=%v", active, err)
	}
	response = transitionRequest(t, router, http.MethodDelete, "/tools/installations/"+activatable.ID.String(), `{"package_kind":"fpcalc"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("delete active installation status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertTransitionErrorBody(t, response, response.Code)
}

type transitionCatalog struct{ release tools.Release }

func (c transitionCatalog) List(context.Context, tools.PackageKind, tools.Platform) ([]tools.Release, error) {
	return []tools.Release{c.release}, nil
}
func (c transitionCatalog) Resolve(_ context.Context, _ tools.PackageKind, _ tools.Platform, identity string) (tools.Release, error) {
	if identity != c.release.Identity {
		return tools.Release{}, context.Canceled
	}
	return c.release, nil
}

type transitionVerifier struct{}

func (transitionVerifier) VerifyInstallation(_ context.Context, root, relative string, _ tools.PackageKind, _ string, _ string) (map[string]string, error) {
	info, err := os.Stat(filepath.Join(root, relative, "fpcalc"))
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, os.ErrInvalid
	}
	return map[string]string{"fpcalc": "verified"}, nil
}

func createTransitionInstallation(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, root, packageKind, release string, active bool) *persistence.ToolInstallation {
	t.Helper()
	id := uuid.New()
	installation := &persistence.ToolInstallation{
		ID: id, PackageKind: packageKind, PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "fixture", ReleaseIdentity: release, RelativePath: filepath.Join(packageKind, release), State: "preparing",
	}
	if err := repository.CreateInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkInstallationReady(ctx, id, json.RawMessage(`{"fpcalc":"verified"}`), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, installation.RelativePath, "fpcalc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("local test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if active {
		if err := repository.ActivateInstallation(ctx, id, packageKind, "linux", "amd64", settings.ActiveFPCalcInstallationKey); err != nil {
			t.Fatal(err)
		}
	}
	return installation
}

type transitionBlockingWorker struct {
	river.WorkerDefaults[service.OperationJobArgs]
}

func (*transitionBlockingWorker) Work(ctx context.Context, _ *river.Job[service.OperationJobArgs]) error {
	<-ctx.Done()
	return ctx.Err()
}
