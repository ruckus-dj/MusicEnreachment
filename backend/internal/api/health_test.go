package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type fakePinger struct {
	err error
}

func (p fakePinger) PingContext(context.Context) error { return p.err }

func TestLiveness(t *testing.T) {
	response := httptest.NewRecorder()
	Liveness(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestReadiness(t *testing.T) {
	tests := []struct {
		name   string
		pinger databasePinger
		want   int
	}{
		{name: "ready", pinger: fakePinger{}, want: http.StatusOK},
		{name: "database error", pinger: fakePinger{err: errors.New("unavailable")}, want: http.StatusServiceUnavailable},
		{name: "missing database", pinger: nil, want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			Readiness(test.pinger)(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
			if response.Code != test.want {
				t.Errorf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func TestReadinessRejectsPlatformDiagnosticState(t *testing.T) {
	platform := settings.PlatformState{
		Platform:   settings.Platform{GOOS: "linux", GOARCH: "amd64"},
		Diagnostic: true, Reason: "platform differs",
	}
	response := httptest.NewRecorder()
	Readiness(fakePinger{}, platform)(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
