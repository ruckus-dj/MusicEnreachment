package main

import (
	"context"
	"log"
	"os"

	"github.com/ruckus/MusicEnreachment/backend/internal/app"
)

func main() {
	if err := app.Run(context.Background(), app.Config{DatabaseURL: os.Getenv("DATABASE_URL")}); err != nil {
		log.Fatal(err)
	}
}
