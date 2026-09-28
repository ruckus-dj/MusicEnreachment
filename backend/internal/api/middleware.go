package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

func RequestIDHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Request-ID", middleware.GetReqID(request.Context()))
		next.ServeHTTP(writer, request)
	})
}

func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			started := time.Now()
			wrapped := middleware.NewWrapResponseWriter(writer, request.ProtoMajor)
			next.ServeHTTP(wrapped, request)
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			if status < http.StatusBadRequest && strings.HasPrefix(request.URL.Path, "/health/") {
				return
			}
			attributes := []any{
				"request_id", middleware.GetReqID(request.Context()),
				"method", request.Method,
				"path", request.URL.Path,
				"status", status,
				"duration_ms", time.Since(started).Milliseconds(),
				"bytes", wrapped.BytesWritten(),
			}
			switch {
			case status >= http.StatusInternalServerError:
				logger.ErrorContext(request.Context(), "HTTP request", attributes...)
			case status >= http.StatusBadRequest:
				logger.WarnContext(request.Context(), "HTTP request", attributes...)
			default:
				logger.InfoContext(request.Context(), "HTTP request", attributes...)
			}
		})
	}
}

func RecoverPanics(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					requestID := middleware.GetReqID(request.Context())
					logger.ErrorContext(request.Context(), "HTTP handler panic",
						"request_id", requestID,
						"panic", recovered,
						"stack", string(debug.Stack()),
					)
					if responseStarted(writer) {
						return
					}
					writer.Header().Set("Content-Type", "application/json")
					writer.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(writer).Encode(map[string]string{
						"error":     "internal_error",
						"requestId": requestID,
					})
				}
			}()
			next.ServeHTTP(writer, request)
		})
	}
}

func responseStarted(writer http.ResponseWriter) bool {
	wrapped, ok := writer.(interface{ Status() int })
	return ok && wrapped.Status() != 0
}
