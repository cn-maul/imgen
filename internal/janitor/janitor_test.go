package janitor

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imgen/imgen/internal/blob"
	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/store"
)

// fixture 组装一个「真 store + 真 blob」的环境，便于验证磁盘文件也被删掉。
type fixture struct {
	st    *store.Store
	blobs *blob.Store
	dir   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "imgen.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	blobs := blob.NewStore(filepath.Join(dir, "blobs"), filepath.Join(dir, "thumbs"), 32)
	return &fixture{st: st, blobs: blobs, dir: dir}
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// seedJob 建一条终态任务并把时间回拨。
func (f *fixture) seedJob(t *testing.T, id string, status domain.JobStatus, age time.Duration) {
	t.Helper()
	j := &domain.Job{
		ID: id, Provider: domain.ProviderAgnes, Model: "agnes-image-2.5-flash",
		Mode: domain.ModeTxt2Img, Prompt: "x", Count: 1,
	}
	if err := f.st.CreateJob(j); err != nil {
		t.Fatalf("建任务: %v", err)
	}
	if err := f.st.UpdateJobStatus(id, status, 100, time.Second, nil); err != nil {
		t.Fatalf("置状态: %v", err)
	}
	when := time.Now().UTC().Add(-age)
	if _, err := f.st.DB().Exec(
		`UPDATE jobs SET created_at = ?, finished_at = ? WHERE id = ?`,
		when.Format(time.RFC3339Nano), when.Format(time.RFC3339Nano), id); err != nil {
		t.Fatalf("回拨时间: %v", err)
	}
}

// seedAsset 写一个真实 PNG 到磁盘并登记资产，返回 asset 与磁盘绝对路径。
func (f *fixture) seedAsset(t *testing.T, raw []byte, age time.Duration) (domain.Asset, string) {
	t.Helper()
	a, err := f.blobs.Put(raw, "generated", "", "")
	if err != nil {
		t.Fatalf("落盘: %v", err)
	}
	rec, _, err := f.st.UpsertAsset(a)
	if err != nil {
		t.Fatalf("登记资产: %v", err)
	}
	when := time.Now().UTC().Add(-age)
	if _, err := f.st.DB().Exec(`UPDATE assets SET created_at = ? WHERE id = ?`,
		when.Format(time.RFC3339Nano), rec.ID); err != nil {
		t.Fatalf("回拨资产时间: %v", err)
	}
	return *rec, f.blobs.Path(rec.Path)
}

// tinyPNG 是 2x2 合法 PNG，让 blob 层能解码。
// 内容不同的图片 sha 不同，才不会被内容寻址去重成同一条资产。
func tinyPNG(tag byte) []byte {
	p := []byte{
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
	// 改 IDAT 尾部之后不会影响解码的字节会让 sha 不同。
	p = append(p, tag)
	return p
}

// TestSweepDeletesJobsAndFiles 端到端：到期任务与孤儿资产被删，磁盘文件也没了。
func TestSweepDeletesJobsAndFiles(t *testing.T) {
	f := newFixture(t)

	f.seedJob(t, "j_old", domain.StatusDone, 40*24*time.Hour)
	asset, absPath := f.seedAsset(t, tinyPNG(1), 40*24*time.Hour)

	if _, err := os.Stat(absPath); err != nil {
		t.Fatalf("前置条件：图片应存在: %v", err)
	}

	j := New(f.st, f.blobs, Options{KeepDays: func() int { return 30 }, Log: quietLog()})
	stats, err := j.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if stats.JobsDeleted != 1 {
		t.Errorf("JobsDeleted = %d, want 1", stats.JobsDeleted)
	}
	if stats.AssetsDeleted != 1 {
		t.Errorf("AssetsDeleted = %d, want 1", stats.AssetsDeleted)
	}
	if stats.FilesRemoved != 1 {
		t.Errorf("FilesRemoved = %d, want 1", stats.FilesRemoved)
	}
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		t.Errorf("磁盘文件没被删除: %v", err)
	}
	if _, err := f.st.GetAsset(asset.ID); err == nil {
		t.Error("资产记录没被删除")
	}
}

// TestSweepKeepsRecent 未到期数据必须原样保留。
func TestSweepKeepsRecent(t *testing.T) {
	f := newFixture(t)
	f.seedJob(t, "j_new", domain.StatusDone, 2*24*time.Hour)
	_, absPath := f.seedAsset(t, tinyPNG(2), 2*24*time.Hour)

	j := New(f.st, f.blobs, Options{KeepDays: func() int { return 30 }, Log: quietLog()})
	stats, err := j.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if stats.JobsDeleted != 0 || stats.AssetsDeleted != 0 {
		t.Errorf("误删未到期数据: %+v", stats)
	}
	if _, err := os.Stat(absPath); err != nil {
		t.Errorf("未到期的图片被删了: %v", err)
	}
}

// TestSweepKeepDaysZeroDisables 0 表示不清理，这是 UI 明确给用户的语义。
func TestSweepKeepDaysZeroDisables(t *testing.T) {
	f := newFixture(t)
	f.seedJob(t, "j_ancient", domain.StatusDone, 4000*24*time.Hour)
	_, absPath := f.seedAsset(t, tinyPNG(3), 4000*24*time.Hour)

	j := New(f.st, f.blobs, Options{KeepDays: func() int { return 0 }, Log: quietLog()})
	stats, err := j.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if stats.JobsDeleted != 0 || stats.AssetsDeleted != 0 {
		t.Errorf("keepDays=0 时不该删任何东西: %+v", stats)
	}
	if _, err := os.Stat(absPath); err != nil {
		t.Errorf("keepDays=0 时图片被删了: %v", err)
	}
}

// TestSweepReadsKeepDaysEachRound 设置改动应在下一轮立即生效。
func TestSweepReadsKeepDaysEachRound(t *testing.T) {
	f := newFixture(t)
	f.seedJob(t, "j_40d", domain.StatusDone, 40*24*time.Hour)

	days := 90
	j := New(f.st, f.blobs, Options{KeepDays: func() int { return days }, Log: quietLog()})

	// 第一轮：90 天保留期，40 天的数据不该删。
	if stats, err := j.Sweep(context.Background()); err != nil {
		t.Fatalf("第一轮: %v", err)
	} else if stats.JobsDeleted != 0 {
		t.Fatalf("第一轮误删: %+v", stats)
	}

	// 用户把保留期改成 30 天，下一轮就该删。
	days = 30
	stats, err := j.Sweep(context.Background())
	if err != nil {
		t.Fatalf("第二轮: %v", err)
	}
	if stats.JobsDeleted != 1 {
		t.Errorf("改设置后 JobsDeleted = %d, want 1", stats.JobsDeleted)
	}
}

// TestSweepPrunesAssetsOrphanedByJobDeletion 这一条最容易写错顺序：
// 资产原先被旧任务引用，旧任务被删后资产才变孤儿 —— 同一轮里应该一起清掉。
func TestSweepPrunesAssetsOrphanedByJobDeletion(t *testing.T) {
	f := newFixture(t)

	asset, absPath := f.seedAsset(t, tinyPNG(4), 40*24*time.Hour)
	f.seedJob(t, "j_refs_asset", domain.StatusDone, 40*24*time.Hour)
	if err := f.st.RecordJobImage("j_refs_asset", 0, asset.ID, "done", nil); err != nil {
		t.Fatalf("记录产出: %v", err)
	}

	j := New(f.st, f.blobs, Options{KeepDays: func() int { return 30 }, Log: quietLog()})
	stats, err := j.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if stats.JobsDeleted != 1 {
		t.Errorf("JobsDeleted = %d, want 1", stats.JobsDeleted)
	}
	if stats.AssetsDeleted != 1 {
		t.Errorf("AssetsDeleted = %d, want 1（旧任务删除后资产应成孤儿）", stats.AssetsDeleted)
	}
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		t.Errorf("孤儿图片没被删除: %v", err)
	}
}

// TestSweepKeepsReferencedAssets 被预设引用的资产即使很旧也不能删。
func TestSweepKeepsReferencedAssets(t *testing.T) {
	f := newFixture(t)
	asset, absPath := f.seedAsset(t, tinyPNG(5), 400*24*time.Hour)

	if _, err := f.st.CreatePreset(store.PromptPreset{
		Name: "老预设", Body: []byte(`{"refs":[{"assetId":"` + asset.ID + `","role":"scene"}]}`),
	}); err != nil {
		t.Fatalf("建预设: %v", err)
	}

	j := New(f.st, f.blobs, Options{KeepDays: func() int { return 30 }, Log: quietLog()})
	if _, err := j.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		t.Errorf("被预设引用的资产被误删: %v", err)
	}
}

// TestStartStop 后台协程能起来、能停，且 Stop 幂等。
func TestStartStop(t *testing.T) {
	f := newFixture(t)
	j := New(f.st, f.blobs, Options{
		KeepDays: func() int { return 30 },
		Interval: 10 * time.Millisecond,
		Log:      quietLog(),
	})
	stop := j.Start()
	time.Sleep(30 * time.Millisecond)
	stop()
	stop() // 幂等
}

// TestStopWithoutStart 没启动就停，不能死锁。
func TestStopWithoutStart(t *testing.T) {
	f := newFixture(t)
	j := New(f.st, f.blobs, Options{KeepDays: func() int { return 30 }, Log: quietLog()})
	done := make(chan struct{})
	go func() {
		j.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 在未 Start 时死锁了")
	}
}

// TestSweepNilBlobs 没接 blob 时只删库记录，不 panic。
func TestSweepNilBlobs(t *testing.T) {
	f := newFixture(t)
	f.seedAsset(t, tinyPNG(6), 40*24*time.Hour)

	j := New(f.st, nil, Options{KeepDays: func() int { return 30 }, Log: quietLog()})
	stats, err := j.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if stats.AssetsDeleted != 1 {
		t.Errorf("AssetsDeleted = %d, want 1", stats.AssetsDeleted)
	}
	if stats.FilesRemoved != 0 {
		t.Errorf("没有 blob 时不应报告删了文件: %d", stats.FilesRemoved)
	}
}
