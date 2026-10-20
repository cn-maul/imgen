package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imgen/imgen/internal/config"
	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/store"
)

// PLAN 的验收标准里有一条「8 张批量 / 100 张批量不崩」。
// 这条必须走真实装配路径（app → job → executor → provider → blob → store），
// 而不是只测 Manager 的循环：只有端到端才能发现
// 「批量时参考图被复用改写」「第 N 张的落盘串号」这类跨层错误。
//
// 上游只支持 n=1，批量由 job 层拆成 N 次串行调用，所以这里断言的是
// 「上游被打了 N 次」而不是「一次请求返回 N 张」。

// countingUpstream 返回合法 PNG 并统计被调用次数。
func countingUpstream(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		// 每次返回内容不同的图，避免被内容寻址去重成同一个 asset，
		// 否则无法验证「N 张各自独立落盘」。
		img := append([]byte{}, tinyPNG...)
		img = append(img, byte(n))

		var reqBody map[string]any
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		if got, _ := reqBody["n"].(float64); got != 1 {
			t.Errorf("上游 n = %v, want 1（上游不支持批量，必须逐张请求）", reqBody["n"])
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": time.Now().Unix(),
			"data": []map[string]any{
				{"b64_json": base64.StdEncoding.EncodeToString(img)},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// newTestApp 装配一个指向假上游的 App。
func newTestApp(t *testing.T, upstreamURL string) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir: dir, DBPath: dir + "/imgen.db",
		BlobDir: dir + "/blobs", ThumbDir: dir + "/thumbs",
		UpstreamTimeout: 30 * time.Second, Concurrency: 2,
		MaxUploadMB: 20, MaxRemoteMB: 32, KeepDays: 30,
	}
	log := newTestLogger()
	a, err := New(cfg, log)
	if err != nil {
		t.Fatalf("装配失败: %v", err)
	}
	t.Cleanup(a.Close)

	enc, err := a.Cipher.Encrypt([]byte("sk-mock"))
	if err != nil {
		t.Fatalf("加密 Key: %v", err)
	}
	if err := a.St.UpdateProvider(domain.ProviderSenseNova, store.ProviderConfig{
		BaseURL: upstreamURL, APIKey: enc,
	}); err != nil {
		t.Fatalf("配置通道: %v", err)
	}
	return a
}

// waitTerminal 轮询直到任务进入终态。
func waitTerminal(t *testing.T, a *App, jobID string) *domain.Job {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, err := a.St.GetJob(jobID)
		if err != nil {
			t.Fatalf("读任务: %v", err)
		}
		switch domain.JobStatus(j.Status) {
		case domain.StatusDone, domain.StatusPartial, domain.StatusFailed, domain.StatusCanceled:
			return j
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("任务 %s 等终态超时", jobID)
	return nil
}

// TestBatchGenerateEight 覆盖 PLAN 验收的「8 张批量」。
func TestBatchGenerateEight(t *testing.T) {
	up, hits := countingUpstream(t)
	a := newTestApp(t, up.URL+"/v1")

	j, err := a.Jobs.Submit(context.Background(), domain.Request{
		Provider: domain.ProviderSenseNova,
		Model:    "sensenova-u1.5-fast",
		Prompt:   "八张不同角度的雪山",
		Mode:     domain.ModeTxt2Img,
		Aspect:   domain.Aspect1_1,
		Tier:     domain.Tier1K,
		Count:    8,
	})
	if err != nil {
		t.Fatalf("提交: %v", err)
	}

	got := waitTerminal(t, a, j.ID)
	if got.Status != string(domain.StatusDone) {
		t.Fatalf("终态 = %s, want done（err=%v）", got.Status, got.Err)
	}
	if n := atomic.LoadInt32(hits); n != 8 {
		t.Errorf("上游被调用 %d 次, want 8", n)
	}
	if len(got.Images) != 8 {
		t.Fatalf("产出 %d 张, want 8", len(got.Images))
	}

	// 每张都必须有独立的 seq、都真的落盘可读。
	seenSeq := map[int]bool{}
	seenAsset := map[string]bool{}
	for _, img := range got.Images {
		if seenSeq[img.Seq] {
			t.Errorf("seq %d 重复", img.Seq)
		}
		seenSeq[img.Seq] = true
		if img.Status != "done" {
			t.Errorf("seq=%d status = %s, want done", img.Seq, img.Status)
		}
		if img.AssetID == "" {
			t.Fatalf("seq=%d 没有 assetId", img.Seq)
		}
		if seenAsset[img.AssetID] {
			t.Errorf("seq=%d 复用了同一个 asset %s（应各自独立）", img.Seq, img.AssetID)
		}
		seenAsset[img.AssetID] = true
		if _, err := a.St.GetAsset(img.AssetID); err != nil {
			t.Errorf("seq=%d 的资产读不到: %v", img.Seq, err)
		}
	}
	if got.Progress != 100 {
		t.Errorf("progress = %d, want 100", got.Progress)
	}
}

// TestBatchGenerateMaxCount 覆盖后端上限：请求 20 张应被钳到 16。
func TestBatchGenerateMaxCount(t *testing.T) {
	up, hits := countingUpstream(t)
	a := newTestApp(t, up.URL+"/v1")

	j, err := a.Jobs.Submit(context.Background(), domain.Request{
		Provider: domain.ProviderSenseNova,
		Model:    "sensenova-u1.5-fast",
		Prompt:   "超量请求",
		Mode:     domain.ModeTxt2Img,
		Count:    20, // 超过上限
	})
	if err != nil {
		t.Fatalf("提交: %v", err)
	}
	if j.Count != 16 {
		t.Errorf("Count = %d, want 16（上限钳制）", j.Count)
	}

	got := waitTerminal(t, a, j.ID)
	if got.Status != string(domain.StatusDone) {
		t.Fatalf("终态 = %s, want done（err=%v）", got.Status, got.Err)
	}
	if n := atomic.LoadInt32(hits); n != 16 {
		t.Errorf("上游被调用 %d 次, want 16", n)
	}
	if len(got.Images) != 16 {
		t.Errorf("产出 %d 张, want 16", len(got.Images))
	}
}

// TestBatchPartialFailureKeepsSuccessful 单张失败不该丢掉已成功的那几张。
// 这是批量的真实风险：上游排队满了会有零星 503。
func TestBatchPartialFailureKeepsSuccessful(t *testing.T) {
	var calls int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		// 第 3 次调用失败，其余成功。
		if n == 3 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"message":"content rejected","type":"invalid_request_error"}}`))
			return
		}
		img := append([]byte{}, tinyPNG...)
		img = append(img, byte(n))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": time.Now().Unix(),
			"data":    []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(img)}},
		})
	}))
	t.Cleanup(up.Close)

	a := newTestApp(t, up.URL+"/v1")
	j, err := a.Jobs.Submit(context.Background(), domain.Request{
		Provider: domain.ProviderSenseNova,
		Model:    "sensenova-u1.5-fast",
		Prompt:   "四张里失败一张",
		Mode:     domain.ModeTxt2Img,
		Count:    4,
	})
	if err != nil {
		t.Fatalf("提交: %v", err)
	}

	got := waitTerminal(t, a, j.ID)
	if got.Status != string(domain.StatusPartial) {
		t.Fatalf("终态 = %s, want partial", got.Status)
	}
	if len(got.Images) != 4 {
		t.Fatalf("Images 记录 %d 条, want 4（成功+失败都要记）", len(got.Images))
	}
	var done, failed int
	for _, img := range got.Images {
		switch img.Status {
		case "done":
			done++
			if img.AssetID == "" {
				t.Error("成功的那张没有 assetId")
			}
		case "failed":
			failed++
			if img.Err == nil {
				t.Error("失败的那张没有错误原因")
			}
		}
	}
	if done != 3 || failed != 1 {
		t.Errorf("done=%d failed=%d, want 3/1", done, failed)
	}
	// 首个失败原因要挂在任务上，前端才能提示。
	if got.Err == nil {
		t.Error("partial 任务没有携带错误原因")
	}
	// 失败那张不该阻止落盘：资产库里应有三张。
	list, total, err := a.St.ListAssets(store.AssetFilter{Limit: 50})
	if err != nil {
		t.Fatalf("列资产: %v", err)
	}
	if total != 3 {
		t.Errorf("资产库 %d 条, want 3（got %d 条记录）", total, len(list))
	}
}

// TestBatchSerializeUpstreamCalls 批量必须是串行调用，不能并发打上游。
// 两家上游都有 RPM 限制（Agnes 4K 只有 1 RPM），并发会直接失败。
func TestBatchSerializeUpstreamCalls(t *testing.T) {
	var inFlight, maxInFlight int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&maxInFlight)
			if cur <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		defer atomic.AddInt32(&inFlight, -1)

		n := atomic.LoadInt32(&maxInFlight)
		img := append([]byte{}, tinyPNG...)
		img = append(img, byte(n))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": time.Now().Unix(),
			"data":    []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(img)}},
		})
	}))
	t.Cleanup(up.Close)

	a := newTestApp(t, up.URL+"/v1")
	j, err := a.Jobs.Submit(context.Background(), domain.Request{
		Provider: domain.ProviderSenseNova,
		Model:    "sensenova-u1.5-fast",
		Prompt:   "串行校验",
		Mode:     domain.ModeTxt2Img,
		Count:    5,
	})
	if err != nil {
		t.Fatalf("提交: %v", err)
	}

	got := waitTerminal(t, a, j.ID)
	if got.Status != string(domain.StatusDone) {
		t.Fatalf("终态 = %s, want done", got.Status)
	}
	if n := atomic.LoadInt32(&maxInFlight); n != 1 {
		t.Errorf("同一批任务的最大并发请求数 = %d, want 1（批量必须串行）", n)
	}
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
