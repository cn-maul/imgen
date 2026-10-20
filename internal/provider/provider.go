// Package provider 定义与厂商无关的图像生成抽象。
package provider

import (
	"context"
	"net/http"

	"github.com/imgen/imgen/internal/domain"
)

// Caps 声明某通道某模型的实际能力，驱动前端参数面板显隐。
type Caps struct {
	Img2Img       bool            `json:"img2img"`
	Compose       bool            `json:"compose"`
	NoWatermark   bool            `json:"noWatermark"`
	PromptExtend  bool            `json:"promptExtend"`
	OutputFormats []domain.Format `json:"outputFormats"`
	MaxRefs       int             `json:"maxRefs"`
	Ratios        []domain.Aspect `json:"ratios"`
	// ExactSize 为真表示上游用像素尺寸而非档位（SenseNova 为 true）。
	ExactSize bool `json:"exactSize"`
	// NativeMultiImage 为真表示原生支持 n>1；两家均为 false。
	NativeMultiImage bool `json:"nativeMultiImage"`
	TimeoutSec       int  `json:"timeoutSec"`
}

// Supports 判断某输出格式是否可用。
func (c Caps) Supports(f domain.Format) bool {
	for _, x := range c.OutputFormats {
		if x == f {
			return true
		}
	}
	return false
}

// Payload 是适配器产出的上游请求描述。
type Payload struct {
	Method string
	Path   string
	Body   any
	// Header 追加的请求头（如个别通道的自定义鉴权）。
	Header map[string]string
	// TimeoutSec 覆盖本次调用超时，0 表示用通道默认值。
	TimeoutSec int
}

// Parsed 是上游响应中「待落盘」的图片载荷。
// 适配器只做提取与归一化，不碰文件系统，便于单测。
type Parsed struct {
	B64           []string
	URLs          []string
	RevisedPrompt string
	Usage         domain.Usage
	// Raw 原样保留，便于排障。
	Raw []byte
}

// Empty 报告是否没有拿到任何图片。
func (p Parsed) Empty() bool { return len(p.B64) == 0 && len(p.URLs) == 0 }

// Provider 是一家上游通道的适配器。
// Translate / Extract / Classify 均为纯函数，可脱离网络单测。
type Provider interface {
	ID() domain.ProviderID
	Name() string

	// Models 返回该通道可用的模型（静态表，不发起网络请求）。
	Models() []domain.Model

	// Capabilities 返回指定模型的能力；未知模型回退到通道级默认值。
	Capabilities(model string) Caps

	// Translate 把领域请求翻译成上游请求体。
	// refs 为已解析为可访问地址的参考图列表。
	Translate(r domain.Request, refs []string) (Payload, error)

	// Extract 从上游响应体提取图片载荷。
	Extract(body []byte) (Parsed, error)

	// Classify 把上游 HTTP 状态码与响应体映射为归一化错误。
	Classify(status int, body []byte) *domain.Error
}

// ImageFetcher 由 blob 层实现，负责把上游返回的图片落地为本地资产。
type ImageFetcher interface {
	// FetchBase64 解码 base64 并落盘。
	FetchBase64(b64 string, hint domain.Format) (domain.Image, error)
	// FetchURL 下载远程图片并落盘。
	FetchURL(ctx context.Context, rawURL string, limit int64) (domain.Image, error)
}

// Doer 抽象 http.Client，便于测试注入。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}
