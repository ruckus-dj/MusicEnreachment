package static

import (
	"embed"
	"io/fs"
	"net/http"
)

// Files contains the Vite production build copied here by scripts/build-frontend.sh.
//
//go:embed dist
var Files embed.FS

func Handler() http.Handler {
	assets, err := fs.Sub(Files, "dist")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(assets))
}
