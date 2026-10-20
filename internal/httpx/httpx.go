// Package httpx 提供带边界重试与指数退避的 HTTP 传输。
//
// 实现参考并改编自 github.com/cn-maul/rosetta 的 internal/httpx（MIT License，
// Copyright (c) 2026 cn-maul），按本项目（仅 POST 的图像生成/edit 端点）裁剪：
// 去掉 SSE 分支，保留 Retry-After 解析、总等待预算、连接池调优与日志脱敏。
package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	dialTimeout         = 30 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
)

// maxTotalWait 限制单次调用用于重试的总睡眠时间。
// 否则 MaxRetries 与长 Retry-After 叠加会让请求挂着数倍于上游超时。
const maxTotalWait = 3 * time.Minute

// maxRetryAfter 限制单个服务端提供的 Retry-After 上限，
// 避免异常大的值（或恶意值）把 goroutine 钉住。
const maxRetryAfter = 60 * time.Second

// ErrPermanent 标记「重来多少次都一样失败」的错误，用于跳过重试预算。
var ErrPermanent = errors.New("httpx: permanent request error")

// ErrBodyTooLarge 表示响应体超过读取上限。
var ErrBodyTooLarge = errors.New("httpx: 响应体超过上限")

// RetryPolicy 决定哪些请求允许重试。
type RetryPolicy int

const (
	// RetryNever 不重试。
	RetryNever RetryPolicy = iota
	// RetryAlways 无条件重试；调用方担保请求可重复执行。
	RetryAlways
)

// Call 描述一次 HTTP 请求。Body 每次重试都会重新调用以便重建载荷。
type Call struct {
	Method      string
	URL         string
	Header      http.Header
	Body        func() ([]byte, error) // nil 表示无请求体
	RetryPolicy RetryPolicy
}

// Client 包装 *http.Client 并提供有界重试。
type Client struct {
	HTTP       *http.Client
	MaxRetries int
	Base       time.Duration
	Cap        time.Duration
	Logger     *slog.Logger
}

// New 返回生产环境默认值的客户端。
func New(logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Client{
		HTTP:       &http.Client{Transport: newTransport(), CheckRedirect: CrossHostSafeRedirect},
		MaxRetries: 3,
		Base:       500 * time.Millisecond,
		Cap:        8 * time.Second,
		Logger:     logger,
	}
}

// newTransport 自建连接池。http.DefaultTransport 的 MaxIdleConnsPerHost=2
// 在并发 >2 时会在每批请求后关闭连接并在下一波重新 TLS 握手，
// 本项目有 worker pool，必须避免这种抖动。
func newTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

// CrossHostSafeRedirect 拒绝跟随跨主机重定向。
// net/http 仅在 host 变化时剥离 Authorization/Cookie/Proxy-*，
// 自定义鉴权头（如 x-api-key）会被原样重发给第三方主机导致泄露。
func CrossHostSafeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return http.ErrUseLastResponse
	}
	if len(via) > 0 && !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
		return fmt.Errorf("%w: 拒绝跨主机重定向 %s -> %s", ErrPermanent,
			via[0].URL.Hostname(), req.URL.Hostname())
	}
	return nil
}

// Do 执行调用并按策略重试。
//
// 关键约定：当重试预算或 ctx 截止耗尽而手上还有响应时，
// **返回 body 仍未关闭的 response**，让调用方能解析真实的上游错误
// （例如 429 的响应体），而不是拿到一个掩盖真相的通用超时错误。
func (c *Client) Do(ctx context.Context, call *Call) (*http.Response, error) {
	var slept time.Duration

	for attempt := 0; ; attempt++ {
		resp, err := c.attempt(ctx, call)
		if err == nil && !RetryableStatus(resp.StatusCode) {
			return resp, nil
		}
		// 永久失败（错误的 method/URL 或无法序列化）每次都一样，不必重试。
		if errors.Is(err, ErrPermanent) ||
			call.RetryPolicy == RetryNever ||
			attempt >= c.MaxRetries ||
			ctx.Err() != nil {
			return resp, err
		}

		wait := c.backoff(attempt)
		if resp != nil {
			// 服务端明确给出等待时间时以它为准，并夹到上限内。
			if ra := retryAfter(resp.Header.Get("Retry-After")); ra > wait {
				wait = ra
			}
			if wait > maxRetryAfter {
				wait = maxRetryAfter
			}
		}
		if slept+wait > maxTotalWait {
			return resp, err
		}
		slept += wait

		c.Logger.Debug("重试请求", "attempt", attempt+1, "wait", wait.String(),
			"status", statusOf(resp), "url", SafeURL(call.URL))

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if resp != nil {
				return resp, err
			}
			return nil, ctx.Err()
		case <-timer.C:
			// 真的要重试了，丢弃本轮 body 以便连接复用。
			drain(resp)
		}
	}
}

func (c *Client) attempt(ctx context.Context, call *Call) (*http.Response, error) {
	var body []byte
	if call.Body != nil {
		var err error
		if body, err = call.Body(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPermanent, err)
		}
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, call.Method, call.URL, rd)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPermanent, err)
	}
	if call.Header != nil {
		req.Header = call.Header.Clone()
	}
	return c.HTTP.Do(req)
}

func (c *Client) backoff(attempt int) time.Duration {
	// 全抖动：把等待打散，避免并发任务在同一时刻重试形成尖峰。
	if attempt <= 0 {
		return c.Base
	}
	d := float64(c.Base) * math.Pow(2, float64(attempt))
	if d > float64(c.Cap) {
		d = float64(c.Cap)
	}
	d *= rand.Float64() + 0.5
	if d < float64(time.Millisecond) {
		d = float64(time.Millisecond) // 绝不允许零延迟重试循环
	}
	return time.Duration(d)
}

func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// RetryableStatus 报告哪些状态码值得重试。
func RetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// retryAfter 解析 Retry-After（延迟秒数或 HTTP-date）。
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// ReadBody 读取响应体并限制大小，超限返回 ErrBodyTooLarge。
func ReadBody(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 64 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, ErrBodyTooLarge
	}
	return raw, nil
}

func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	const drainLimit = 64 << 10
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, drainLimit))
	_ = resp.Body.Close()
}

// secretPattern 覆盖常见厂商密钥前缀，用于在日志中脱敏。
var secretPattern = regexp.MustCompile(
	`sk-[A-Za-z0-9_-]{8,}|AIza[0-9A-Za-z_-]{20,}|AKIA[0-9A-Z]{16}|` +
		`gh[pousr]_[A-Za-z0-9]{16,}|xox[baprs]-[A-Za-z0-9-]{10,}|` +
		`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}`)

// MaskSecrets 替换字符串中的疑似密钥。
func MaskSecrets(s string) string {
	return secretPattern.ReplaceAllString(s, "***")
}

// SafeURL 脱敏后可用于日志的 URL。
func SafeURL(raw string) string {
	return MaskSecrets(raw)
}