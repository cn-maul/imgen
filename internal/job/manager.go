package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/provider"
	"github.com/imgen/imgen/internal/store"
)

// EventKind 是任务进度事件类型。
type EventKind string

const (
	EventQueued  EventKind = "queued"
	EventRunning EventKind = "running"
	EventPartial EventKind = "partial"
	EventDone    EventKind = "done"
	EventFailed  EventKind = "failed"
)

// Event 是推送给前端的进度事件。
type Event struct {
	JobID    string        `json:"jobId"`
	Kind     EventKind     `json:"kind"`
	Status   string        `json:"status"`
	Progress int           `json:"progress"`
	Seq      int           `json:"seq,omitempty"`
	AssetID  string        `json:"assetId,omitempty"`
	Latency  int64         `json:"latencyMs,omitempty"`
	Error    *domain.Error `json:"error,omitempty"`
	At       time.Time     `json:"at"`
}

// Manager 编排任务生命周期。
type Manager struct {
	Exec *Executor
	St   *store.Store
	Q    *Queue
	Log  *slog.Logger

	mu      sync.RWMutex
	subs    map[string]map[chan Event]struct{}
	cancels map[string]context.CancelFunc
}

// NewManager 构造 Manager。
func NewManager(exec *Executor, st *store.Store, q *Queue, log *slog.Logger) *Manager {
	return &Manager{
		Exec: exec, St: st, Q: q, Log: log,
		subs:    map[string]map[chan Event]struct{}{},
		cancels: map[string]context.CancelFunc{},
	}
}

// Submit 创建一个批量任务并入队执行。
func (m *Manager) Submit(ctx context.Context, req domain.Request) (*domain.Job, error) {
	p, ok := m.Exec.Providers[req.Provider]
	if !ok {
		return nil, fmt.Errorf("%w: 未知通道 %s", domain.ErrBadRequest, req.Provider)
	}
	if _, err := m.Exec.Keys.APIKey(ctx, req.Provider); err != nil {
		return nil, toDomainError(err)
	}

	if req.Count < 1 {
		req.Count = 1
	}
	if req.Count > 16 {
		req.Count = 16
	}
	if req.Tier == "" {
		req.Tier = domain.Tier2K
	}
	if req.Aspect == "" {
		req.Aspect = domain.Aspect1_1
	}
	caps := p.Capabilities(req.Model)
	if req.Mode == domain.ModeTxt2Img {
		req.Refs = nil
	} else {
		if !caps.Img2Img {
			return nil, fmt.Errorf("%w: 通道 %s 不支持图生图", domain.ErrBadRequest, req.Provider)
		}
		if len(req.Refs) == 0 {
			return nil, fmt.Errorf("%w: 图生图至少需要一张参考图", domain.ErrBadRequest)
		}
		if len(req.Refs) > caps.MaxRefs {
			return nil, fmt.Errorf("%w: 参考图最多 %d 张，收到 %d", domain.ErrBadRequest, caps.MaxRefs, len(req.Refs))
		}
	}

	j := &domain.Job{
		ID:       newID(),
		Provider: req.Provider,
		Model:    req.Model,
		Mode:     req.Mode,
		Prompt:   req.Prompt,
		Params:   req,
		Count:    req.Count,
	}
	if err := m.St.CreateJob(j); err != nil {
		return nil, err
	}

	baseURL, err := m.providerBaseURL(req.Provider)
	if err != nil {
		return nil, err
	}
	key, err := m.Exec.Keys.APIKey(ctx, req.Provider)
	if err != nil {
		return nil, toDomainError(err)
	}

	// 任务用独立 context，便于取消；脱离请求生命周期。
	bgCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.mu.Lock()
	m.cancels[j.ID] = cancel
	m.mu.Unlock()

	if !m.Q.Submit(bgCtx, j.ID, func(c context.Context) { m.run(c, p, baseURL, key, j) }) {
		cancel()
		m.mu.Lock()
		delete(m.cancels, j.ID)
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: 队列已满，请稍后重试", domain.ErrBadRequest)
	}

	m.emit(*j, EventQueued, 0)
	return j, nil
}

// run 串行执行批量任务的每一次调用。
func (m *Manager) run(ctx context.Context, p provider.Provider, baseURL, key string, j *domain.Job) {
	start := time.Now()
	m.St.UpdateJobStatus(j.ID, domain.StatusRunning, 0, 0, nil)
	m.emit(*j, EventRunning, 0)

	var (
		firstErr  *domain.Error
		successes int
		totalLat  time.Duration
	)

	for seq := 0; seq < j.Count; seq++ {
		if ctx.Err() != nil {
			m.finish(j, domain.StatusCanceled, 0, 0, nil)
			return
		}

		out := m.Exec.RunOnce(ctx, p, baseURL, key, j.Params)
		totalLat += out.Latency
		m.St.RecordUsage(string(j.Provider), j.Model, out.Err == nil, len(out.Assets))

		if out.Err != nil {
			// 单张失败不终止整批：记录原因后继续，最终按产出数决定终态。
			m.St.RecordJobImage(j.ID, seq, "", "failed", out.Err)
			if firstErr == nil {
				firstErr = out.Err
			}
			m.emitWithSeq(*j, EventFailed, percent(seq, j.Count), seq)
			continue
		}

		successes++
		assetID := ""
		if len(out.Assets) > 0 {
			assetID = out.Assets[0].ID
		}
		m.St.RecordJobImage(j.ID, seq, assetID, "done", nil)
		m.emitSeq(*j, EventPartial, seq, percent(seq+1, j.Count), assetID)
	}

	switch {
	case successes == j.Count:
		m.finish(j, domain.StatusDone, 100, totalLat/time.Duration(maxInt(successes, 1)), nil)
	case successes > 0:
		// 部分成功：保留已产出，附带首个失败原因。
		m.finish(j, domain.StatusPartial, percent(successes, j.Count), time.Since(start), firstErr)
	default:
		st := domain.StatusFailed
		if firstErr == nil {
			firstErr = domain.NewError(domain.CodeInternal, "任务未产出任何图片")
		}
		m.finish(j, st, 0, time.Since(start), firstErr)
	}
}

// finish 收尾：写状态、发事件、清取消句柄。
func (m *Manager) finish(j *domain.Job, st domain.JobStatus, progress int, latency time.Duration, e *domain.Error) {
	_ = m.St.UpdateJobStatus(j.ID, st, progress, latency, e)
	m.mu.Lock()
	delete(m.cancels, j.ID)
	m.mu.Unlock()

	kind := EventDone
	switch st {
	case domain.StatusFailed:
		kind = EventFailed
	case domain.StatusCanceled:
		kind = EventFailed
	case domain.StatusPartial:
		kind = EventPartial
	}
	m.emitErr(*j, kind, progress, e)
}

// Cancel 取消一个未完成的任务。
//
// 注意：仅调用 context.CancelFunc 不足以通知订阅者 —— 若任务还在排队
// （尚未被 worker 取走），没有任何协程在跑 run()，也就没人发出终态事件，
// SSE 订阅者会一直挂到客户端超时。故这里在状态写成功后主动广播一次终态。
func (m *Manager) Cancel(id string) error {
	m.mu.RLock()
	cancel, ok := m.cancels[id]
	m.mu.RUnlock()
	if ok {
		cancel()
	}
	if err := m.St.CancelJob(id); err != nil {
		return err
	}
	// 已终态的任务 CancelJob 返回 nil 但不改状态；此时若仍有 cancels 句柄
	// 说明任务其实还没跑完，不能误发终态。用 DB 现状判断是否真的取消成功。
	j, err := m.St.GetJob(id)
	if err != nil {
		return nil
	}
	if j.Status == string(domain.StatusCanceled) {
		m.mu.Lock()
		delete(m.cancels, id)
		m.mu.Unlock()
		m.emit(*j, EventFailed, j.Progress)
	}
	return nil
}

// Subscribe 订阅某任务的进度事件。
func (m *Manager) Subscribe(jobID string) (<-chan Event, func()) {
	ch := make(chan Event, 32)
	m.mu.Lock()
	if m.subs[jobID] == nil {
		m.subs[jobID] = map[chan Event]struct{}{}
	}
	m.subs[jobID][ch] = struct{}{}
	m.mu.Unlock()

	return ch, func() {
		m.mu.Lock()
		if set, ok := m.subs[jobID]; ok {
			if _, exists := set[ch]; exists {
				delete(set, ch)
				close(ch)
			}
			if len(set) == 0 {
				delete(m.subs, jobID)
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) emit(j domain.Job, kind EventKind, progress int) {
	m.broadcast(j, Event{JobID: j.ID, Kind: kind, Status: string(kind), Progress: progress, At: time.Now().UTC()})
}

func (m *Manager) emitSeq(j domain.Job, kind EventKind, seq, progress int, assetID string) {
	m.broadcast(j, Event{JobID: j.ID, Kind: kind, Status: string(kind), Progress: progress,
		Seq: seq, AssetID: assetID, At: time.Now().UTC()})
}

func (m *Manager) emitErr(j domain.Job, kind EventKind, progress int, e *domain.Error) {
	m.broadcast(j, Event{JobID: j.ID, Kind: kind, Status: string(kind), Progress: progress,
		Error: e, At: time.Now().UTC()})
}

func (m *Manager) emitWithSeq(j domain.Job, kind EventKind, progress, seq int) {
	m.broadcast(j, Event{JobID: j.ID, Kind: kind, Status: string(kind), Progress: progress,
		Seq: seq, At: time.Now().UTC()})
}

func (m *Manager) broadcast(j domain.Job, ev Event) {
	m.mu.RLock()
	subs := make([]chan Event, 0, len(m.subs[j.ID]))
	for ch := range m.subs[j.ID] {
		subs = append(subs, ch)
	}
	m.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			// 订阅者消费过慢则丢弃该事件；前端可回落到轮询。
		}
	}
	// 终态后自动解除订阅，避免泄漏。
	if isTerminal(ev.Kind) {
		m.mu.Lock()
		if set, ok := m.subs[j.ID]; ok {
			for ch := range set {
				delete(set, ch)
				close(ch)
			}
			delete(m.subs, j.ID)
		}
		m.mu.Unlock()
	}
}

func isTerminal(k EventKind) bool {
	return k == EventDone || k == EventFailed
}

func (m *Manager) providerBaseURL(id domain.ProviderID) (string, error) {
	providers, err := m.St.ListProviders()
	if err != nil {
		return "", err
	}
	for _, p := range providers {
		if p.ID == id {
			if !p.Enabled {
				return "", fmt.Errorf("%w: 通道 %s 已禁用", domain.ErrBadRequest, id)
			}
			return p.BaseURL, nil
		}
	}
	return "", fmt.Errorf("%w: 通道 %s", domain.ErrNotFound, id)
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败属于极端情况，退化为时间戳。
		return fmt.Sprintf("j_%d", time.Now().UnixNano())
	}
	return "j_" + hex.EncodeToString(b)
}

func percent(done, total int) int {
	if total <= 0 {
		return 0
	}
	p := done * 100 / total
	if p > 100 {
		p = 100
	}
	return p
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
