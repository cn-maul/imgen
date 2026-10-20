// Package job 负责任务调度：把批量任务拆成多次上游调用并执行。
//
// 设计取向（见 PLAN §6）：只有全局并发上限与 429 退避重试，
// 不做熔断、不做令牌桶、不做多实例路由。
package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/imgen/imgen/internal/blob"
	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/httpx"
	"github.com/imgen/imgen/internal/provider"
	"github.com/imgen/imgen/internal/store"
)

const maxResponseBytes = 64 << 20

// KeyResolver 解密上游凭据，由 store + secret 组合实现。
type KeyResolver interface {
	APIKey(ctx context.Context, id domain.ProviderID) (string, error)
}

// Executor 执行单次上游调用：翻译 → 发送 → 解析 → 落盘。
type Executor struct {
	Providers map[domain.ProviderID]provider.Provider
	Keys      KeyResolver
	Blobs     *blob.Store
	St        *store.Store
	Log       *slog.Logger
	HTTP      *httpx.Client
}

// Outcome 是单次调用的结果。
type Outcome struct {
	Assets  []domain.Asset
	Latency time.Duration
	Err     *domain.Error
}

// RunOnce 执行一次上游调用并把图片落盘。
// baseURL 来自 DB 配置而非适配器，以便用户在 UI 改地址。
func (e *Executor) RunOnce(ctx context.Context, p provider.Provider, baseURL, key string, req domain.Request) Outcome {
	start := time.Now()

	refs, derr := e.resolveRefs(req.Refs)
	if derr != nil {
		return Outcome{Err: derr, Latency: time.Since(start)}
	}

	payload, err := p.Translate(req, refs)
	if err != nil {
		return Outcome{Err: toDomainError(err), Latency: time.Since(start)}
	}
	body, err := json.Marshal(payload.Body)
	if err != nil {
		return Outcome{Err: domain.NewError(domain.CodeInternal, "序列化请求失败"), Latency: time.Since(start)}
	}

	timeout := time.Duration(p.Capabilities(req.Model).TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	if payload.TimeoutSec > 0 {
		timeout = time.Duration(payload.TimeoutSec) * time.Second
	}

	endpoint := strings.TrimRight(baseURL, "/") + payload.Path

	call := func(ctx context.Context, timeout time.Duration) (*http.Response, context.CancelFunc, *domain.Error) {
		// endpoint 与 body 在每次重试时重建，避免复用已读的请求体。
		hdr := http.Header{}
		hdr.Set("Authorization", "Bearer "+key)
		hdr.Set("Content-Type", "application/json")
		hdr.Set("Accept", "application/json")
		for k, v := range payload.Header {
			hdr.Set(k, v)
		}
		snapshot := body

		cctx, cancel := context.WithTimeout(ctx, timeout)

		resp, err := e.HTTP.Do(cctx, &httpx.Call{
			Method:      payload.Method,
			URL:         endpoint,
			Header:      hdr,
			Body:        func() ([]byte, error) { return snapshot, nil },
			RetryPolicy: httpx.RetryAlways, // 图像生成无副作用，可安全重试
		})
		if err != nil {
			cancel()
			return resp, nil, classifyTransport(err, p, timeout)
		}
		// 不能在这里 cancel：httpx.Do 返回后 resp.Body 仍绑定 cctx，
		// 提前取消会让后续 ReadBody 立刻报 context canceled。
		// 由调用方在读完 body 后统一 cancel。
		return resp, cancel, nil
	}

	resp, cancel, derr := call(ctx, timeout)
	if derr != nil {
		return Outcome{Err: derr, Latency: time.Since(start)}
	}
	if cancel != nil {
		defer cancel()
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	// 非 2xx：读取 body 交给适配器分类（body 此时仍可读）。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, err := httpx.ReadBody(resp.Body, maxResponseBytes)
		if err != nil && len(body) == 0 {
			return Outcome{Err: &domain.Error{Code: domain.CodeUpstreamRejected,
				Message: "读取上游错误响应失败", Provider: string(p.ID())}, Latency: time.Since(start)}
		}
		return Outcome{Err: p.Classify(resp.StatusCode, body), Latency: time.Since(start)}
	}

	raw, err := httpx.ReadBody(resp.Body, maxResponseBytes)
	if err != nil {
		e.Log.Error("读取上游响应失败", "provider", p.ID(), "status", resp.StatusCode, "err", err)
		return Outcome{Err: &domain.Error{Code: domain.CodeUpstreamUnavailable,
			Message: "读取上游响应失败", Provider: string(p.ID()), Retryable: true}, Latency: time.Since(start)}
	}

	assets, perr := e.persist(ctx, p, raw)
	if perr != nil {
		return Outcome{Err: perr, Latency: time.Since(start)}
	}
	return Outcome{Assets: assets, Latency: time.Since(start)}
}

// classifyTransport 把传输层失败映射为归一化错误。
// httpx 已按 Retry-After 与总预算退避过，这里只负责归类。
func classifyTransport(err error, p provider.Provider, timeout time.Duration) *domain.Error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &domain.Error{Code: domain.CodeUpstreamTimeout,
			Message: fmt.Sprintf("上游超过 %s 未响应", timeout), Provider: string(p.ID()), Retryable: true}
	case errors.Is(err, context.Canceled):
		return domain.NewError(domain.CodeInternal, "任务已取消")
	default:
		return &domain.Error{Code: domain.CodeUpstreamUnavailable,
			Message: "网络错误: " + truncate(err.Error(), 200), Provider: string(p.ID()), Retryable: true}
	}
}

// persist 解析响应并把图片落盘。
func (e *Executor) persist(ctx context.Context, p provider.Provider, raw []byte) ([]domain.Asset, *domain.Error) {
	parsed, err := p.Extract(raw)
	if err != nil {
		return nil, &domain.Error{Code: domain.CodeUpstreamUnparseable,
			Message: "解析上游响应失败: " + err.Error(), Provider: string(p.ID())}
	}
	if parsed.Empty() {
		return nil, &domain.Error{Code: domain.CodeUpstreamRejected,
			Message: "上游返回成功但没有任何图片", Provider: string(p.ID())}
	}

	out := make([]domain.Asset, 0, len(parsed.B64)+len(parsed.URLs))
	appendAsset := func(a domain.Asset) *domain.Error {
		rec, _, err := e.St.UpsertAsset(a)
		if err != nil {
			return domain.Wrap(domain.CodeInternal, "写入资产失败", err)
		}
		out = append(out, *rec)
		return nil
	}

	for _, b64 := range parsed.B64 {
		decoded, err := provider.DecodeBase64(b64)
		if err != nil {
			return nil, &domain.Error{Code: domain.CodeUpstreamRejected,
				Message: "图片 base64 解码失败: " + err.Error(), Provider: string(p.ID())}
		}
		a, err := e.Blobs.Put(decoded, "generated", "", "")
		if err != nil {
			return nil, domain.Wrap(domain.CodeInternal, "图片落盘失败", err)
		}
		if derr := appendAsset(a); derr != nil {
			return nil, derr
		}
	}

	// 上游返回 url 时立即取回：这些链接有有效期（SenseNova 为 24 小时）。
	for _, u := range parsed.URLs {
		a, err := e.Blobs.FetchURL(ctx, u)
		if err != nil {
			// 落盘失败不丢弃整批：记录警告后继续处理其余图片。
			e.Log.Warn("远程图片落盘失败", "provider", p.ID(), "url", truncate(u, 120), "err", err)
			continue
		}
		if derr := appendAsset(a); derr != nil {
			return nil, derr
		}
	}

	if len(out) == 0 {
		return nil, &domain.Error{Code: domain.CodeUpstreamRejected,
			Message: "上游图片全部下载失败", Provider: string(p.ID())}
	}
	return out, nil
}

// resolveRefs 把本地资产引用解析为上游可访问的 data URI。
func (e *Executor) resolveRefs(refs []domain.Ref) ([]string, *domain.Error) {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		switch {
		case r.URL != "":
			out = append(out, r.URL)
		case r.AssetID != "":
			rel, err := e.St.AssetPath(r.AssetID)
			if err != nil {
				return nil, domain.NewError(domain.CodeBadRequest, "参考图不存在: "+r.AssetID)
			}
			uri, err := e.Blobs.DataURI(rel)
			if err != nil {
				return nil, domain.NewError(domain.CodeBadRequest, "读取参考图失败: "+err.Error())
			}
			out = append(out, uri)
		default:
			return nil, domain.NewError(domain.CodeBadRequest, "参考图既无 assetId 也无 url")
		}
	}
	return out, nil
}

func toDomainError(err error) *domain.Error {
	var de *domain.Error
	if errors.As(err, &de) {
		return de
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.NewError(domain.CodeNotFound, err.Error())
	case errors.Is(err, domain.ErrBadRequest), errors.Is(err, domain.ErrNoCredential):
		return domain.NewError(domain.CodeBadRequest, err.Error())
	}
	return domain.NewError(domain.CodeInternal, err.Error())
}

// Queue 是一个简单的 worker pool。
type Queue struct {
	jobs    chan *task
	wg      sync.WaitGroup
	workers int
	closed  bool
	mu      sync.Mutex
}

type task struct {
	ctx  context.Context
	run  func(context.Context)
	name string
}

// NewQueue 创建队列并启动 workers 个消费协程。
func NewQueue(workers int) *Queue {
	if workers < 1 {
		workers = 1
	}
	q := &Queue{jobs: make(chan *task, workers*8), workers: workers}
	for i := 0; i < workers; i++ {
		q.wg.Add(1)
		go q.loop()
	}
	return q
}

func (q *Queue) loop() {
	defer q.wg.Done()
	for t := range q.jobs {
		// nil 是 SetWorkers 缩容时投下的毒丸。
		if t == nil {
			return
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("任务执行 panic", "name", t.name, "panic", r)
				}
			}()
			t.run(t.ctx)
		}()
	}
}

// SetWorkers 调整并发上限：多于当前则拉起新 worker，少于当前则投毒丸退出多余的。
// 不关闭 jobs 通道，否则后续扩容将无法再投递任务。
func (q *Queue) SetWorkers(n int) {
	if n < 1 {
		n = 1
	}
	if n > 64 {
		n = 64
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	for q.workers < n {
		q.workers++
		q.wg.Add(1)
		go q.loop()
	}
	for q.workers > n {
		q.workers--
		select {
		case q.jobs <- nil:
		default:
			// 通道已满，毒丸投递失败：恢复计数，下次调用再试。
			q.workers++
			return
		}
	}
}

// Submit 入队一个任务；队列已满时返回 false。
func (q *Queue) Submit(ctx context.Context, name string, run func(context.Context)) bool {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	select {
	case q.jobs <- &task{ctx: ctx, run: run, name: name}:
		q.mu.Unlock()
		return true
	default:
		q.mu.Unlock()
		return false
	}
}

// Len 返回排队中的任务数。
func (q *Queue) Len() int { return len(q.jobs) }

// Workers 返回并发上限。SetWorkers 会改该值，故需加锁读取。
func (q *Queue) Workers() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.workers
}

// Close 停止接收新任务并等待存量完成。
func (q *Queue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	q.mu.Unlock()

	close(q.jobs)
	q.wg.Wait()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
