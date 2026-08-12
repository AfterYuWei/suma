package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed dist
var content embed.FS

func Handler() http.Handler {
	root, _ := fs.Sub(content, "dist")
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if name == "." {
			name = "index.html"
		}
		if _, err := fs.Stat(root, name); err != nil {
			request.URL.Path = "/"
		}
		files.ServeHTTP(writer, request)
	})
}
