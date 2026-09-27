package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
)

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(api.New(chi.NewRouter()).OpenAPI()); err != nil {
		log.Fatal(err)
	}
}
