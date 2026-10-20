//go:build imgenweb && !dev

package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// 真实前端构建：先 `pnpm --dir web build` 生成 dist，再 `go build -tags imgenweb`。
//
//go:embed all:dist
var distFS embed.FS

func newStaticHandler(boot Boot) (http.Handler, error) {
	root := subFS(distFS, "dist")

	raw, err := fs.ReadFile(root, "index.html")
	if err != nil {
		return nil, err
	}
	index := serveIndex(raw, boot)

	fileServer := http.FileServer(http.FS(root))
	return spaFallback(root, fileServer, index), nil
}
