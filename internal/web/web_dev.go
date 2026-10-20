//go:build dev

package web

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// 开发形态：把页面请求反向代理到 Vite dev server，享受 HMR。
// 用法：`go run -tags dev ./cmd/imgen serve`，并另开终端 `pnpm --dir web dev`。
//
// /api 与 /assets 不走代理 —— 由后端自己处理，Vite 的 proxy 只用于页内 fetch 兜底。
const devServer = "http://127.0.0.1:5173"

func newStaticHandler(boot Boot) (http.Handler, error) {
	target, err := url.Parse(devServer)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	// dev 下不需要注入 boot script：由 web/index.html 或 vite 插件直接提供。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebSocket 用于 HMR，反向代理默认支持 upgrade。
		proxy.ServeHTTP(w, r)
	}), nil
}
