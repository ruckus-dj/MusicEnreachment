package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func New(router chi.Router) huma.API {
	config := huma.DefaultConfig("MeloTrove API", "0.0.0")
	config.Transformers = append(config.Transformers, redactAcoustIDKeyErrors)
	return humachi.New(router, config)
}

func redactAcoustIDKeyErrors(ctx huma.Context, status string, response any) (any, error) {
	if ctx.Operation().OperationID != "set-acoustid-application-key" || len(status) != 3 || status[0] != '4' {
		return response, nil
	}
	errorResponse, ok := response.(*huma.ErrorModel)
	if !ok {
		return response, nil
	}
	errorResponse.Detail = "invalid AcoustID application key request"
	errorResponse.Errors = nil
	return errorResponse, nil
}

func Handler() http.Handler {
	router := chi.NewRouter()
	New(router)
	return router
}

func HandlerWithSetup(setup *service.SetupService) http.Handler {
	return HandlerWithDependencies(Dependencies{Setup: setup})
}

func HandlerWithDependencies(dependencies Dependencies) http.Handler {
	router := chi.NewRouter()
	api := New(router)
	RegisterAll(api, dependencies)
	registerOperationEvents(router, dependencies.Operations)
	return router
}
