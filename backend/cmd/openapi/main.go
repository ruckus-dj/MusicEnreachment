package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
)

func main() {
	output := flag.String("output", "", "write OpenAPI JSON to this file")
	flag.Parse()
	writer := os.Stdout
	if *output != "" {
		file, err := os.Create(*output)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
		writer = file
	}
	if err := json.NewEncoder(writer).Encode(api.New(chi.NewRouter()).OpenAPI()); err != nil {
		log.Fatal(err)
	}
}
