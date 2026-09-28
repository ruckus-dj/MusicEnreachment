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
		defer func() {
			if err := file.Close(); err != nil {
				log.Printf("close OpenAPI output: %v", err)
			}
		}()
		writer = file
	}
	humaAPI := api.New(chi.NewRouter())
	api.RegisterAll(humaAPI, api.Dependencies{})
	if err := json.NewEncoder(writer).Encode(humaAPI.OpenAPI()); err != nil {
		log.Fatal(err)
	}
}
