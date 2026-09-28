package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func TestRequestMiddlewareAddsIDAndStructuredLog(t *testing.T) {
	var logs bytes.Buffer
	router := testMiddlewareRouter(&logs)
	router.Get("/ok", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ok", nil))
	requestID := response.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("X-Request-ID is empty")
	}
	for _, expected := range []string{`"status":201`, `"request_id":"` + requestID + `"`} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("log %q does not contain %q", logs.String(), expected)
		}
	}
}

func TestRecoverPanicsReturnsRequestID(t *testing.T) {
	var logs bytes.Buffer
	router := testMiddlewareRouter(&logs)
	router.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	requestID := response.Header().Get("X-Request-ID")
	if !strings.Contains(response.Body.String(), `"requestId":"`+requestID+`"`) {
		t.Errorf("body = %q, want request ID %q", response.Body.String(), requestID)
	}
	if !strings.Contains(logs.String(), `"msg":"HTTP handler panic"`) {
		t.Errorf("log %q does not contain panic entry", logs.String())
	}
}

func testMiddlewareRouter(logs *bytes.Buffer) *chi.Mux {
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(RequestIDHeader)
	router.Use(RequestLogger(logger))
	router.Use(RecoverPanics(logger))
	return router
}
