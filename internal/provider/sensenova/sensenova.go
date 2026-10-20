// Package sensenova 实现商汤日日新图像通道适配器。
//
// 端点与参数依据 https://platform.sensenova.cn/docs 实测整理，
// 坑位记录见 docs/providers/sensenova.md。
package sensenova

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/provider"
)

const (
	id        = domain.ProviderSenseNova
	defBase   = "https://token.sensenova.cn/v1"
	maxRefs   = 5
	defTtlSec = 180
)

// maxEdge 是上游的单边像素上限。
const maxEdge = 4096

// recTable 是官方推荐的 (档位, 宽高比) → 像素尺寸。
// 部分条目（高档位 + 超宽/超高比）会超过 maxEdge，需经 deriveSize 缩放钳制。
var recTable = map[domain.Tier]map[domain.Aspect][2]int{
	domain.Tier1K: {
		domain.Aspect1_1: {1024, 1024}, domain.Aspect3_4: {864, 1152},
		domain.Aspect4_3: {1152, 864}, domain.Aspect16_9: {1312, 736},
		domain.Aspect9_16: {736, 1312}, domain.Aspect2_3: {832, 1248},
		domain.Aspect3_2: {1248, 832}, domain.Aspect21_9: {1568, 672},
	},
	domain.Tier2K: {
		domain.Aspect1_1: {2048, 2048}, domain.Aspect3_4: {1728, 2304},
		domain.Aspect4_3: {2304, 1728}, domain.Aspect16_9: {2720, 1536},
		domain.Aspect9_16: {1536, 2720}, domain.Aspect2_3: {1664, 2496},
		domain.Aspect3_2: {2496, 1664}, domain.Aspect21_9: {3136, 1344},
	},
	domain.Tier3K: {
		domain.Aspect1_1: {3072, 3072}, domain.Aspect3_4: {2592, 3456},
		domain.Aspect4_3: {3456, 2592}, domain.Aspect16_9: {3936, 2208},
		domain.Aspect9_16: {2208, 3936}, domain.Aspect2_3: {2496, 3744},
		domain.Aspect3_2: {3744, 2496}, domain.Aspect21_9: {4704, 2016},
	},
	domain.Tier4K: {
		domain.Aspect1_1: {4096, 4096}, domain.Aspect3_4: {3456, 4608},
		domain.Aspect4_3: {4608, 3456}, domain.Aspect16_9: {5248, 2944},
		domain.Aspect9_16: {2944, 5248}, domain.Aspect2_3: {3328, 4992},
		domain.Aspect3_2: {4992, 3328}, domain.Aspect21_9: {6272, 2688},
	},
}

// deriveSize 由 (档位, 宽高比) 推出合法的像素尺寸。
//
// 上游硬约束：W/H 必须为 32 的倍数、单边 ≤4096（maxEdge）、宽高比 ≤3:1。
// 官方推荐值在「高档位 + 超宽/超高比」时超出单边上限，
// 此时按比例整体缩放并重新对齐到 32 的倍数——优先保住用户请求的宽高比，
// 而不是死守档位标称分辨率。
func deriveSize(tier domain.Tier, aspect domain.Aspect) (int, int, error) {
	byAspect, ok := recTable[tier]
	if !ok {
		return 0, 0, fmt.Errorf("未知档位 %q", tier)
	}
	rec, ok := byAspect[aspect]
	if !ok {
		return 0, 0, fmt.Errorf("档位 %s 不支持宽高比 %s", tier, aspect)
	}

	w, h := rec[0], rec[1]
	if long := max(w, h); long > maxEdge {
		f := float64(maxEdge) / float64(long)
		w = align32(float64(w) * f)
		h = align32(float64(h) * f)
	}
	if w < 512 || h < 512 {
		return 0, 0, fmt.Errorf("档位 %s 与宽高比 %s 组合过小", tier, aspect)
	}
	return w, h, nil
}

func align32(v float64) int {
	return int(math.Round(v/32)) * 32
}

// SizeFor 暴露尺寸推导，供单测与前端预览复用。
func SizeFor(tier domain.Tier, aspect domain.Aspect) (int, int, error) {
	return deriveSize(tier, aspect)
}

type Provider struct {
	BaseURL string
	TTL     time.Duration
}

var _ provider.Provider = (*Provider)(nil)

func New(baseURL string) *Provider {
	if baseURL == "" {
		baseURL = defBase
	}
	return &Provider{BaseURL: strings.TrimRight(baseURL, "/"), TTL: defTtlSec * time.Second}
}

func (p *Provider) ID() domain.ProviderID { return id }
func (p *Provider) Name() string          { return "商汤日日新 SenseNova" }

func (p *Provider) Models() []domain.Model {
	return []domain.Model{
		{ID: "sensenova-u1.5-lite", Name: "SenseNova U1.5 Lite（质量优先）"},
		{ID: "sensenova-u1.5-fast", Name: "SenseNova U1.5 Fast（速度优先）"},
	}
}

func (p *Provider) Capabilities(model string) provider.Caps {
	return provider.Caps{
		Img2Img:      true,
		Compose:      true,
		NoWatermark:  true,
		PromptExtend: true,
		OutputFormats: []domain.Format{
			domain.FormatPNG, domain.FormatJPEG, domain.FormatWebP,
		},
		MaxRefs:          maxRefs,
		Ratios:           domain.AllAspects,
		ExactSize:        true,
		NativeMultiImage: false, // n 仅支持 1
		TimeoutSec:       defTtlSec,
	}
}

type ref struct {
	ImageURL string `json:"image_url"`
}

type body struct {
	Model          string        `json:"model"`
	Prompt         string        `json:"prompt"`
	Size           string        `json:"size,omitempty"`
	N              int           `json:"n"` // 上游仅支持 1
	Watermark      bool          `json:"watermark"`
	PromptExtend   *bool         `json:"prompt_extend,omitempty"`
	ResponseFormat string        `json:"response_format"`
	OutputFormat   domain.Format `json:"output_format,omitempty"`
	Images         []ref         `json:"images,omitempty"`
}

func (p *Provider) Translate(r domain.Request, refs []string) (provider.Payload, error) {
	if strings.TrimSpace(r.Prompt) == "" {
		return provider.Payload{}, fmt.Errorf("%w: prompt 不能为空", domain.ErrBadRequest)
	}
	if r.Mode != domain.ModeTxt2Img {
		if len(refs) == 0 {
			return provider.Payload{}, fmt.Errorf("%w: %s 模式至少需要一张参考图", domain.ErrBadRequest, r.Mode)
		}
		if len(refs) > maxRefs {
			return provider.Payload{}, fmt.Errorf("%w: 参考图最多 %d 张，收到 %d", domain.ErrBadRequest, maxRefs, len(refs))
		}
	}

	w, h, err := SizeFor(r.Tier, r.Aspect)
	if err != nil {
		return provider.Payload{}, fmt.Errorf("%w: %v", domain.ErrBadRequest, err)
	}

	b := body{
		Model:  r.Model,
		Prompt: r.Prompt,
		Size:   fmt.Sprintf("%dx%d", w, h),
		N:      1,
		// 始终显式下发：默认值在公测结束后可能变化。
		Watermark: !r.NoWatermark,
		// 一律取 base64：上游 url 仅 24 小时有效。
		ResponseFormat: "b64_json",
		OutputFormat:   domain.FormatPNG,
		PromptExtend:   r.PromptExtend,
	}
	if caps := p.Capabilities(r.Model); r.OutputFormat != "" && caps.Supports(r.OutputFormat) {
		b.OutputFormat = r.OutputFormat
	}

	// 文生图走生成端点；编辑走独立端点，首张参考图为主图。
	pl := provider.Payload{
		Method: http.MethodPost,
		Path:   "/images/generations",
		Body:   b,
	}
	if r.Mode == domain.ModeTxt2Img {
		return pl, nil
	}

	pl.Path = "/images/edits"
	b.Images = make([]ref, 0, len(refs))
	for _, u := range refs {
		b.Images = append(b.Images, ref{ImageURL: u})
	}
	// b 是结构体值，写入 Images 后需回填，否则请求体丢失参考图。
	pl.Body = b
	return pl, nil
}

type dataItem struct {
	B64JSON       *string `json:"b64_json"`
	URL           *string `json:"url"`
	RevisedPrompt *string `json:"revised_prompt"`
}

type respBody struct {
	Created      int64       `json:"created"`
	Data         []dataItem  `json:"data"`
	OutputFormat string      `json:"output_format"`
	Size         string      `json:"size"`
	Usage        *usageBlock `json:"usage"`
	Error        *errBlock   `json:"error"`
}

type usageBlock struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	ImagesCount  int `json:"images_count"`
}

type errBlock struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (p *Provider) Extract(raw []byte) (provider.Parsed, error) {
	var out respBody
	if err := json.Unmarshal(raw, &out); err != nil {
		return provider.Parsed{Raw: raw}, fmt.Errorf("响应不是合法 JSON: %w", err)
	}
	if out.Error != nil {
		return provider.Parsed{Raw: raw}, fmt.Errorf("上游返回错误: %s", out.Error.Message)
	}
	out2 := provider.Parsed{Raw: raw}
	for _, d := range out.Data {
		if d.RevisedPrompt != nil && *d.RevisedPrompt != "" {
			out2.RevisedPrompt = *d.RevisedPrompt
		}
		if d.B64JSON != nil && *d.B64JSON != "" {
			out2.B64 = append(out2.B64, *d.B64JSON)
		}
		if d.URL != nil && *d.URL != "" {
			out2.URLs = append(out2.URLs, *d.URL)
		}
	}
	if out.Usage != nil {
		out2.Usage = domain.Usage{
			InputTokens:  out.Usage.InputTokens,
			OutputTokens: out.Usage.OutputTokens,
			ImagesCount:  out.Usage.ImagesCount,
		}
	}
	if len(out.Data) > 0 && out2.Usage.ImagesCount == 0 {
		out2.Usage.ImagesCount = len(out.Data)
	}
	return out2, nil
}

func (p *Provider) Classify(status int, raw []byte) *domain.Error {
	const prov = "sensenova"
	msg := truncate(string(raw), 512)
	typ, code := "", ""
	var probe respBody
	if json.Unmarshal(raw, &probe) == nil && probe.Error != nil {
		typ, code = probe.Error.Type, probe.Error.Code
		if probe.Error.Message != "" {
			msg = truncate(probe.Error.Message, 512)
		}
	}

	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &domain.Error{Code: domain.CodeUpstreamAuth, Message: fallback(msg, "鉴权失败，请检查 API Key"),
			Provider: prov, UpstreamStatus: status, UpstreamType: typ}
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return &domain.Error{Code: domain.CodeUpstreamTimeout, Message: fallback(msg, "上游超时，请降低尺寸或减少参考图"),
			Provider: prov, UpstreamStatus: status, Retryable: true}
	case http.StatusTooManyRequests:
		return &domain.Error{Code: domain.CodeUpstreamQuota, Message: fallback(msg, "触发限流或配额上限，请稍后重试"),
			Provider: prov, UpstreamStatus: status, UpstreamType: typ, Retryable: true}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return &domain.Error{Code: domain.CodeUpstreamRejected, Message: fallback(msg, "参数被上游拒绝"),
			Provider: prov, UpstreamStatus: status, UpstreamType: firstNonEmpty(code, typ)}
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return &domain.Error{Code: domain.CodeUpstreamUnavailable, Message: fallback(msg, "上游暂时不可用"),
			Provider: prov, UpstreamStatus: status, Retryable: true}
	}

	if status >= 200 && status < 300 && probe.Error != nil {
		// 200 但携带错误体的情形。
		return &domain.Error{Code: domain.CodeUpstreamRejected, Message: msg,
			Provider: prov, UpstreamStatus: status, UpstreamType: typ}
	}
	return &domain.Error{Code: domain.CodeUpstreamRejected, Message: fallback(msg, "未知上游错误"),
		Provider: prov, UpstreamStatus: status}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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
