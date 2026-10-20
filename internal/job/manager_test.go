package job

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imgen/imgen/internal/domain"
)

// ---- Submit 入参校验 ------------------------------------------------------

func TestSubmitUnknownProvider(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, okHandler())
	req := f.agnesReq(1)
	req.Provider = "openai" // 二期才接
	if _, err := f.M.Submit(context.Background(), req); err == nil {
		t.Fatal("未知通道应当报错")
	} else if !errors.Is(err, domain.ErrBadRequest) {
		t.Errorf("错误类型 = %v, want ErrBadRequest", err)
	}
}

func TestSubmitNoCredential(t *testing.T) {
	f := newFixture(t, fakeKeys{err: domain.ErrNoCredential}, okHandler())
	// Submit 把 KeyResolver 的错误收敛成 domain.Error 再返回。
	// 注意 toDomainError 用 domain.NewError 重建，**不保留原 error 链**，
	// 所以 errors.Is(err, domain.ErrNoCredential) 不成立，只能断言码与文案。
	_, err := f.M.Submit(context.Background(), f.agnesReq(1))
	if err == nil {
		t.Fatal("未配置 Key 应当报错")
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("错误类型 = %T, want *domain.Error", err)
	}
	if de.Code != domain.CodeBadRequest {
		t.Errorf("错误码 = %q, want BAD_REQUEST", de.Code)
	}
	if !strings.Contains(de.Message, domain.ErrNoCredential.Error()) {
		t.Errorf("错误信息 = %q, 应透出 %q", de.Message, domain.ErrNoCredential)
	}
}

func TestSubmitImg2ImgNeedsRefs(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, okHandler())

	req := f.agnesReq(1)
	req.Mode = domain.ModeImg2Img
	if _, err := f.M.Submit(context.Background(), req); err == nil {
		t.Error("图生图无参考图应当报错")
	}

	// 超过 MaxRefs（Agnes 是 8）也要被拒。
	req.Refs = make([]domain.Ref, 9)
	for i := range req.Refs {
		req.Refs[i] = domain.Ref{URL: "https://cdn.example/a.png"}
	}
	if _, err := f.M.Submit(context.Background(), req); err == nil {
		t.Error("参考图超限应当报错")
	}
}

func TestSubmitNormalizesRequest(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, okHandler())

	// Count/Tier/Aspect 的缺省值由 Submit 补齐。
	req := f.agnesReq(0)
	req.Tier = ""
	req.Aspect = ""
	j, err := f.M.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if j.Count != 1 {
		t.Errorf("Count = %d, want 1（小于 1 时补 1）", j.Count)
	}
	if j.Params.Tier != domain.Tier2K {
		t.Errorf("Tier = %q, want 2K", j.Params.Tier)
	}
	if j.Params.Aspect != domain.Aspect1_1 {
		t.Errorf("Aspect = %q, want 1:1", j.Params.Aspect)
	}
	if j.Status != "" {
		// CreateJob 只写库，状态由 run 推进；这里只确认 ID 形态。
		_ = j.Status
	}
	if !strings.HasPrefix(j.ID, "j_") {
		t.Errorf("ID 形态异常: %q", j.ID)
	}

	// Count 上限 16。
	req2 := f.agnesReq(100)
	j2, err := f.M.Submit(context.Background(), req2)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if j2.Count != 16 {
		t.Errorf("Count = %d, want 16（上限钳制）", j2.Count)
	}
}

func TestSubmitClearsRefsForTxt2Img(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, okHandler())

	req := f.agnesReq(1)
	req.Mode = domain.ModeTxt2Img
	req.Refs = []domain.Ref{{URL: "https://cdn.example/dropped.png"}}

	j, err := f.M.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(j.Params.Refs) != 0 {
		t.Errorf("文生图的 Refs 应被清空, got %d", len(j.Params.Refs))
	}
}

// ---- 终态 ------------------------------------------------------------------

func TestJobDone(t *testing.T) {
	var hits atomic.Int32
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(okResponse()))
	})

	j, err := f.M.Submit(context.Background(), f.agnesReq(2))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got := f.waitStatus(t, j.ID, 5*time.Second)
	if got.Status != string(domain.StatusDone) {
		t.Fatalf("终态 = %q, want done（err=%+v）", got.Status, got.Err)
	}
	if got.Progress != 100 {
		t.Errorf("progress = %d, want 100", got.Progress)
	}
	if hits.Load() != 2 {
		t.Errorf("上游被请求 %d 次, want 2（count=2）", hits.Load())
	}
	if len(got.Images) != 2 {
		t.Fatalf("产出记录 = %d, want 2", len(got.Images))
	}
	for _, img := range got.Images {
		if img.Status != "done" || img.AssetID == "" {
			t.Errorf("产出 = %+v, want done 且带 assetId", img)
		}
	}
	if got.Finished == nil || got.StartedAt == nil {
		t.Error("done 任务应写 started_at / finished_at")
	}
	if got.LatencyMS < 0 {
		t.Errorf("latency_ms = %d", got.LatencyMS)
	}
}

// TestJobPartial 部分成功 → 终态 partial，保留已产出并附带首个失败原因。
func TestJobPartial(t *testing.T) {
	var hits atomic.Int32
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		// seq 0 成功，seq 1 起返回用户错误（不重试，避免拖慢测试）。
		if hits.Add(1) == 1 {
			_, _ = w.Write([]byte(okResponse()))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"提示词违规","type":"image_generation_user_error"}}`))
	})

	j, err := f.M.Submit(context.Background(), f.agnesReq(2))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got := f.waitStatus(t, j.ID, 5*time.Second)
	if got.Status != string(domain.StatusPartial) {
		t.Fatalf("终态 = %q, want partial", got.Status)
	}
	if got.Err == nil {
		t.Fatal("partial 应附带首个失败原因")
	}
	if got.Err.Code != domain.CodeUpstreamRejected {
		t.Errorf("附带错误码 = %q", got.Err.Code)
	}
	if got.Progress != 50 {
		t.Errorf("progress = %d, want 50（1/2 成功）", got.Progress)
	}

	// 已产出的那张要保留下来。
	var doneCount, failCount int
	for _, img := range got.Images {
		switch img.Status {
		case "done":
			doneCount++
			if img.AssetID == "" {
				t.Error("成功的产出缺少 assetId")
			}
		case "failed":
			failCount++
			if img.Err == nil {
				t.Error("失败的产出缺少错误")
			}
		}
	}
	if doneCount != 1 || failCount != 1 {
		t.Errorf("产出分布 = done %d / failed %d, want 1/1", doneCount, failCount)
	}
	// 资产真的落库了。
	for _, img := range got.Images {
		if img.Status == "done" {
			if _, err := f.St.GetAsset(img.AssetID); err != nil {
				t.Errorf("partial 的产出未入库: %v", err)
			}
		}
	}
	if got.Finished == nil {
		t.Error("partial 是终态，应写 finished_at")
	}
}

// TestJobFailedAll 全部失败 → failed。
func TestJobFailedAll(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"提示词违规","type":"image_generation_user_error"}}`))
	})

	j, err := f.M.Submit(context.Background(), f.agnesReq(2))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got := f.waitStatus(t, j.ID, 5*time.Second)
	if got.Status != string(domain.StatusFailed) {
		t.Fatalf("终态 = %q, want failed", got.Status)
	}
	if got.Err == nil || got.Err.Code != domain.CodeUpstreamRejected {
		t.Fatalf("failed 应带错误, got %+v", got.Err)
	}
	if len(got.Images) != 2 {
		t.Fatalf("产出记录 = %d, want 2", len(got.Images))
	}
	for _, img := range got.Images {
		if img.Status != "failed" {
			t.Errorf("产出 %d 状态 = %q, want failed", img.Seq, img.Status)
		}
	}
	if got.Finished == nil {
		t.Error("failed 应写 finished_at")
	}
}

// TestJobCanceled 取消任务 → 终态 canceled。
func TestJobCanceled(t *testing.T) {
	// 上游先挂住，留出取消的时间窗。release 用 sync.Once 关闭，
	// 避免「取消后放行」与 Cleanup 重复关闭同一个 channel 引发 panic。
	release := make(chan struct{})
	var once sync.Once
	openRelease := func() { once.Do(func() { close(release) }) }

	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = w.Write([]byte(okResponse()))
	})
	t.Cleanup(openRelease)

	j, err := f.M.Submit(context.Background(), f.agnesReq(4))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// 等任务真正跑起来再取消，否则可能还没进 run 就被 CancelJob 置为 canceled。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := f.St.GetJob(j.ID)
		if err == nil && cur.Status == string(domain.StatusRunning) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := f.M.Cancel(j.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	// 放行上游，让循环走到下一次 ctx.Err() 检查。
	openRelease()

	got := f.waitStatus(t, j.ID, 5*time.Second)
	if got.Status != string(domain.StatusCanceled) {
		t.Fatalf("终态 = %q, want canceled", got.Status)
	}
	if got.Finished == nil {
		t.Error("canceled 应写 finished_at")
	}
}

// TestCancelUnknownJob 取消不存在的任务 → ErrNotFound。
func TestCancelUnknownJob(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, okHandler())
	if err := f.M.Cancel("j_ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Cancel 不存在 = %v, want ErrNotFound", err)
	}
}

// ---- Subscribe ------------------------------------------------------------

// TestSubscribeReceivesTerminalEvent 订阅应收到终态事件，且频道随后被关闭。
func TestSubscribeReceivesTerminalEvent(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, okHandler())

	j, err := f.M.Submit(context.Background(), f.agnesReq(1))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// ⚠️ 必须先订阅再等终态：broadcast 在终态后会立刻关闭并移除订阅，
	// 若任务已经跑完再订阅，只会拿到一个已关闭的空频道（拿不到任何事件）。
	ch, unsubscribe := f.M.Subscribe(j.ID)
	defer unsubscribe()

	var sawDone bool
	// 读已关闭频道会立刻返回 ok==false，所以循环自然终止，不会死等。
	for ev := range ch {
		if ev.JobID != j.ID {
			t.Errorf("事件 jobId = %q, want %q", ev.JobID, j.ID)
		}
		if ev.Kind == EventDone {
			sawDone = true
			if ev.Progress != 100 {
				t.Errorf("done 事件 progress = %d, want 100", ev.Progress)
			}
		}
	}
	if !sawDone {
		t.Fatal("未收到 done 终态事件（频道应在终态后被关闭）")
	}
}

// TestSubscribeAfterTerminalGetsClosedChannel 锁定「终态后再订阅」的行为：
// 频道仍在，但下一个终态事件到达时会被关闭，读不出事件。
// 前端 SSE 若要拿到完整事件流，必须在 Submit 返回后立刻订阅。
func TestSubscribeAfterTerminalGetsClosedChannel(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, okHandler())

	j, err := f.M.Submit(context.Background(), f.agnesReq(1))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	f.waitStatus(t, j.ID, 5*time.Second)

	ch, unsubscribe := f.M.Subscribe(j.ID)
	defer unsubscribe()

	// 任务已结束，不会再有事件：频道可能仍是打开的，但不会收到东西。
	select {
	case ev, ok := <-ch:
		if ok {
			t.Errorf("终态之后仍收到事件 %+v", ev)
		}
	case <-time.After(300 * time.Millisecond):
		// 频道保持打开、无事件 —— 这是当前行为，不报错。
	}
}

// TestSubscribeUnsubscribeClosesChannel 主动退订也应当关闭频道。
func TestSubscribeUnsubscribeClosesChannel(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, okHandler())
	ch, unsubscribe := f.M.Subscribe("j_manual")
	unsubscribe()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("退订后仍能读到事件")
		}
	case <-time.After(time.Second):
		t.Fatal("退订后频道未被关闭（读阻塞了）")
	}
}

// TestSubscribeDoesNotBlockOnSlowConsumer 订阅者不消费时不该阻塞任务执行。
func TestSubscribeDoesNotBlockOnSlowConsumer(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, okHandler())

	j, err := f.M.Submit(context.Background(), f.agnesReq(1))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	_, unsubscribe := f.M.Subscribe(j.ID)
	defer unsubscribe()

	// 一个事件都不读，任务照样要跑完。
	got := f.waitStatus(t, j.ID, 5*time.Second)
	if got.Status != string(domain.StatusDone) {
		t.Fatalf("终态 = %q, want done（慢订阅者不应拖住任务）", got.Status)
	}
}

// ---- Queue -----------------------------------------------------------------

func TestQueueSubmitAndRun(t *testing.T) {
	q := NewQueue(2)
	defer q.Close()

	done := make(chan string, 3)
	for i := 0; i < 3; i++ {
		name := string(rune('a' + i))
		if !q.Submit(context.Background(), name, func(context.Context) { done <- name }) {
			t.Fatalf("任务 %s 入队失败", name)
		}
	}
	got := map[string]bool{}
	for i := 0; i < 3; i++ {
		select {
		case n := <-done:
			got[n] = true
		case <-time.After(3 * time.Second):
			t.Fatal("任务未执行完")
		}
	}
	if len(got) != 3 {
		t.Errorf("执行了 %d 个任务, want 3", len(got))
	}
	if q.Workers() != 2 {
		t.Errorf("Workers = %d, want 2", q.Workers())
	}
}

func TestQueueSubmitAfterCloseReturnsFalse(t *testing.T) {
	q := NewQueue(1)
	q.Close()
	if q.Submit(context.Background(), "x", func(context.Context) {}) {
		t.Error("Close 之后入队应当返回 false")
	}
	// 重复 Close 不应 panic 或死锁。
	q.Close()
}

func TestQueuePanicDoesNotKillWorker(t *testing.T) {
	q := NewQueue(1)
	defer q.Close()

	boom := make(chan struct{})
	ok := make(chan struct{})
	if !q.Submit(context.Background(), "boom", func(context.Context) {
		defer close(boom)
		panic("故意的")
	}) {
		t.Fatal("入队失败")
	}
	// 同一个 worker 应当能继续处理下一个任务。
	if !q.Submit(context.Background(), "ok", func(context.Context) { close(ok) }) {
		t.Fatal("入队失败")
	}
	<-boom
	select {
	case <-ok:
	case <-time.After(3 * time.Second):
		t.Fatal("panic 之后 worker 不再消费任务")
	}
}

func TestQueueSetWorkers(t *testing.T) {
	q := NewQueue(1)
	defer q.Close()
	if q.Workers() != 1 {
		t.Fatalf("Workers = %d, want 1", q.Workers())
	}
	q.SetWorkers(4)
	if q.Workers() != 4 {
		t.Errorf("扩容后 Workers = %d, want 4", q.Workers())
	}
	q.SetWorkers(0) // 非法值应被钳到 1
	if q.Workers() != 1 {
		t.Errorf("非法值后 Workers = %d, want 1", q.Workers())
	}
	q.SetWorkers(999) // 上限 64
	if q.Workers() != 64 {
		t.Errorf("超上限后 Workers = %d, want 64", q.Workers())
	}
}

// ---- 纯函数 ---------------------------------------------------------------

func TestPercent(t *testing.T) {
	cases := []struct{ done, total, want int }{
		{0, 4, 0},
		{1, 4, 25},
		{2, 4, 50},
		{4, 4, 100},
		{5, 4, 100}, // 溢出应被夹到 100
		{1, 0, 0},   // total 为 0 不应 panic
		{1, -1, 0},
	}
	for _, c := range cases {
		if got := percent(c.done, c.total); got != c.want {
			t.Errorf("percent(%d,%d) = %d, want %d", c.done, c.total, got, c.want)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	if !isTerminal(EventDone) || !isTerminal(EventFailed) {
		t.Error("done/failed 应当是终态事件")
	}
	if isTerminal(EventPartial) || isTerminal(EventRunning) || isTerminal(EventQueued) {
		t.Error("partial/running/queued 不是终态事件")
	}
}

// TestNewIDUnique 任务 ID 不重复。
func TestNewIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := newID()
		if !strings.HasPrefix(id, "j_") {
			t.Fatalf("ID 形态异常: %q", id)
		}
		if seen[id] {
			t.Fatalf("ID 重复: %q", id)
		}
		seen[id] = true
	}
}

// okHandler 永远返回一张图。
func okHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(okResponse()))
	}
}
