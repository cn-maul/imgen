package job

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imgen/imgen/internal/blob"
	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/httpx"
	"github.com/imgen/imgen/internal/provider"
	"github.com/imgen/imgen/internal/provider/agnes"
	"github.com/imgen/imgen/internal/provider/sensenova"
	"github.com/imgen/imgen/internal/store"
)

// ---- 测试替身 ------------------------------------------------------------

// fakeKeys 是 KeyResolver 的测试实现。
type fakeKeys struct {
	key string
	err error
}

func (f fakeKeys) APIKey(context.Context, domain.ProviderID) (string, error) {
	return f.key, f.err
}

// discardLog 静默日志，避免测试输出噪声。
func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- 夹具 ----------------------------------------------------------------

type fixture struct {
	St    *store.Store
	Blobs *blob.Store
	Exec  *Executor
	M     *Manager
	Q     *Queue
	Srv   *httptest.Server
	Calls atomic.Int32 // 上游被请求的次数
}

// newFixture 搭一套可直接跑任务的测试环境。handler 是上游打桩。
func newFixture(t *testing.T, keys KeyResolver, handler http.HandlerFunc) *fixture {
	t.Helper()
	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "imgen.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	blobs := blob.NewStore(filepath.Join(dir, "blobs"), filepath.Join(dir, "thumbs"), 8)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	f := &fixture{St: st, Blobs: blobs, Srv: srv}

	// httpx 的退避是真的 sleep：把 Base/Cap 压到毫秒级，
	// 让 429 重试只花几十毫秒而不是几秒。
	hc := httpx.New(discardLog())
	hc.MaxRetries = 3
	hc.Base = 5 * time.Millisecond
	hc.Cap = 20 * time.Millisecond

	provs := map[domain.ProviderID]provider.Provider{
		domain.ProviderSenseNova: sensenova.New(srv.URL),
		domain.ProviderAgnes:     agnes.New(srv.URL),
	}
	// 步数计数放在 wrapper 里，不影响适配器自身。
	for id, p := range provs {
		provs[id] = countingProvider{Provider: p, n: &f.Calls}
	}

	// 内置通道的 base_url 指向打桩服务器，否则 Manager 会用官方地址发真实请求。
	if err := st.UpdateProvider(domain.ProviderSenseNova, store.ProviderConfig{BaseURL: srv.URL}); err != nil {
		t.Fatalf("改写 sensenova base_url: %v", err)
	}
	if err := st.UpdateProvider(domain.ProviderAgnes, store.ProviderConfig{BaseURL: srv.URL}); err != nil {
		t.Fatalf("改写 agnes base_url: %v", err)
	}

	f.Exec = &Executor{
		Providers: provs,
		Keys:      keys,
		Blobs:     blobs,
		St:        st,
		Log:       discardLog(),
		HTTP:      hc,
	}
	f.Q = NewQueue(2)
	f.M = NewManager(f.Exec, st, f.Q, discardLog())
	t.Cleanup(func() { f.Q.Close() })
	return f
}

// countingProvider 统计 Translate 被调用的次数（等于上游调用次数）。
type countingProvider struct {
	provider.Provider
	n *atomic.Int32
}

func (c countingProvider) Translate(r domain.Request, refs []string) (provider.Payload, error) {
	c.n.Add(1)
	return c.Provider.Translate(r, refs)
}

func (f *fixture) agnesReq(count int) domain.Request {
	return domain.Request{
		Provider: domain.ProviderAgnes,
		Model:    "agnes-image-2.5-flash",
		Prompt:   "一只海豹",
		Mode:     domain.ModeTxt2Img,
		Tier:     domain.Tier2K,
		Aspect:   domain.Aspect16_9,
		Count:    count,
	}
}

// waitStatus 轮询直到任务进入终态，返回最终任务。避免固定 sleep。
func (f *fixture) waitStatus(t *testing.T, id string, d time.Duration) *domain.Job {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		j, err := f.St.GetJob(id)
		if err == nil && isDone(j.Status) {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, err := f.St.GetJob(id)
	if err != nil {
		t.Fatalf("等待终态失败: %v", err)
	}
	return j
}

func isDone(s string) bool {
	switch domain.JobStatus(s) {
	case domain.StatusDone, domain.StatusFailed, domain.StatusCanceled, domain.StatusPartial:
		return true
	}
	return false
}

// ---- 响应构造 ------------------------------------------------------------

var testPNG = mustDecodePNG()

func mustDecodePNG() []byte {
	// 1x1 红色 PNG，够上游「返回一张图」用，且解码开销极小。
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
		0x9f, 0x00, 0x05, 0xfe, 0x02, 0xfe, 0xa7, 0x35,
		0x8d, 0x4e, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
		0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
}

// okResponse 造一个「成功返回一张 b64 图」的响应体。
func okResponse() string {
	raw, err := json.Marshal(map[string]any{
		"created": time.Now().Unix(),
		"data": []map[string]any{
			{"b64_json": base64.StdEncoding.EncodeToString(testPNG)},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// emptyResponse 上游返回 200 但没有任何图片。
func emptyResponse() string { return `{"created":1,"data":[]}` }

// ---- RunOnce 层用例 -------------------------------------------------------

// TestRunOnceSuccess 成功路径：图片被落盘并记入资产库。
func TestRunOnceSuccess(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okResponse())
	})

	out := f.Exec.RunOnce(context.Background(),
		agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))

	if out.Err != nil {
		t.Fatalf("RunOnce 失败: %+v", out.Err)
	}
	if len(out.Assets) != 1 {
		t.Fatalf("产出 = %d 张, want 1", len(out.Assets))
	}
	a := out.Assets[0]
	if a.Source != "generated" {
		t.Errorf("Source = %q, want generated", a.Source)
	}
	if a.MIME != "image/png" || a.Bytes != int64(len(testPNG)) {
		t.Errorf("资产 = %+v", a)
	}
	// 落盘并写了资产记录。
	if _, err := os.Stat(f.Blobs.Path(a.Path)); err != nil {
		t.Errorf("图片未落盘: %v", err)
	}
	if _, err := f.St.GetAsset(a.ID); err != nil {
		t.Errorf("资产未入库: %v", err)
	}
	if out.Latency <= 0 {
		t.Error("Latency 应大于 0")
	}
}

// TestRunOnce429ThenSuccess 429 退避后最终成功。
//
// 注意：httpx 的退避是真实 sleep，这里把 Base/Cap 压到毫秒级（见 newFixture），
// 因此整个用例只有几十毫秒。

// TestRunOnceLargeChunkedBody 保证大响应体能被完整读出。
//
// 回归用例：曾因 httpx.Do 返回后在 call 闭包里 defer cancel()，
// 使 resp.Body 立刻绑定到已取消的 context，小响应因已进缓冲区而侥幸通过，
// 但真实的分块大响应（图像 base64 常有 1MB+）读时会报 context canceled。
func TestRunOnceLargeChunkedBody(t *testing.T) {
	// 用足量的图片数据把响应体撑到几百 KB，模拟真实 base64 响应的体量。
	big := make([]byte, 0, 256<<10)
	for len(big) < 256<<10 {
		big = append(big, testPNG...)
	}
	b64 := base64.StdEncoding.EncodeToString(big)
	// 切成 8KB 的块，保证按 4 字符对齐（8192 是 4 的倍数）。
	const chunkSize = 8 << 10

	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 不写 Content-Length，走分块传输；每块之间让出，保证 body 不会被整体缓存。
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"`)
		flusher.Flush()
		for i := 0; i < len(b64); i += chunkSize {
			end := i + chunkSize
			if end > len(b64) {
				end = len(b64)
			}
			_, _ = io.WriteString(w, b64[i:end])
			flusher.Flush()
		}
		_, _ = io.WriteString(w, `"}]}`)
		flusher.Flush()
	})

	out := f.Exec.RunOnce(context.Background(),
		agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))

	if out.Err != nil {
		t.Fatalf("大响应体读取失败: %+v", out.Err)
	}
	if len(out.Assets) != 1 {
		t.Fatalf("产出 = %d 张, want 1", len(out.Assets))
	}
}

func TestRunOnce429ThenSuccess(t *testing.T) {
	var hits atomic.Int32
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			// 第一次返回 429，不带 Retry-After，走 httpx 自己的短退避。
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"RPM 超限","type":"rate_limit"}}`)
			return
		}
		_, _ = io.WriteString(w, okResponse())
	})

	out := f.Exec.RunOnce(context.Background(),
		agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))

	if out.Err != nil {
		t.Fatalf("429 之后应当成功，实际: %+v", out.Err)
	}
	if len(out.Assets) != 1 {
		t.Fatalf("产出 = %d 张, want 1", len(out.Assets))
	}
	if got := hits.Load(); got < 2 {
		t.Errorf("上游只被请求 %d 次，429 没有触发重试", got)
	}
}

// TestRunOnceUserErrorNoRetry Agnes 的 400/422 用户错误：不重试、直接失败。
func TestRunOnceUserErrorNoRetry(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "Agnes 400 image_generation_user_error",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"提示词包含违规内容","type":"image_generation_user_error","code":"content_violation"}}`,
		},
		{
			name:   "Agnes 422",
			status: http.StatusUnprocessableEntity,
			body:   `{"error":{"message":"参数不合法","type":"invalid_request_error"}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var hits atomic.Int32
			f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			})

			out := f.Exec.RunOnce(context.Background(),
				agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))

			if out.Err == nil {
				t.Fatal("用户错误应当直接失败")
			}
			if out.Err.Code != domain.CodeUpstreamRejected {
				t.Errorf("错误码 = %q, want UPSTREAM_REJECTED", out.Err.Code)
			}
			if out.Err.Retryable {
				t.Error("用户错误不应标记 Retryable（前端应提示改提示词而非重试）")
			}
			if got := hits.Load(); got != 1 {
				t.Errorf("上游被请求 %d 次, want 1（用户错误不该重试）", got)
			}
			if len(out.Assets) != 0 {
				t.Errorf("失败不应产出资产, got %d", len(out.Assets))
			}
		})
	}
}

// TestRunOnce5xxExhaustsRetries 5xx 会重试，耗尽预算后失败且标记可重试。
func TestRunOnce5xxExhaustsRetries(t *testing.T) {
	var hits atomic.Int32
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"message":"上游挂了"}}`)
	})

	out := f.Exec.RunOnce(context.Background(),
		agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))

	if out.Err == nil {
		t.Fatal("5xx 应当失败")
	}
	if out.Err.Code != domain.CodeUpstreamUnavailable {
		t.Errorf("错误码 = %q, want UPSTREAM_UNAVAILABLE", out.Err.Code)
	}
	if !out.Err.Retryable {
		t.Error("5xx 应标记 Retryable")
	}
	// MaxRetries=3 → 首次 + 3 次重试。
	if got := hits.Load(); got != 4 {
		t.Errorf("上游被请求 %d 次, want 4（1 次首发 + 3 次重试）", got)
	}
}

// TestRunOnceEmptyResult 200 但没图 → UPSTREAM_REJECTED。
func TestRunOnceEmptyResult(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, emptyResponse())
	})
	out := f.Exec.RunOnce(context.Background(),
		agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))
	if out.Err == nil || out.Err.Code != domain.CodeUpstreamRejected {
		t.Fatalf("空结果 = %+v, want UPSTREAM_REJECTED", out.Err)
	}
	if !strings.Contains(out.Err.Message, "没有任何图片") {
		t.Errorf("错误信息 = %q", out.Err.Message)
	}
}

// TestRunOnceUnparseable 200 但不是 JSON。
func TestRunOnceUnparseable(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>502</html>")
	})
	out := f.Exec.RunOnce(context.Background(),
		agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))
	if out.Err == nil || out.Err.Code != domain.CodeUpstreamUnparseable {
		t.Fatalf("非法响应 = %+v, want UPSTREAM_UNPARSEABLE", out.Err)
	}
}

// TestRunOnceBadBase64 200 但 b64 解不出来。
func TestRunOnceBadBase64(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"!!!!not-base64!!!!"}]}`)
	})
	out := f.Exec.RunOnce(context.Background(),
		agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))
	if out.Err == nil {
		t.Fatal("坏 base64 应当失败")
	}
	if !strings.Contains(out.Err.Message, "base64") {
		t.Errorf("错误信息 = %q", out.Err.Message)
	}
}

// TestRunOnceCanceledContext 已取消的 ctx 直接失败，不发请求。
func TestRunOnceCanceledContext(t *testing.T) {
	var hits atomic.Int32
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, okResponse())
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := f.Exec.RunOnce(ctx, agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))
	if out.Err == nil {
		t.Fatal("已取消的 ctx 应当失败")
	}
	if out.Err.Code != domain.CodeInternal {
		t.Errorf("错误码 = %q, want INTERNAL（任务已取消）", out.Err.Code)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("取消后仍发了 %d 次请求", got)
	}
}

// TestRunOnceTimeout ctx 超时 → UPSTREAM_TIMEOUT 且可重试。
func TestRunOnceTimeout(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		// 上游故意慢，但 httpx 的 MaxRetries 会让它重试；用短 ctx 提前收场。
		time.Sleep(400 * time.Millisecond)
		_, _ = io.WriteString(w, okResponse())
	})
	// Agnes 的 TimeoutSec 是 180s，这里用 ctx 超时把它压下来。
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	out := f.Exec.RunOnce(ctx, agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", f.agnesReq(1))
	if out.Err == nil {
		t.Fatal("超时应当失败")
	}
	if out.Err.Code != domain.CodeUpstreamTimeout {
		t.Errorf("错误码 = %q, want UPSTREAM_TIMEOUT", out.Err.Code)
	}
	if !out.Err.Retryable {
		t.Error("超时应可重试")
	}
}

// TestRunOnceResolveRefs 参考图解析：URL 直通 / assetId 转 data URI / 缺失报错。
func TestRunOnceResolveRefs(t *testing.T) {
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, okResponse())
	})

	// 先放一张图进资产库，拿到 assetId。
	a, err := f.Blobs.Put(testPNG, "upload", "", "")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, _, err := f.St.UpsertAsset(a); err != nil {
		t.Fatalf("UpsertAsset: %v", err)
	}

	// URL 引用直通。
	if got, derr := f.Exec.resolveRefs([]domain.Ref{{URL: "https://cdn.example/a.png"}}); derr != nil ||
		len(got) != 1 || got[0] != "https://cdn.example/a.png" {
		t.Errorf("URL 引用 = %v, %v", got, derr)
	}

	// assetId 引用应展开成 data URI。
	got, derr := f.Exec.resolveRefs([]domain.Ref{{AssetID: a.ID}})
	if derr != nil {
		t.Fatalf("assetId 引用: %+v", derr)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "data:image/png;base64,") {
		t.Fatalf("assetId 未展开为 data URI: %q", got)
	}

	// 两种都缺 → BAD_REQUEST。
	if _, derr := f.Exec.resolveRefs([]domain.Ref{{}}); derr == nil || derr.Code != domain.CodeBadRequest {
		t.Errorf("空引用 = %+v, want BAD_REQUEST", derr)
	}

	// 不存在的 assetId → BAD_REQUEST。
	if _, derr := f.Exec.resolveRefs([]domain.Ref{{AssetID: "a_ghost"}}); derr == nil ||
		derr.Code != domain.CodeBadRequest {
		t.Errorf("缺失 assetId = %+v, want BAD_REQUEST", derr)
	}
}

// TestRunOnceImg2ImgSendsRefs 图生图：参考图必须真的出现在请求体里。
func TestRunOnceImg2ImgSendsRefs(t *testing.T) {
	var gotBody atomic.Value
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody.Store(string(raw))
		_, _ = io.WriteString(w, okResponse())
	})

	a, err := f.Blobs.Put(testPNG, "upload", "", "")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, _, err := f.St.UpsertAsset(a); err != nil {
		t.Fatalf("UpsertAsset: %v", err)
	}

	req := f.agnesReq(1)
	req.Mode = domain.ModeImg2Img
	req.Refs = []domain.Ref{{AssetID: a.ID}}

	out := f.Exec.RunOnce(context.Background(), agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", req)
	if out.Err != nil {
		t.Fatalf("图生图失败: %+v", out.Err)
	}
	body, _ := gotBody.Load().(string)
	if !strings.Contains(body, "extra_body") || !strings.Contains(body, `"image"`) {
		t.Errorf("请求体缺少参考图: %s", body)
	}
	if !strings.Contains(body, "data:image/png;base64,") {
		t.Errorf("参考图未展开为 data URI: %s", body)
	}
}

// TestRunOnceRefsClearedForTxt2Img 文生图的 Refs 会被清空（Manager.Submit 负责）。
func TestRunOnceRefsClearedForTxt2Img(t *testing.T) {
	var gotBody atomic.Value
	f := newFixture(t, fakeKeys{key: "sk-test"}, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody.Store(string(raw))
		_, _ = io.WriteString(w, okResponse())
	})

	req := f.agnesReq(1)
	req.Mode = domain.ModeTxt2Img
	req.Refs = []domain.Ref{{URL: "https://cdn.example/should-be-dropped.png"}}
	req.Refs = nil // Manager.Submit 对文生图的行为

	if out := f.Exec.RunOnce(context.Background(), agnes.New(f.Srv.URL), f.Srv.URL, "sk-test", req); out.Err != nil {
		t.Fatalf("RunOnce: %+v", out.Err)
	}
	body, _ := gotBody.Load().(string)
	if strings.Contains(body, "should-be-dropped") {
		t.Errorf("文生图不该带参考图: %s", body)
	}
}
