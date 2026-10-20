package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/job"
	"github.com/imgen/imgen/internal/store"
)

// ============================================================
// POST /api/v1/jobs
// ============================================================

func (a *API) createJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// 领域层（Manager.Submit / 适配器 Translate）也会校验，
	// 但这里先做一轮是为了给出精确、可读的 400 提示，
	// 而不是把上游的兜底错误原样抛给用户。
	if de := validateCreateJob(req); de != nil {
		writeDomainErr(w, de)
		return
	}

	dr := domain.Request{
		Provider:     domain.ProviderID(req.Provider),
		Model:        req.Model,
		Prompt:       strings.TrimSpace(req.Prompt),
		Refs:         req.Refs,
		Mode:         req.Mode,
		Aspect:       req.Aspect,
		Tier:         req.Tier,
		Count:        req.Count,
		PromptExtend: req.PromptExtend,
		OutputFormat: req.OutputFormat,
	}
	if req.NoWatermark != nil {
		dr.NoWatermark = *req.NoWatermark
	}
	if dr.Mode == "" {
		dr.Mode = domain.ModeTxt2Img
	}

	j, err := a.deps.Jobs.Submit(r.Context(), dr)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, createJobDTO{JobID: j.ID, Status: string(domain.StatusQueued)})
}

// validateCreateJob 做入参校验。所有失败都是 BAD_REQUEST 且 retryable=false
// —— 改参数才有意义，重试没有意义。
func validateCreateJob(req createJobRequest) *domain.Error {
	if strings.TrimSpace(req.Provider) == "" {
		return errBadRequest("provider 不能为空")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return errBadRequest("prompt 不能为空")
	}
	if strings.TrimSpace(req.Model) == "" {
		return errBadRequest("model 不能为空")
	}
	switch req.Mode {
	case "", domain.ModeTxt2Img, domain.ModeImg2Img, domain.ModeCompose:
	default:
		return errBadRequest(fmt.Sprintf("mode %q 非法，应为 txt2img / img2img / compose", req.Mode))
	}
	// 档位与宽高比留空由领域层补默认值，但填了就必须合法。
	if req.Tier != "" && !validTier(req.Tier) {
		return errBadRequest(fmt.Sprintf("tier %q 非法，应为 1K / 2K / 3K / 4K", req.Tier))
	}
	if req.Aspect != "" && !validAspect(req.Aspect) {
		return errBadRequest(fmt.Sprintf("aspect %q 不在支持的 8 档宽高比内", req.Aspect))
	}
	if req.Count < 0 {
		return errBadRequest("count 不能为负")
	}
	if req.Count > 16 {
		return errBadRequest("count 最多 16")
	}
	for i, ref := range req.Refs {
		if ref.Empty() {
			return errBadRequest(fmt.Sprintf("refs[%d] 既无 assetId 也无 url", i))
		}
	}
	if req.OutputFormat != "" {
		switch req.OutputFormat {
		case domain.FormatPNG, domain.FormatJPEG, domain.FormatWebP:
		default:
			return errBadRequest(fmt.Sprintf("outputFormat %q 非法，应为 png / jpeg / webp", req.OutputFormat))
		}
	}
	return nil
}

func validTier(t domain.Tier) bool {
	for _, x := range domain.AllTiers {
		if x == t {
			return true
		}
	}
	return false
}

func validAspect(x domain.Aspect) bool {
	for _, v := range domain.AllAspects {
		if v == x {
			return true
		}
	}
	return false
}

// ============================================================
// GET /api/v1/jobs/:id
// ============================================================

func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	j, err := a.deps.St.GetJob(id)
	if err != nil {
		// 未知 job → 404，由 errors.Is(err, domain.ErrNotFound) 判别。
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDTO(*j))
}

// ============================================================
// GET /api/v1/jobs
// ============================================================

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.JobFilter{
		Provider: domain.ProviderID(strings.TrimSpace(q.Get("provider"))),
		Status:   domain.JobStatus(strings.TrimSpace(q.Get("status"))),
		Query:    strings.TrimSpace(q.Get("q")),
		Limit:    queryInt(q.Get("limit"), 50),
		Offset:   queryInt(q.Get("offset"), 0),
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	items, total, err := a.deps.St.ListJobs(f)
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []domain.Job{}
	}
	writeJSON(w, http.StatusOK, paged{Items: items, Total: total, Limit: normLimit(f.Limit), Offset: f.Offset})
}

// ============================================================
// POST /api/v1/jobs/:id/cancel
// ============================================================

func (a *API) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeDomainErr(w, errBadRequest("缺少任务 id"))
		return
	}

	// 未知 id 由 store.CancelJob 返回 ErrNotFound → 404。
	if err := a.deps.Jobs.Cancel(id); err != nil {
		writeErr(w, err)
		return
	}

	// 回显最新状态：cancel 对已完成任务是 no-op，前端需要知道真实结果。
	j, err := a.deps.St.GetJob(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDTO(*j))
}

// ============================================================
// GET /api/v1/jobs/:id/events —— SSE
// ============================================================

// sseKeepalive 是心跳间隔：既防止反向代理掐断空闲连接，
// 也让客户端能发现「连接还活着但事件流停了」。
const sseKeepalive = 15 * time.Second

func (a *API) jobEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeDomainErr(w, errBadRequest("缺少任务 id"))
		return
	}

	// 先确认任务存在，避免客户端订阅一个永不产生事件的 id。
	j, err := a.deps.St.GetJob(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	// 已终态的任务没有事件可推：直接回一条当前状态后结束，
	// 前端不会因此卡在等待中。
	if isTerminalStatus(j.Status) {
		a.writeTerminalSSE(w, r, j)
		return
	}

	ch, cancel := a.deps.Jobs.Subscribe(id)
	// 必须调用 cancel：否则订阅者泄漏，且 broadcast 终态时 close 的频道
	// 会一直留在 Manager.subs 里。
	defer cancel()

	a.streamSSE(w, r, ch)
}

// writeTerminalSSE 对已完成任务推送一条终态事件后关闭。
func (a *API) writeTerminalSSE(w http.ResponseWriter, r *http.Request, j *domain.Job) {
	flusher, ok := setupSSE(w)
	if !ok {
		writeDomainErr(w, errInternal("当前连接不支持流式响应", nil))
		return
	}

	ev := job.Event{
		JobID:    j.ID,
		Kind:     terminalKind(j.Status),
		Status:   j.Status,
		Progress: j.Progress,
		Latency:  j.LatencyMS,
		Error:    j.Err,
		At:       time.Now().UTC(),
	}
	// 已完成任务不写 Last-Event-ID 语义：客户端拿到终态即可停止重连。
	writeSSEEvent(w, flusher, ev)
}

// streamSSE 持续推送事件直到终态或客户端断开。
func (a *API) streamSSE(w http.ResponseWriter, r *http.Request, ch <-chan job.Event) {
	flusher, ok := setupSSE(w)
	if !ok {
		writeDomainErr(w, errInternal("当前连接不支持流式响应", nil))
		return
	}

	ctx := r.Context()
	ticker := time.NewTicker(sseKeepalive)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// 客户端断开：直接返回，defer 里的 cancel 负责清理订阅。
			return

		case ev, open := <-ch:
			if !open {
				// broadcast 在终态会 close 频道。读已关闭频道会立刻返回零值，
				// 不检测 ok 就会刷屏空事件 —— 这里必须退出。
				return
			}
			writeSSEEvent(w, flusher, ev)
			if isTerminalKind(ev.Kind) {
				return
			}

		case <-ticker.C:
			// 注释行同时充当心跳，避免代理按空闲超时掐断。
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// setupSSE 写好 SSE 响应头并返回 flusher。
func setupSSE(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// 关掉 nginx 的响应缓冲，否则事件会被攒着一起发。
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return flusher, true
}

// writeSSEEvent 写出一个 data 帧。
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, ev job.Event) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	flusher.Flush()
}

func isTerminalStatus(s string) bool {
	switch domain.JobStatus(s) {
	case domain.StatusDone, domain.StatusFailed, domain.StatusCanceled:
		return true
	}
	return false
}

func isTerminalKind(k job.EventKind) bool {
	return k == job.EventDone || k == job.EventFailed
}

func terminalKind(s string) job.EventKind {
	switch domain.JobStatus(s) {
	case domain.StatusDone:
		return job.EventDone
	case domain.StatusPartial:
		return job.EventPartial
	default:
		return job.EventFailed
	}
}

// ============================================================
// 查询参数工具
// ============================================================

// queryInt 解析整数查询参数，非法或缺失时用 def。
func queryInt(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// normLimit 回显实际生效的分页大小。必须与 store.ListJobs 的钳制规则
// 完全一致，否则前端拿到的 limit 与真实翻页步长不符：
// store 是 `if limit <= 0 || limit > 100 { limit = 50 }`。
func normLimit(v int) int {
	if v > 0 && v <= 100 {
		return v
	}
	return 50
}
