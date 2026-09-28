package static

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
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
		writer.Header().Set("Cache-Control", cacheControl(request.URL.Path))
		if shouldServeIndex(request.URL.Path) {
			request = request.Clone(request.Context())
			request.URL.Path = "/"
		}
		fileServer.ServeHTTP(writer, request)
	})
}

func cacheControl(requestPath string) string {
	if strings.HasPrefix(requestPath, "/assets/") {
		return "public, max-age=31536000, immutable"
	}
	return "no-cache"
}

// shouldServeIndex keeps client-side routes reloadable while allowing missing
// static assets to retain their normal 404 response.
func shouldServeIndex(requestPath string) bool {
	return requestPath != "/" && path.Ext(requestPath) == ""
}
