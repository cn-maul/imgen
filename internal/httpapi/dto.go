package httpapi

import (
	"time"

	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/provider"
	"github.com/imgen/imgen/internal/store"
)

// ============================================================
// 公共
// ============================================================

type healthDTO struct {
	OK   bool      `json:"ok"`
	API  string    `json:"api"`
	Time time.Time `json:"time"`
}

// paged 是所有分页响应的公共外层。
type paged struct {
	Items  any `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// ============================================================
// Providers
// ============================================================

// providerDTO 是 GET /api/v1/providers 的单条记录。
// 字段名必须稳定：前端靠它驱动参数面板的显隐。
type providerDTO struct {
	ID string `json:"id"`
	// Name 优先用 DB 里的展示名，缺失时回落到适配器的 Name()。
	Name string `json:"name"`

	BaseURL string `json:"baseUrl"`
	// Enabled 为假时该通道不可发起任务。
	Enabled bool `json:"enabled"`
	// HasKey 指示是否已在 DB 中配置 Key。
	HasKey bool `json:"hasKey"`
	// APIKeyMask 是掩码后的 Key 预览；明文永不外泄。
	APIKeyMask string `json:"apiKeyMask,omitempty"`

	TimeoutMS int  `json:"timeoutMs"`
	CreatedAt time.Time `json:"createdAt"`

	Models []domain.Model `json:"models"`
	// Caps 每模型一份，来自适配器的 Capabilities(model)。
	Caps map[string]provider.Caps `json:"caps"`
}

type providerListDTO struct {
	Providers []providerDTO `json:"providers"`
}

// patchProviderRequest 是 PATCH /api/v1/providers/:id 的请求体。
// 所有字段可选（指针表示「是否出现」）。
type patchProviderRequest struct {
	BaseURL *string `json:"baseUrl"`
	APIKey  *string `json:"apiKey"`
	// Enabled 独立非空，便于显式关闭通道。
	Enabled *bool `json:"enabled"`
	// TimeoutMS 覆盖单次上游超时。
	TimeoutMS *int `json:"timeoutMs"`
}

// providerMutationDTO 是写操作后的回显，与列表同构便于前端直接合并。
type providerMutationDTO struct {
	Provider providerDTO `json:"provider"`
}

// testProviderRequest 是 POST /api/v1/providers/:id/test 的可选请求体。
type testProviderRequest struct {
	// Model 为空时取该通道的第一个模型。
	Model string `json:"model,omitempty"`
	// TimeoutSec 覆盖默认探测超时。
	TimeoutSec int `json:"timeoutSec,omitempty"`
}

// testProviderDTO 是连通性探测结果。
type testProviderDTO struct {
	OK bool `json:"ok"`
	// Model 是实际探测使用的模型。
	Model string `json:"model"`
	// Status 是上游 HTTP 状态码；网络不通时为 0。
	Status int `json:"upstreamStatus,omitempty"`
	// LatencyMS 是首字节耗时。
	LatencyMS int64          `json:"latencyMs"`
	Message   string         `json:"message,omitempty"`
	Error     *domain.Error  `json:"error,omitempty"`
	CheckedAt time.Time      `json:"checkedAt"`
}

// ============================================================
// Jobs
// ============================================================

// createJobRequest 是 POST /api/v1/jobs 的请求体。
// 与 domain.Request 同构但显式列出，便于给出精确的 400 提示。
type createJobRequest struct {
	Provider string           `json:"provider"`
	Model    string           `json:"model"`
	Prompt   string           `json:"prompt"`
	Refs     []domain.Ref     `json:"refs,omitempty"`
	Mode     domain.Mode      `json:"mode"`
	Aspect   domain.Aspect    `json:"aspect"`
	Tier     domain.Tier      `json:"tier"`
	Count    int              `json:"count"`

	NoWatermark  *bool         `json:"noWatermark,omitempty"`
	PromptExtend *bool         `json:"promptExtend,omitempty"`
	OutputFormat domain.Format `json:"outputFormat,omitempty"`
}

// createJobDTO 是建任务的立即响应：只回 job_id，余下的靠轮询或 SSE。
type createJobDTO struct {
	JobID  string `json:"jobId"`
	Status string `json:"status"`
}

// jobDTO 是 GET /api/v1/jobs/:id 的响应。
// 直接用 domain.Job 序列化：字段本身就是给前端看的。
type jobDTO = domain.Job

// assetDTO 是资产列表条目。domain.Asset 内部字段用 json:"-" 排除，
// 不会外泄磁盘路径。
type assetDTO = domain.Asset

// ============================================================
// Settings / Usage
// ============================================================

// settingsDTO 是 GET /api/v1/settings 的响应。
type settingsDTO struct {
	Concurrency int `json:"concurrency"`
	MaxUploadMB int `json:"maxUploadMB"`
	KeepDays    int `json:"keepDays"`
	// QueueDepth 是当前排队数，供前端显示水位。
	QueueDepth int `json:"queueDepth"`
}

// settingsMutationDTO 是 PUT /api/v1/settings 的响应，回显生效后的值。
type settingsMutationDTO struct {
	Settings settingsDTO `json:"settings"`
}

// putSettingsRequest 是 PUT /api/v1/settings 的请求体。
// 全部可选：只更新出现的键。
type putSettingsRequest struct {
	Concurrency *int `json:"concurrency"`
	MaxUploadMB *int `json:"maxUploadMB"`
	KeepDays    *int `json:"keepDays"`
}

// usageDTO 是 GET /api/v1/usage 的响应。
type usageDTO struct {
	Days  int                  `json:"days"`
	Items []store.UsageStat    `json:"items"`
}
