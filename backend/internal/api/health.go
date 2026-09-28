package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

type databasePinger interface {
	PingContext(context.Context) error
}

func Liveness(writer http.ResponseWriter, _ *http.Request) {
	writeHealth(writer, http.StatusOK, "live")
}

func Readiness(database databasePinger) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if database == nil {
			writeHealth(writer, http.StatusServiceUnavailable, "not_ready")
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()
		if err := database.PingContext(ctx); err != nil {
			writeHealth(writer, http.StatusServiceUnavailable, "not_ready")
			return
		}
		writeHealth(writer, http.StatusOK, "ready")
	}
}

func writeHealth(writer http.ResponseWriter, status int, state string) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(`{"status":"` + state + `"}`))
}

var _ databasePinger = (*sql.DB)(nil)
