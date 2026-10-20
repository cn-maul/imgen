// Package httpapi 实现 HTTP 路由层：把领域能力暴露为 /api/v1 下的 REST + SSE 接口。
//
// 职责边界：
//   - 只做参数校验、错误归一化、DTO 组装；
//   - 不碰上游协议（那是 internal/provider 的事）；
//   - 不管任务调度（那是 internal/job 的事）；
//   - 不做 SPA fallback（那是 internal/web 的事）。
package httpapi

import (
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/imgen/imgen/internal/blob"
	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/httpx"
	"github.com/imgen/imgen/internal/job"
	"github.com/imgen/imgen/internal/provider"
	"github.com/imgen/imgen/internal/secret"
	"github.com/imgen/imgen/internal/store"
)

// APIPrefix 是本路由层挂载的前缀。internal/web 负责其余路径。
const APIPrefix = "/api/v1"

// 设置项在 kv 表中的键名。
const (
	kvMaxUploadMB = "settings.max_upload_mb"
	kvKeepDays    = "settings.keep_days"
	kvConcurrency = "settings.concurrency"
)

// kvUnset 是 kvInt 的「未设置」哨兵，见 settings.go 中 kvInt 的注释。
// 三个设置项下界均 ≥0，故 -1 不会与合法值撞车。
const kvUnset = -1

// 设置项的合法区间。
const (
	minConcurrency = 1
	maxConcurrency = 16
	minUploadMB    = 1
	maxUploadMBCap = 512
	minKeepDays    = 0
	maxKeepDays    = 365
)

// defaultMaxUploadMB 与 defaultKeepDays 是 Deps 未注入 / kv 为空时的兜底，
// 与 internal/config 的默认值保持一致。
const (
	defaultMaxUploadMB = 20
	defaultKeepDays    = 30
)

// Deps 是 httpapi 需要的全部依赖，由 cmd/imgen 与 internal/app 装配。
type Deps struct {
	Log       *slog.Logger
	St        *store.Store
	Blobs     *blob.Store
	Jobs      *job.Manager
	Providers map[domain.ProviderID]provider.Provider
	// Keys 用于 provider/test 探测连通性时取真实 Key。
	Keys job.KeyResolver
	// Cipher 用于 PATCH provider 时加密 apiKey、读回时解密做掩码。
	Cipher *secret.Cipher
	// MaxUploadMB 来自 config，PUT settings 可改。
	MaxUploadMB int
	// KeepDays 来自 config，PUT settings 可改；0 表示不清理。
	// janitor 通过 API.KeepDays() 读取运行期值。
	KeepDays int
}

// API 持有 handler 之间共享的状态与依赖。
type API struct {
	deps Deps
	log  *slog.Logger
	// up 复用于 provider/test 的连通性探测，避免每次请求新建连接池。
	up *httpx.Client

	// maxUploadMB 与 keepDays 运行期可改，用原子量保护。
	maxUploadMB int64
	keepDays    int64
}

// New 构造 API。装配层随后调用 Handler 拿到挂在 /api/v1 下的路由。
func New(d Deps) *API {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	a := &API{deps: d, log: log, up: httpx.New(log)}

	mb := d.MaxUploadMB
	if mb <= 0 {
		mb = defaultMaxUploadMB
	}
	a.maxUploadMB = int64(mb)
	// 保留天数带哨兵语义：只有 Config 明确给了值才覆盖默认。
	// 0 是合法值（不清理），不能像 MaxUploadMB 那样把 0 当「未提供」。
	if d.KeepDays >= 0 {
		a.keepDays = int64(clamp(d.KeepDays, minKeepDays, maxKeepDays))
	} else {
		a.keepDays = defaultKeepDays
	}

	// 已持久化的设置覆盖注入值，使重启后仍然生效。
	a.loadPersistedSettings()
	return a
}

// loadPersistedSettings 从 kv 读回设置并应用到运行期状态。
// 出错不影响启动：最多退回注入值。
func (a *API) loadPersistedSettings() {
	if a.deps.St == nil {
		return
	}
	if v, ok := a.kvInt(kvMaxUploadMB); ok && v >= minUploadMB && v <= maxUploadMBCap {
		a.maxUploadMB = int64(v)
	}
	if v, ok := a.kvInt(kvKeepDays); ok && v >= minKeepDays && v <= maxKeepDays {
		a.keepDays = int64(v)
	}
	if v, ok := a.kvInt(kvConcurrency); ok && a.deps.Jobs != nil && a.deps.Jobs.Q != nil {
		a.deps.Jobs.Q.SetWorkers(clamp(v, minConcurrency, maxConcurrency))
	}
}

// MaxUploadMB 返回当前上传体积上限（MB）。供 internal/web 注入前端启动参数。
func (a *API) MaxUploadMB() int { return int(atomic.LoadInt64(&a.maxUploadMB)) }

// KeepDays 返回当前保留天数。
func (a *API) KeepDays() int { return int(atomic.LoadInt64(&a.keepDays)) }

// setMaxUploadMB 更新上传上限（MB）。
func (a *API) setMaxUploadMB(mb int) {
	if mb <= 0 {
		mb = defaultMaxUploadMB
	}
	atomic.StoreInt64(&a.maxUploadMB, int64(clamp(mb, minUploadMB, maxUploadMBCap)))
}

// setKeepDays 更新保留天数。
func (a *API) setKeepDays(days int) {
	if days < 0 {
		days = defaultKeepDays
	}
	atomic.StoreInt64(&a.keepDays, int64(clamp(days, minKeepDays, maxKeepDays)))
}

// SetKeepDaysForTest 供跨包测试直接改运行期保留天数，
// 避免为了验证「janitor 读的是运行期值」而重复搭一套 HTTP 请求。
// 生产代码请走 PUT /settings（那里有校验与持久化）。
func (a *API) SetKeepDaysForTest(days int) { a.setKeepDays(days) }

// Handler 返回挂载在 /api/v1 下的完整路由。
func (a *API) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(a.logRequest)

	r.Route(APIPrefix, func(r chi.Router) {
		r.Get("/health", a.health)

		r.Route("/providers", func(r chi.Router) {
			r.Get("/", a.listProviders)
			r.Route("/{id}", func(r chi.Router) {
				r.Patch("/", a.patchProvider)
				r.Post("/test", a.testProvider)
			})
		})

		r.Route("/jobs", func(r chi.Router) {
			r.Post("/", a.createJob)
			r.Get("/", a.listJobs)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", a.getJob)
				r.Get("/events", a.jobEvents)
				r.Post("/cancel", a.cancelJob)
			})
		})

		r.Route("/assets", func(r chi.Router) {
			r.Post("/", a.uploadAssets)
			r.Get("/", a.listAssets)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/raw", a.serveAssetRaw)
				r.Get("/thumb", a.serveAssetThumb)
				r.Delete("/", a.deleteAsset)
			})
		})

		r.Get("/usage", a.usage)

		// 提示词预设：图生图 / 多图合成的可复用模板。
		// 刻意独立于 jobs —— 预设是可编辑草稿，job 是不可变历史。
		r.Route("/presets", func(r chi.Router) {
			r.Get("/", a.listPresets)
			r.Post("/", a.createPreset)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", a.getPreset)
				r.Put("/", a.updatePreset)
				r.Delete("/", a.deletePreset)
			})
		})

		r.Route("/settings", func(r chi.Router) {
			r.Get("/", a.getSettings)
			r.Put("/", a.putSettings)
		})
	})

	return r
}

// logRequest 记录访问日志。健康检查与静态资源不记，避免刷屏。
func (a *API) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if r.URL.Path == APIPrefix+"/health" {
			return
		}
		a.log.Debug("http", "method", r.Method, "path", r.URL.Path,
			"status", ww.Status(), "bytes", ww.BytesWritten(), "dur", time.Since(start).String())
	})
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthDTO{
		OK:   true,
		API:  "v1",
		Time: time.Now().UTC(),
	})
}

// clamp 把 v 夹到 [lo, hi]。
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
