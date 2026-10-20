//go:build !imgenweb && !dev

package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// 纯后端构建：嵌入占位页，保证 `go build ./...` 在没有前端产物时也能通过。
//
//go:embed all:dist_stub
var stubFS embed.FS

func newStaticHandler(boot Boot) (http.Handler, error) {
	root := subFS(stubFS, "dist_stub")

	raw, err := fs.ReadFile(root, "index.html")
	if err != nil {
		return nil, err
	}
	index := serveIndex(raw, boot)

	fileServer := http.FileServer(http.FS(root))
	return spaFallback(root, fileServer, index), nil
}
