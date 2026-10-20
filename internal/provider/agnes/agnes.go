// Package agnes 实现 Agnes AI 图像通道适配器。
//
// 端点与参数依据 https://agnes-ai.cn/zh-Hans/docs/agnes-image-25-flash 整理。
// ⚠️ 参考图字段位置存在文档矛盾：参数表写在顶层 `image`，而「重要说明」与全部示例
// 写在 `extra_body.image`。本实现默认走 extra_body.image（符合官方注意事项的强调），
// 并提供顶层回落开关 TopLevelImage。M0 实测（2026-10-15）确认 extra_body.image 可用，
// 顶层 image 因上游排队未拿到成功样本，见 docs/providers/README.md。
package agnes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/provider"
)

const (
	id        = domain.ProviderAgnes
	defBase   = "https://apihub.agnes-ai.cn/v1"
	defTtlSec = 180
	// maxRefs 为 2026-10-15 实测值：9 张返回 400 "too many input images: 9 provided, at most 6 allowed"。
	maxRefsGuess = 6
)

type Provider struct {
	BaseURL string
	TTL     time.Duration
	// TopLevelImage 为真时把参考图放在请求体顶层 `image` 字段。
	// 默认 false，走 extra_body.image。
	// M0 实测（2026-10-15）确认 extra_body.image 可用，顶层 image 待补测。
	TopLevelImage bool
}

var _ provider.Provider = (*Provider)(nil)

func New(baseURL string) *Provider {
	if baseURL == "" {
		baseURL = defBase
	}
	return &Provider{BaseURL: strings.TrimRight(baseURL, "/"), TTL: defTtlSec * time.Second}
}

func (p *Provider) ID() domain.ProviderID { return id }
func (p *Provider) Name() string          { return "Agnes AI" }

func (p *Provider) Models() []domain.Model {
	return []domain.Model{
		{ID: "agnes-image-2.5-flash", Name: "Agnes Image 2.5 Flash"},
		{ID: "agnes-image-2.1-flash", Name: "Agnes Image 2.1 Flash"},
		{ID: "agnes-image-2.0-flash", Name: "Agnes Image 2.0 Flash"},
	}
}

func (p *Provider) Capabilities(model string) provider.Caps {
	return provider.Caps{
		Img2Img:      true,
		Compose:      true,
		NoWatermark:  false, // 上游无此参数
		PromptExtend: false, // 上游无此参数
		// 上游无 output_format，固定返回 png。
		OutputFormats:    []domain.Format{domain.FormatPNG},
		MaxRefs:          maxRefsGuess, // M0 实测后修正
		Ratios:           domain.AllAspects,
		ExactSize:        false, // 用「档位 + ratio」表达
		NativeMultiImage: false,
		TimeoutSec:       defTtlSec,
	}
}

type extraBody struct {
	// 官方文档明确要求 response_format 放在 extra_body 内，
	// 放在顶层是被列为错误示例的用法。
	ResponseFormat string   `json:"response_format,omitempty"`
	Image          []string `json:"image,omitempty"`
}

type body struct {
	Model         string    `json:"model"`
	Prompt        string    `json:"prompt"`
	Size          string    `json:"size"`  // 1K | 2K | 3K | 4K
	Ratio         string    `json:"ratio"` // 1:1 | 3:4 | ...
	Extra         extraBody `json:"extra_body,omitempty"`
	TopLevelImage []string  `json:"image,omitempty"` // 仅 TopLevelImage 模式
}

func (p *Provider) Translate(r domain.Request, refs []string) (provider.Payload, error) {
	if strings.TrimSpace(r.Prompt) == "" {
		return provider.Payload{}, fmt.Errorf("%w: prompt 不能为空", domain.ErrBadRequest)
	}
	if !isValidTier(r.Tier) {
		return provider.Payload{}, fmt.Errorf("%w: 档位 %q 非法，应为 1K/2K/3K/4K", domain.ErrBadRequest, r.Tier)
	}
	if !isValidRatio(r.Aspect) {
		return provider.Payload{}, fmt.Errorf("%w: 宽高比 %q 不受支持", domain.ErrBadRequest, r.Aspect)
	}
	if r.Mode != domain.ModeTxt2Img {
		if len(refs) == 0 {
			return provider.Payload{}, fmt.Errorf("%w: %s 模式至少需要一张参考图", domain.ErrBadRequest, r.Mode)
		}
		if len(refs) > maxRefsGuess {
			return provider.Payload{}, fmt.Errorf("%w: 参考图最多 %d 张，收到 %d", domain.ErrBadRequest, maxRefsGuess, len(refs))
		}
	}

	b := body{
		Model:  r.Model,
		Prompt: r.Prompt,
		Size:   string(r.Tier),
		Ratio:  string(r.Aspect),
		// 一律取 base64：url 托管在第三方，有效期不明。
		Extra: extraBody{ResponseFormat: "b64_json"},
	}

	if r.Mode != domain.ModeTxt2Img {
		if p.TopLevelImage {
			b.TopLevelImage = refs
		} else {
			b.Extra.Image = refs
		}
	}

	// 文生图与图生图同端点，靠 image 字段区分。
	return provider.Payload{
		Method: http.MethodPost,
		Path:   "/images/generations",
		Body:   b,
	}, nil
}

func isValidTier(t domain.Tier) bool {
	for _, x := range domain.AllTiers {
		if x == t {
			return true
		}
	}
	return false
}

func isValidRatio(a domain.Aspect) bool {
	for _, x := range domain.AllAspects {
		if x == a {
			return true
		}
	}
	return false
}

type dataItem struct {
	URL           *string `json:"url"`
	B64JSON       *string `json:"b64_json"`
	RevisedPrompt *string `json:"revised_prompt"`
}

type respBody struct {
	Created int64      `json:"created"`
	Data    []dataItem `json:"data"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (p *Provider) Extract(raw []byte) (provider.Parsed, error) {
	var out respBody
	if err := json.Unmarshal(raw, &out); err != nil {
		return provider.Parsed{Raw: raw}, fmt.Errorf("响应不是合法 JSON: %w", err)
	}
	if out.Error != nil {
		return provider.Parsed{Raw: raw}, fmt.Errorf("上游返回错误: %s", out.Error.Message)
	}

	got := provider.Parsed{Raw: raw}
	for _, d := range out.Data {
		if d.RevisedPrompt != nil && *d.RevisedPrompt != "" {
			got.RevisedPrompt = *d.RevisedPrompt
		}
		if d.B64JSON != nil && *d.B64JSON != "" {
			got.B64 = append(got.B64, *d.B64JSON)
		}
		if d.URL != nil && *d.URL != "" {
			got.URLs = append(got.URLs, *d.URL)
		}
	}
	// 该上游不返回 usage，按图片数记账。
	got.Usage = domain.Usage{ImagesCount: len(out.Data)}
	return got, nil
}

func (p *Provider) Classify(status int, raw []byte) *domain.Error {
	const prov = "agnes"
	msg := truncate(string(raw), 512)
	typ := ""
	var probe respBody
	if json.Unmarshal(raw, &probe) == nil && probe.Error != nil {
		typ = probe.Error.Type
		if probe.Error.Message != "" {
			msg = truncate(probe.Error.Message, 512)
		}
	}

	switch status {
	case http.StatusUnauthorized:
		return &domain.Error{Code: domain.CodeUpstreamAuth, Message: fallback(msg, "API Key 无效"),
			Provider: prov, UpstreamStatus: status, UpstreamType: typ}
	case http.StatusForbidden:
		return &domain.Error{Code: domain.CodeUpstreamAuth, Message: fallback(msg, "无权访问该模型"),
			Provider: prov, UpstreamStatus: status, UpstreamType: typ}
	case http.StatusPaymentRequired:
		return &domain.Error{Code: domain.CodeUpstreamQuota, Message: fallback(msg, "余额或订阅配额不足"),
			Provider: prov, UpstreamStatus: status}
	case http.StatusTooManyRequests:
		return &domain.Error{Code: domain.CodeUpstreamQuota, Message: fallback(msg, "超出 RPM 限制，请稍后重试"),
			Provider: prov, UpstreamStatus: status, UpstreamType: typ, Retryable: true}
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return &domain.Error{Code: domain.CodeUpstreamTimeout, Message: fallback(msg, "上游超时，请降低尺寸档位"),
			Provider: prov, UpstreamStatus: status, Retryable: true}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return &domain.Error{Code: domain.CodeUpstreamRejected, Message: fallback(msg, "参数被上游拒绝"),
			Provider: prov, UpstreamStatus: status, UpstreamType: typ}
	case http.StatusRequestEntityTooLarge:
		return &domain.Error{Code: domain.CodeUpstreamRejected, Message: fallback(msg, "请求体过大，请减少参考图或降低尺寸"),
			Provider: prov, UpstreamStatus: status}
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		// 实测：上游队列打满时返回 503 且消息为 "image queue is full"。
		// 这不是故障而是排队，退避重试通常能过；单独给一句人话。
		if strings.Contains(strings.ToLower(msg), "queue is full") {
			msg = "上游生成队列已满，正在退避重试，请稍后重试"
		}
		return &domain.Error{Code: domain.CodeUpstreamUnavailable, Message: fallback(msg, "上游暂时不可用"),
			Provider: prov, UpstreamStatus: status, Retryable: true}
	}

	if status >= 200 && status < 300 && probe.Error != nil {
		return &domain.Error{Code: domain.CodeUpstreamRejected, Message: msg,
			Provider: prov, UpstreamStatus: status, UpstreamType: typ}
	}
	return &domain.Error{Code: domain.CodeUpstreamRejected, Message: fallback(msg, "未知上游错误"),
		Provider: prov, UpstreamStatus: status}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
