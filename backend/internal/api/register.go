package api

import (
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func RegisterAll(api huma.API, dependencies Dependencies) {
	tokens := newPreflightTokens()
	RegisterSetup(api, dependencies.Setup)
	registerSettings(api, dependencies.Setup)
	registerTools(api, dependencies, tokens)
	registerOperations(api, dependencies.Operations, dependencies.Setup)
}

func registerOperationEvents(router chi.Router, operations *service.Operations) {
	router.Get("/operations/{operation_id}/events", func(writer http.ResponseWriter, request *http.Request) {
		if operations == nil {
			http.Error(writer, "operation service is unavailable", http.StatusServiceUnavailable)
			return
		}
		id, err := uuid.Parse(chi.URLParam(request, "operation_id"))
		if err != nil {
			http.Error(writer, "invalid operation ID", http.StatusBadRequest)
			return
		}
		events, cancel := operations.Subscribe(id)
		defer cancel()
		if _, err := operations.Snapshot(request.Context(), id); err != nil {
			http.Error(writer, "operation not found", http.StatusNotFound)
			return
		}
		flusher, ok := writer.(http.Flusher)
		if !ok {
			http.Error(writer, "streaming is unavailable", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Cache-Control", "no-cache, no-store")
		writer.Header().Set("X-Accel-Buffering", "no")
		writer.WriteHeader(http.StatusOK)
		flusher.Flush()
		for {
			select {
			case <-request.Context().Done():
				return
			case <-events:
				if _, err := fmt.Fprintf(writer, "event: operation-changed\ndata: %s\n\n", id); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}
