package app

import (
	"github.com/imgen/imgen/internal/httpapi"
)

// newAPI 是 httpapi 的装配接缝：把 App 的字段映射为 httpapi.Deps。
//
// 设置（上传上限 / 并发 / 保留天数）由 httpapi 自己经 kv 表读写并回调 Queue.SetWorkers，
// 故这里只负责一次性注入初始值。
//
// 返回 *API 而非裸 Handler：janitor 需要读运行期的 keepDays，
// 而 keepDays 的权威值在 API 里（随 PUT /settings 变化）。
func newAPI(a *App) (*httpapi.API, error) {
	api := httpapi.New(httpapi.Deps{
		Log:         a.Log,
		St:          a.St,
		Blobs:       a.Blobs,
		Jobs:        a.Jobs,
		Providers:   a.Provs,
		Keys:        a.Keys,
		Cipher:      a.Cipher,
		MaxUploadMB: a.Cfg.MaxUploadMB,
		KeepDays:    a.Cfg.KeepDays,
	})
	return api, nil
}
