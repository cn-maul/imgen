// Package web 负责前端产物的嵌入、启动配置注入与 SPA fallback。
//
// 三种构建形态由 build tag 选择，纯后端编译（无 tag）永远可用：
//
//	默认        嵌入 dist_stub 占位页，保证 `go build ./...` 通过
//	-tags imgenweb 嵌入真实 dist（需先 `pnpm --dir web build`）
//	-tags dev      反向代理到 Vite dev server（127.0.0.1:5173）
package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Boot 是注入到 index.html 的启动配置，前端通过 window.__IMGEN__ 读取。
type Boot struct {
	Version     string `json:"version"`
	BuildTime   string `json:"buildTime"`
	MaxUploadMB int    `json:"maxUploadMB"`
	Concurrency int    `json:"concurrency"`
}

// Handler 返回静态文件处理器。assetsRoot 为空时由各构建形态自行决定。
func Handler(boot Boot) (http.Handler, error) {
	return newStaticHandler(boot)
}

// serveIndex 返回注入了启动配置的 index.html。
// 支持两种注入点：显式的 %IMGEN_BOOT% 标记，或退化到 <head> 之后追加。
func serveIndex(raw []byte, boot Boot) []byte {
	script, err := bootScript(boot)
	if err != nil {
		script = `window.__IMGEN__={}`
	}
	const marker = "%IMGEN_BOOT%"
	if strings.Contains(string(raw), marker) {
		return []byte(strings.Replace(string(raw), marker, script, 1))
	}
	// 无标记时在 <head> 后插入，确保 Vue 应用启动前已可用。
	head := "<head>"
	if i := indexFold(string(raw), head); i >= 0 {
		out := string(raw[:i+len(head)]) + "\n<script>" + script + "</script>" + string(raw[i+len(head):])
		return []byte(out)
	}
	return append([]byte("<script>"+script+"</script>"), raw...)
}

func bootScript(boot Boot) (string, error) {
	raw, err := json.Marshal(boot)
	if err != nil {
		return "", err
	}
	return "window.__IMGEN__=" + string(raw) + ";", nil
}

// indexFold 是大小写不敏感的子串查找，用于匹配 <head> / <HEAD>。
func indexFold(s, sub string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(sub))
}

// spaFallback 优先提供真实静态文件，未命中且不像静态资源时回落到 index.html。
func spaFallback(root fs.FS, fileServer http.Handler, index []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只接受安全的静态资源方法。
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" && existsFile(root, p) {
			fileServer.ServeHTTP(w, r)
			return
		}
		// 带扩展名却不存在 → 真实 404。否则缺失的 js/css 会拿到 HTML，
		// 被浏览器当成脚本解析并报语法错，掩盖了真正的「资源缺失」。
		if p != "" && strings.Contains(lastSegment(p), ".") {
			http.NotFound(w, r)
			return
		}
		writeIndex(w, r, index)
	})
}

// existsFile 判断 p 是否是 root 下的一个普通文件。
func existsFile(root fs.FS, p string) bool {
	clean := path.Clean(strings.TrimPrefix(p, "/"))
	if clean == "." || strings.HasPrefix(clean, "..") {
		return false
	}
	info, err := fs.Stat(root, filepath.ToSlash(clean))
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func writeIndex(w http.ResponseWriter, r *http.Request, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// SPA 首屏必须不缓存，否则升级后浏览器仍用旧 index 引用已消失的 hash 产物。
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(index)))
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(index)))
	_, _ = w.Write(index)
}

// subFS 从嵌入的 FS 中取出 dist 子树；不存在时回退到根。
func subFS(f fs.FS, name string) fs.FS {
	if sub, err := fs.Sub(f, name); err == nil {
		return sub
	}
	return f
}
