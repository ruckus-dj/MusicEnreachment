package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// apiSettingsStore is the in-package copy of the setup API memory settings
// fixture used by the external api_test package. Internal (package api) test
// files cannot reference identifiers declared in the external test package.
type apiSettingsStore map[string]string

func (store apiSettingsStore) Get(_ context.Context, key string) (string, bool, error) {
	value, exists := store[key]
	return value, exists, nil
}

func (store apiSettingsStore) Set(_ context.Context, key, value string) error {
	store[key] = value
	return nil
}

func (store apiSettingsStore) SetMany(_ context.Context, values map[string]string) error {
	for key, value := range values {
		store[key] = value
	}
	return nil
}

func (store apiSettingsStore) SetIfAbsent(_ context.Context, key, value string) (string, error) {
	if current, exists := store[key]; exists {
		return current, nil
	}
	store[key] = value
	return value, nil
}

func (store apiSettingsStore) InitializePlatform(_ context.Context, goos, goarch string) (string, string, bool, error) {
	persistedOS, hasOS := store[settings.PlatformGOOSKey]
	persistedArch, hasArch := store[settings.PlatformGOARCHKey]
	if hasOS != hasArch {
		return persistedOS, persistedArch, false, nil
	}
	if !hasOS {
		store[settings.PlatformGOOSKey] = goos
		store[settings.PlatformGOARCHKey] = goarch
		return goos, goarch, true, nil
	}
	return persistedOS, persistedArch, true, nil
}

type cleanupAdmissionFixture struct{ admissions int }

func (*cleanupAdmissionFixture) ListSourceAnalysisArtifactCleanupCandidates(context.Context, int) ([]*persistence.SourceAnalysisArtifact, error) {
	return nil, nil
}

func (repository *cleanupAdmissionFixture) AdmitSourceAnalysisArtifactCleanupWithArgsFactory(context.Context, []uuid.UUID, persistence.RiverInserter, func(uuid.UUID) river.JobArgs, *river.InsertOpts) (*persistence.Operation, error) {
	repository.admissions++
	return nil, errors.New("unexpected cleanup admission")
}

func (*cleanupAdmissionFixture) ListSourceAnalysisArtifactCleanupItems(context.Context, uuid.UUID) ([]persistence.SourceAnalysisArtifactCleanupItem, error) {
	return nil, nil
}

type cleanupAdmissionRiver struct{}

func (cleanupAdmissionRiver) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("unexpected River insert")
}

func TestCleanupSourceArtifactsRequiresCompletedSetupAndSupportedPlatform(t *testing.T) {
	for _, test := range []struct {
		name       string
		platform   settings.PlatformState
		completed  bool
		wantStatus int
	}{
		{
			name:       "setup unfinished",
			platform:   settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}},
			wantStatus: http.StatusConflict,
		},
		{
			name: "unsupported platform",
			platform: settings.PlatformState{
				Platform: settings.Platform{GOOS: "plan9", GOARCH: "mips"}, Diagnostic: true,
			},
			completed:  true,
			wantStatus: http.StatusServiceUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := apiSettingsStore{}
			registry := settings.New(store, nil)
			if test.completed {
				if err := registry.CompleteSetup(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			setup := service.NewSetup(store, registry, test.platform, nil, nil)
			repository := &cleanupAdmissionFixture{}
			cleanup := service.NewSourceAnalysisArtifactCleanup(repository, nil, cleanupAdmissionRiver{})
			handler := HandlerWithDependencies(Dependencies{Setup: setup, SourceArtifactCleanup: cleanup})
			request := httptest.NewRequest(http.MethodPost, "/source-analysis/artifacts/cleanup", strings.NewReader(`{"artifact_ids":["`+uuid.NewString()+`"]}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
			if repository.admissions != 0 {
				t.Fatalf("cleanup admission count=%d, want 0", repository.admissions)
			}
		})
	}
}
