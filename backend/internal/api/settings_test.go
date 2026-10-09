package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type failingSHA256Store struct {
	apiSettingsStore
}

type failingSHA256GetStore struct {
	apiSettingsStore
}

func (store failingSHA256GetStore) Get(ctx context.Context, key string) (string, bool, error) {
	if key == settings.SHA256EnabledKey {
		return "", false, fmt.Errorf("database secret: private-credential")
	}
	return store.apiSettingsStore.Get(ctx, key)
}

func (store failingSHA256Store) Set(ctx context.Context, key, value string) error {
	if key == settings.SHA256EnabledKey {
		return fmt.Errorf("database secret: private-credential")
	}
	return store.apiSettingsStore.Set(ctx, key, value)
}

func TestSettingsSHA256DefaultAndRoundTrip(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	handler := api.HandlerWithSetup(setup)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sha256_enabled":true`) {
		t.Fatalf("default settings response status=%d body=%s, want SHA-256 enabled", response.Code, response.Body.String())
	}

	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/settings/sha256", strings.NewReader(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("disable SHA-256 status=%d body=%s, want %d", response.Code, response.Body.String(), http.StatusNoContent)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sha256_enabled":false`) {
		t.Fatalf("updated settings response status=%d body=%s, want SHA-256 disabled", response.Code, response.Body.String())
	}
}

func TestAcoustIDApplicationKeyWriteOnlyLifecycle(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	handler := api.HandlerWithSetup(setup)
	secret := " arbitrary:key value "

	request := httptest.NewRequest(http.MethodPut, "/settings/acoustid/application-key", strings.NewReader(`{"key":" arbitrary:key value "}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("set key status=%d body=%s", response.Code, response.Body.String())
	}
	if store[settings.AcoustIDApplicationKey] != secret {
		t.Fatalf("stored key=%q, want exact supplied value", store[settings.AcoustIDApplicationKey])
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"has_acoustid_application_key":true`) || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("GET settings status=%d body=%s, want presence only", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/settings/acoustid/application-key", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete key status=%d body=%s", response.Code, response.Body.String())
	}
	if _, exists := store[settings.AcoustIDApplicationKey]; exists {
		t.Fatal("expected AcoustID application key setting to be absent after delete")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"has_acoustid_application_key":false`) || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("GET after delete status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAcoustIDApplicationKeyRejectsEmptyAndDoesNotEcho(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := api.HandlerWithSetup(service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/settings/acoustid/application-key", strings.NewReader(`{"key":""}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || strings.Contains(response.Body.String(), `"key":""`) {
		t.Fatalf("empty key status=%d body=%s, want safe validation error", response.Code, response.Body.String())
	}
}

func TestAcoustIDApplicationKeyValidationNeverEchoesRequestValues(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := api.HandlerWithSetup(service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil))

	secret := "recognizable-acoustid-secret"
	longKey := strings.Repeat(secret, 4096/len(secret)+1)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "oversized key", body: `{"key":"` + longKey + `"}`},
		{name: "array key", body: `{"key":["` + secret + `"]}`},
		{name: "object key", body: `{"key":{"secret":"` + secret + `"}}`},
		{name: "unknown field", body: `{"key":"valid","unexpected":"` + secret + `"}`},
		{name: "missing key", body: `{"unexpected":"` + secret + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/settings/acoustid/application-key", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)
			if response.Code < http.StatusBadRequest || response.Code >= http.StatusInternalServerError {
				t.Fatalf("status=%d body=%s, want a client error", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), longKey) {
				t.Fatalf("response leaked request value: %s", response.Body.String())
			}
		})
	}
}

func TestSettingsSHA256RejectsInvalidInput(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	handler := api.HandlerWithSetup(setup)

	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/settings/sha256", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnprocessableEntity {
			t.Errorf("input %s status=%d body=%s, want %d", body, response.Code, response.Body.String(), http.StatusUnprocessableEntity)
		}
	}
}

func TestSettingsSHA256StoreFailureIsNotReportedAsSuccess(t *testing.T) {
	store := failingSHA256Store{apiSettingsStore{}}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	handler := api.HandlerWithSetup(setup)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/settings/sha256", strings.NewReader(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private-credential") {
		t.Fatalf("store failure status=%d body=%s, want safe 500", response.Code, response.Body.String())
	}
}

func TestSHA256ProjectionAcrossReadRoutes(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "setup state", path: "/setup"},
		{name: "configuration health", path: "/setup/health"},
		{name: "settings", path: "/settings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, persisted := range []struct {
				name  string
				value string
				want  string
			}{
				{name: "default enabled", want: `"sha256_enabled":true`},
				{name: "saved disabled", value: "false", want: `"sha256_enabled":false`},
			} {
				t.Run(persisted.name, func(t *testing.T) {
					store := apiSettingsStore{}
					if persisted.value != "" {
						store[settings.SHA256EnabledKey] = persisted.value
					}
					registry := settings.New(store, nil)
					if err := registry.CompleteSetup(context.Background()); err != nil {
						t.Fatal(err)
					}
					handler := api.HandlerWithSetup(service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil))
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
					if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), persisted.want) {
						t.Fatalf("GET %s status=%d body=%s, want status 200 and %s", tc.path, response.Code, response.Body.String(), persisted.want)
					}
				})
			}
		})
	}
}

func TestSHA256ReadFailureIsSafeAcrossReadRoutes(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "setup state", path: "/setup"},
		{name: "configuration health", path: "/setup/health"},
		{name: "settings", path: "/settings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := failingSHA256GetStore{apiSettingsStore{}}
			registry := settings.New(store, nil)
			if err := registry.CompleteSetup(context.Background()); err != nil {
				t.Fatal(err)
			}
			handler := api.HandlerWithSetup(service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private-credential") {
				t.Fatalf("GET %s status=%d body=%s, want safe 500", tc.path, response.Code, response.Body.String())
			}
		})
	}
}
