package static

import (
	"embed"
	"io/fs"
	"net/http"
)

// Files contains the Vite production build staged here by scripts/build-frontend.sh.
// The generated files are ignored by Git; .gitkeep only makes a clean checkout
// compilable before a frontend build is staged.
//
//go:embed dist/*
var Files embed.FS

func Handler() http.Handler {
	assets, err := fs.Sub(Files, "dist")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(assets))
}
