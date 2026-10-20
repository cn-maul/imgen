// Package domain 定义跨层共享的实体与领域错误。
package domain

import (
	"errors"
	"fmt"
	"time"
)

// ProviderID 标识一家上游通道。
type ProviderID string

const (
	ProviderSenseNova ProviderID = "sensenova"
	ProviderAgnes     ProviderID = "agnes"
	ProviderOpenAI    ProviderID = "openai" // 二期
)

// Mode 是统一的生成模式，屏蔽各家端点差异。
type Mode string

const (
	ModeTxt2Img Mode = "txt2img"
	ModeImg2Img Mode = "img2img"
	ModeCompose Mode = "compose"
)

// Tier 是尺寸档位；Agnes 原生支持，SenseNova 需查表转成像素。
type Tier string

const (
	Tier1K Tier = "1K"
	Tier2K Tier = "2K"
	Tier3K Tier = "3K"
	Tier4K Tier = "4K"
)

// Aspect 是宽高比，8 档，两家共同支持。
type Aspect string

const (
	Aspect1_1  Aspect = "1:1"
	Aspect3_4  Aspect = "3:4"
	Aspect4_3  Aspect = "4:3"
	Aspect16_9 Aspect = "16:9"
	Aspect9_16 Aspect = "9:16"
	Aspect2_3  Aspect = "2:3"
	Aspect3_2  Aspect = "3:2"
	Aspect21_9 Aspect = "21:9"
)

// AllAspects 是能力声明与前端选择器共用的有序集合。
var AllAspects = []Aspect{
	Aspect1_1, Aspect3_4, Aspect4_3, Aspect16_9,
	Aspect9_16, Aspect2_3, Aspect3_2, Aspect21_9,
}

// AllTiers 是有序档位集合，1K → 4K。
var AllTiers = []Tier{Tier1K, Tier2K, Tier3K, Tier4K}

// Format 是输出图片格式。
type Format string

const (
	FormatPNG  Format = "png"
	FormatJPEG Format = "jpeg"
	FormatWebP Format = "webp"
)

// Quality 是质量档，仅 OpenAI 支持；保留以便二期。
type Quality string

// Ref 是一张参考图的引用。
type Ref struct {
	// AssetID 指向本地资产库。
	AssetID string `json:"assetId,omitempty"`
	// URL 是可直接访问的公网地址或 data URI。
	URL string `json:"url,omitempty"`
}

func (r Ref) Empty() bool { return r.AssetID == "" && r.URL == "" }

// Request 是与厂商无关的生成请求。
type Request struct {
	Provider ProviderID `json:"provider"`
	Model    string     `json:"model"`
	Prompt   string     `json:"prompt"`
	Refs     []Ref      `json:"refs,omitempty"`
	Mode     Mode       `json:"mode"`
	Aspect   Aspect     `json:"aspect"`
	Tier     Tier       `json:"tier"`
	// Count 为期望产出张数；上游只支持单张，由 job 层拆分为多次调用。
	Count int `json:"count"`

	NoWatermark  bool   `json:"noWatermark,omitempty"`
	PromptExtend *bool  `json:"promptExtend,omitempty"`
	OutputFormat Format `json:"outputFormat,omitempty"`
}

// Image 是一张已落盘的图片。
type Image struct {
	AssetID string `json:"assetId"`
	MIME    string `json:"mime"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Bytes   int64  `json:"bytes"`
}

// Usage 汇总一次调用的消耗。
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	ImagesCount  int `json:"imagesCount"`
}

// Result 是一次上游调用的归一化结果。
type Result struct {
	Images        []Image           `json:"images"`
	Usage         Usage             `json:"usage"`
	RevisedPrompt string            `json:"revisedPrompt,omitempty"`
	Raw           []byte            `json:"-"`
	Latency       time.Duration     `json:"-"`
	Extra         map[string]string `json:"-"`
}

// Model 是一个可用的上游模型。
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Job 是生成任务。
type Job struct {
	ID        string     `json:"id"`
	Provider  ProviderID `json:"provider"`
	Model     string     `json:"model"`
	Mode      Mode       `json:"mode"`
	Prompt    string     `json:"prompt"`
	Params    Request    `json:"params"`
	Status    string     `json:"status"`
	Progress  int        `json:"progress"`
	Count     int        `json:"count"`
	LatencyMS int64      `json:"latencyMs"`
	Err       *Error     `json:"error,omitempty"`
	Images    []JobImage `json:"images"`
	CreatedAt time.Time  `json:"createdAt"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Finished  *time.Time `json:"finishedAt,omitempty"`
}

// JobStatus 是任务状态机的取值。
type JobStatus string

const (
	StatusQueued   JobStatus = "queued"
	StatusRunning  JobStatus = "running"
	StatusPartial  JobStatus = "partial"
	StatusDone     JobStatus = "done"
	StatusFailed   JobStatus = "failed"
	StatusCanceled JobStatus = "canceled"
)

// JobImage 记录一次批量任务中的单张产出。
type JobImage struct {
	Seq     int    `json:"seq"`
	AssetID string `json:"assetId,omitempty"`
	Status  string `json:"status"`
	Err     *Error `json:"error,omitempty"`
}

// Asset 是本地资产库条目。
type Asset struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	MIME   string `json:"mime"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int64  `json:"bytes"`
	Source string `json:"source"`
	// SourceURL 仅 remote_url 来源时有值，用于标注过期风险。
	SourceURL string     `json:"sourceUrl,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`

	// Ext 与 Path 是磁盘细节，不外泄给前端。
	Ext  string `json:"-"`
	Path string `json:"-"`
}

// Error 是归一化后的错误体，同时用于 HTTP 响应与任务记录。
type Error struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Provider       string `json:"provider,omitempty"`
	UpstreamStatus int    `json:"upstreamStatus,omitempty"`
	UpstreamType   string `json:"upstreamType,omitempty"`
	// Retryable 指示重试是否有意义，驱动前端按钮文案。
	Retryable bool `json:"retryable"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Provider != "" {
		return fmt.Sprintf("%s[%s]: %s", e.Code, e.Provider, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// 归一化错误码。前端据此决定提示与可执行动作。
const (
	CodeBadRequest          = "BAD_REQUEST"
	CodeNotFound            = "NOT_FOUND"
	CodeInternal            = "INTERNAL"
	CodeUpstreamAuth        = "UPSTREAM_AUTH"
	CodeUpstreamQuota       = "UPSTREAM_QUOTA"
	CodeUpstreamRejected    = "UPSTREAM_REJECTED"
	CodeUpstreamModeration  = "UPSTREAM_MODERATION"
	CodeUpstreamTimeout     = "UPSTREAM_TIMEOUT"
	CodeUpstreamUnavailable = "UPSTREAM_UNAVAILABLE"
	CodeUpstreamUnparseable = "UPSTREAM_UNPARSEABLE"
)

// 领域哨兵错误。
var (
	ErrNotFound     = errors.New("not found")
	ErrBadRequest   = errors.New("bad request")
	ErrNoCredential = errors.New("provider credential not configured")
)

// NewError 构造归一化错误。
func NewError(code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// Wrap 将任意 error 收敛为归一化错误。
func Wrap(code, msg string, err error) *Error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Message: msg + ": " + err.Error()}
}
