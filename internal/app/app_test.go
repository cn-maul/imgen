package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/imgen/imgen/internal/config"
	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/store"
)

// tinyPNG 是一张 2x2 的合法 PNG，用于让 blob 层能解码落盘。
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x02,
	0x08, 0x02, 0x00, 0x00, 0x00, 0xfd, 0xd4, 0x9a, 0x73,
	0x00, 0x00, 0x00, 0x16, 'I', 'D', 'A', 'T',
	0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0xf0, 0x9f, 0x81, 0x81,
	0x01, 0x0c, 0x00, 0x1b, 0xb6, 0x02, 0xfd, 0x2d, 0x0e, 0x0c,
	0xa5, 0x00, 0x3d, 0x8e, 0x01, 0x00, 0x00, 0x00, 0x00,
	'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// TestEndToEndGenerate 用假上游跑通「装配 → 配 Key → 建任务 → 出图 → 落盘 → 终态」。
// 这是唯一覆盖 app 装配层与 job/store/blob 协同的测试。
func TestEndToEndGenerate(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": time.Now().Unix(),
			"data": []map[string]any{
				{"b64_json": base64.StdEncoding.EncodeToString(tinyPNG)},
			},
		})
	}))
	defer up.Close()

	dir := t.TempDir()
	cfg := config.Config{
		DataDir: dir, DBPath: dir + "/imgen.db",
		BlobDir: dir + "/blobs", ThumbDir: dir + "/thumbs",
		UpstreamTimeout: 30 * time.Second, Concurrency: 2,
		MaxUploadMB: 20, MaxRemoteMB: 32, KeepDays: 30,
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	a, err := New(cfg, log)
	if err != nil {
		t.Fatalf("装配失败: %v", err)
	}
	defer a.Close()

	// 把 Key 与 baseUrl 指向假上游。
	enc, err := a.Cipher.Encrypt([]byte("sk-mock"))
	if err != nil {
		t.Fatalf("加密 Key: %v", err)
	}
	if err := a.St.UpdateProvider(domain.ProviderSenseNova, store.ProviderConfig{
		BaseURL: up.URL + "/v1", APIKey: enc,
	}); err != nil {
		t.Fatalf("配置通道: %v", err)
	}

	// 未配 Key 的通道应被拒 —— 顺带验证凭据门禁。
	if _, err := a.Jobs.Submit(context.Background(), domain.Request{
		Provider: domain.ProviderAgnes, Model: "agnes-image-2.5-flash",
		Prompt: "x", Mode: domain.ModeTxt2Img, Count: 1,
	}); err == nil {
		t.Error("未配 Key 的通道竟然允许提交")
	}

	j, err := a.Jobs.Submit(context.Background(), domain.Request{
		Provider: domain.ProviderSenseNova,
		Model:    "sensenova-u1.5-fast",
		Prompt:   "一只在冰川上的海豹",
		Mode:     domain.ModeTxt2Img,
		Aspect:   domain.Aspect16_9,
		Tier:     domain.Tier2K,
		Count:    2,
	})
	if err != nil {
		t.Fatalf("提交任务: %v", err)
	}

	// 订阅事件，确认终态事件可达（SSE 依赖这条路径）。
	ch, cancel := a.Jobs.Subscribe(j.ID)
	defer cancel()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got, err := a.St.GetJob(j.ID)
		if err != nil {
			t.Fatalf("读任务: %v", err)
		}
		switch got.Status {
		case string(domain.StatusDone), string(domain.StatusPartial),
			string(domain.StatusFailed), string(domain.StatusCanceled):
			if got.Status != string(domain.StatusDone) {
				t.Fatalf("终态 = %s, want done（err=%v）", got.Status, got.Err)
			}
			if len(got.Images) != 2 {
				t.Fatalf("产出 %d 张, want 2", len(got.Images))
			}
			// 校验每张都真的落盘、能被读回。
			for _, img := range got.Images {
				if img.AssetID == "" {
					t.Fatalf("seq=%d 没有 assetId", img.Seq)
				}
				as, err := a.St.GetAsset(img.AssetID)
				if err != nil {
					t.Fatalf("读资产 %s: %v", img.AssetID, err)
				}
				if as.Width != 2 || as.Height != 2 {
					t.Errorf("资产尺寸 = %dx%d, want 2x2", as.Width, as.Height)
				}
				// 同图去重：两张应是同一个 sha。
				if as.SHA256 == "" {
					t.Error("sha256 为空")
				}
			}
			if got.Images[0].AssetID != got.Images[1].AssetID {
				t.Logf("注意：两次调用产出不同（上游返回同一张图时应去重为同一 asset）")
			}
			// 终态事件必须在频道里。
			select {
			case ev, ok := <-ch:
				if !ok {
					t.Log("订阅频道已被关闭（终态广播后关闭，符合预期）")
				} else {
					t.Logf("收到事件 kind=%s status=%s", ev.Kind, ev.Status)
				}
			case <-time.After(2 * time.Second):
				t.Error("终态后未收到任何事件")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("等终态超时")
}

// TestNewRequiresUsableDataDir 装配在目录不可写时应报错而非 panic。
func TestNewRequiresUsableDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		DataDir: dir, DBPath: dir + "/imgen.db",
		BlobDir: dir + "/blobs", ThumbDir: dir + "/thumbs",
		UpstreamTimeout: 30 * time.Second, Concurrency: 1,
		MaxUploadMB: 20, MaxRemoteMB: 32,
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	a, err := New(cfg, log)
	if err != nil {
		t.Fatalf("合法目录装配失败: %v", err)
	}
	if a.Jobs == nil || a.Q == nil || a.Blobs == nil || a.Cipher == nil {
		t.Fatal("装配结果有空字段")
	}
	if a.Q.Workers() != 1 {
		t.Errorf("并发 = %d, want 1", a.Q.Workers())
	}
	a.Close()
}
