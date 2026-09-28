package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

func New(router chi.Router) huma.API {
	return humachi.New(router, huma.DefaultConfig("MeloTrove API", "0.0.0"))
}

func Handler() http.Handler {
	router := chi.NewRouter()
	New(router)
	return router
}
