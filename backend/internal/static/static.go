package static

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
)

// Files contains the Vite production build staged here by Task.
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
	fileServer := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if shouldServeIndex(request.URL.Path) {
			request = request.Clone(request.Context())
			request.URL.Path = "/"
		}
		fileServer.ServeHTTP(writer, request)
	})
}

// shouldServeIndex keeps client-side routes reloadable while allowing missing
// static assets to retain their normal 404 response.
func shouldServeIndex(requestPath string) bool {
	return requestPath != "/" && path.Ext(requestPath) == ""
}
