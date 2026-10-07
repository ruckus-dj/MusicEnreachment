//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestExplicitActivationRequiresSetupCompletionPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
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
	setup := service.NewSetup(store, registry, platform, repository, nil)
	installations := service.NewInstallations(repository, platform.Platform, registry, transitionVerifier{}, registry)
	router := chi.NewRouter()
	humaAPI := api.New(router)
	api.RegisterAll(humaAPI, api.Dependencies{Setup: setup, Installations: installations})

	ready := createTransitionInstallation(t, ctx, repository, toolsRoot, "fpcalc", "activation-gate", false)
	request := "/tools/installations/" + ready.ID.String() + "/activate"
	response := transitionRequest(t, router, http.MethodPost, request, `{"package_kind":"fpcalc"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("activation before setup completion status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertTransitionErrorBody(t, response, response.Code)
	active, err := registry.ReadRuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.ActiveFPCalcInstallation != "" {
		t.Fatalf("activation before setup completion changed active installation to %q", active.ActiveFPCalcInstallation)
	}

	if err := registry.CompleteSetup(ctx); err != nil {
		t.Fatal(err)
	}
	response = transitionRequest(t, router, http.MethodPost, request, `{"package_kind":"fpcalc"}`)
	if response.Code != http.StatusNoContent {
		t.Fatalf("activation after setup completion status=%d want=%d body=%s", response.Code, http.StatusNoContent, response.Body.String())
	}
	active, err = registry.ReadRuntimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.ActiveFPCalcInstallation != ready.ID.String() {
		t.Fatalf("active installation after setup completion=%q want=%q", active.ActiveFPCalcInstallation, ready.ID)
	}
	var persisted persistence.ToolInstallation
	if err := database.NewSelect().Model(&persisted).Where("id = ?", ready.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if persisted.State != "ready" {
		t.Fatalf("activation changed installation state to %q", persisted.State)
	}
	var versions map[string]string
	if err := json.Unmarshal(persisted.ExecutableVersions, &versions); err != nil {
		t.Fatal(err)
	}
	if versions["fpcalc"] != "verified" {
		t.Fatalf("persisted executable versions = %#v", versions)
	}
	if filepath.IsAbs(persisted.RelativePath) {
		t.Fatalf("fixture installation path is not managed-relative: %q", persisted.RelativePath)
	}
}
