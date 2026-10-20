package app

import (
	"context"
	"testing"
	"time"

	"github.com/imgen/imgen/internal/domain"
)

// 这些测试验证「设置 → 清理任务」这条链路真的接通了。
// store 与 janitor 各自有单测，但只有装配层能证明：
// GET/PUT /settings 改的 keepDays 确实被 janitor 读到了。

// TestJanitorWiredToSettings 默认设置下 janitor 应被装配出来。
func TestJanitorWiredToSettings(t *testing.T) {
	a := newMinimalApp(t)
	if a.Janitor == nil {
		t.Fatal("janitor 没被装配")
	}
	if a.API == nil {
		t.Fatal("API 没被装配")
	}
}

// newMinimalApp 装配 App 并触发 Handler（这会顺带构造 janitor 与 API）。
func newMinimalApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t, "http://127.0.0.1:1/v1") // 上游不可达，本测试不发请求
	if _, err := a.Handler(); err != nil {
		t.Fatalf("构造 Handler: %v", err)
	}
	return a
}

// TestJanitorUsesKeepDaysFromConfig 配置注入的保留天数要生效。
func TestJanitorUsesKeepDaysFromConfig(t *testing.T) {
	a := newMinimalApp(t)
	if a.API == nil {
		t.Fatal("API 未装配")
	}
	if got := a.API.KeepDays(); got != 30 {
		t.Errorf("API.KeepDays() = %d, want 30（来自 config）", got)
	}
}

// TestJanitorSweepRemovesExpiredData 端到端：过期任务与孤儿资产被真删掉。
func TestJanitorSweepRemovesExpiredData(t *testing.T) {
	a := newMinimalApp(t)

	// 造一条 40 天前完成的终态任务。
	j := &domain.Job{
		ID: "j_janitor_e2e", Provider: domain.ProviderAgnes,
		Model: "agnes-image-2.5-flash", Mode: domain.ModeTxt2Img,
		Prompt: "x", Count: 1,
	}
	if err := a.St.CreateJob(j); err != nil {
		t.Fatalf("建任务: %v", err)
	}
	if err := a.St.UpdateJobStatus(j.ID, domain.StatusDone, 100, time.Second, nil); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	old := time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339Nano)
	if _, err := a.St.DB().Exec(
		`UPDATE jobs SET created_at = ?, finished_at = ? WHERE id = ?`, old, old, j.ID); err != nil {
		t.Fatalf("回拨时间: %v", err)
	}

	stats, err := a.Janitor.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if stats.JobsDeleted != 1 {
		t.Errorf("JobsDeleted = %d, want 1", stats.JobsDeleted)
	}
	if _, err := a.St.GetJob(j.ID); err == nil {
		t.Error("过期任务仍在库里")
	}
}

// TestJanitorRespectsRuntimeKeepDaysOverride 改设置后 janitor 立刻跟着变。
// 这是「设置只存意图、janitor 才是执行者」这条分层的核心断言。
func TestJanitorRespectsRuntimeKeepDaysOverride(t *testing.T) {
	a := newMinimalApp(t)

	// 造一条 10 天前完成的任务：在 30 天保留期内，不该被删。
	j := &domain.Job{
		ID: "j_10d", Provider: domain.ProviderAgnes,
		Model: "agnes-image-2.5-flash", Mode: domain.ModeTxt2Img,
		Prompt: "x", Count: 1,
	}
	if err := a.St.CreateJob(j); err != nil {
		t.Fatalf("建任务: %v", err)
	}
	if err := a.St.UpdateJobStatus(j.ID, domain.StatusDone, 100, time.Second, nil); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	when := time.Now().UTC().AddDate(0, 0, -10).Format(time.RFC3339Nano)
	if _, err := a.St.DB().Exec(
		`UPDATE jobs SET created_at = ?, finished_at = ? WHERE id = ?`, when, when, j.ID); err != nil {
		t.Fatalf("回拨时间: %v", err)
	}

	// 保留期 30 天 → 保留。
	if stats, err := a.Janitor.Sweep(context.Background()); err != nil {
		t.Fatalf("第一轮: %v", err)
	} else if stats.JobsDeleted != 0 {
		t.Fatalf("30 天保留期内误删: %+v", stats)
	}

	// 用户把保留期改成 7 天 → 下一轮就该删掉这条 10 天的任务。
	// 走真实的 PUT /settings 路径而不是直接改字段，确保设置层与执行层没有脱节。
	a.API.SetKeepDaysForTest(7)

	stats, err := a.Janitor.Sweep(context.Background())
	if err != nil {
		t.Fatalf("第二轮: %v", err)
	}
	if stats.JobsDeleted != 1 {
		t.Errorf("改成 7 天后 JobsDeleted = %d, want 1", stats.JobsDeleted)
	}
}

// TestJanitorDisabledWhenKeepDaysZero keepDays=0 关闭清理，这是 UI 承诺的语义。
func TestJanitorDisabledWhenKeepDaysZero(t *testing.T) {
	a := newMinimalApp(t)

	j := &domain.Job{
		ID: "j_ancient", Provider: domain.ProviderAgnes,
		Model: "agnes-image-2.5-flash", Mode: domain.ModeTxt2Img,
		Prompt: "x", Count: 1,
	}
	if err := a.St.CreateJob(j); err != nil {
		t.Fatalf("建任务: %v", err)
	}
	if err := a.St.UpdateJobStatus(j.ID, domain.StatusDone, 100, time.Second, nil); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	when := time.Now().UTC().AddDate(0, 0, -4000).Format(time.RFC3339Nano)
	if _, err := a.St.DB().Exec(
		`UPDATE jobs SET created_at = ?, finished_at = ? WHERE id = ?`, when, when, j.ID); err != nil {
		t.Fatalf("回拨时间: %v", err)
	}

	a.API.SetKeepDaysForTest(0)
	stats, err := a.Janitor.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if stats.JobsDeleted != 0 {
		t.Errorf("keepDays=0 时删了 %d 条", stats.JobsDeleted)
	}
	if _, err := a.St.GetJob(j.ID); err != nil {
		t.Errorf("keepDays=0 时任务被删了: %v", err)
	}
}

// TestJanitorAssetsOrphanedByJobSweep 旧任务删掉后，它独占的资产要一起清掉。
func TestJanitorAssetsOrphanedByJobSweep(t *testing.T) {
	a := newMinimalApp(t)

	// 用 blob 层落一张真图并登记。
	asset, err := a.Blobs.Put(tinyPNG, "generated", "", "")
	if err != nil {
		t.Fatalf("落盘: %v", err)
	}
	rec, _, err := a.St.UpsertAsset(asset)
	if err != nil {
		t.Fatalf("登记资产: %v", err)
	}

	j := &domain.Job{
		ID: "j_old_with_asset", Provider: domain.ProviderAgnes,
		Model: "agnes-image-2.5-flash", Mode: domain.ModeTxt2Img,
		Prompt: "x", Count: 1,
	}
	if err := a.St.CreateJob(j); err != nil {
		t.Fatalf("建任务: %v", err)
	}
	if err := a.St.RecordJobImage(j.ID, 0, rec.ID, "done", nil); err != nil {
		t.Fatalf("记录产出: %v", err)
	}
	if err := a.St.UpdateJobStatus(j.ID, domain.StatusDone, 100, time.Second, nil); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	old := time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339Nano)
	if _, err := a.St.DB().Exec(
		`UPDATE jobs SET created_at = ?, finished_at = ? WHERE id = ?`, old, old, j.ID); err != nil {
		t.Fatalf("回拨任务: %v", err)
	}
	if _, err := a.St.DB().Exec(`UPDATE assets SET created_at = ? WHERE id = ?`, old, rec.ID); err != nil {
		t.Fatalf("回拨资产: %v", err)
	}

	stats, err := a.Janitor.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if stats.JobsDeleted != 1 {
		t.Errorf("JobsDeleted = %d, want 1", stats.JobsDeleted)
	}
	if stats.AssetsDeleted != 1 {
		t.Errorf("AssetsDeleted = %d, want 1（任务删除后资产变孤儿）", stats.AssetsDeleted)
	}
	if _, err := a.St.GetAsset(rec.ID); err == nil {
		t.Error("孤儿资产仍在库里")
	}
}