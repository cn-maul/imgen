package store

import (
	"testing"
	"time"

	"github.com/imgen/imgen/internal/domain"
)

// backdate 把任务与资产的 created_at 改到指定时间，模拟「陈旧数据」。
// 直接写库而不是 clock 注入：清理判定读的就是这两列，改列最贴近真实场景。
func backdateJob(t *testing.T, s *Store, id string, when time.Time) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE jobs SET created_at = ? WHERE id = ?`, ts(when), id); err != nil {
		t.Fatalf("回拨任务时间: %v", err)
	}
}

func backdateJobFinished(t *testing.T, s *Store, id string, when time.Time) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE jobs SET finished_at = ? WHERE id = ?`, ts(when), id); err != nil {
		t.Fatalf("回拨任务结束时间: %v", err)
	}
}

func backdateAsset(t *testing.T, s *Store, id string, when time.Time) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE assets SET created_at = ? WHERE id = ?`, ts(when), id); err != nil {
		t.Fatalf("回拨资产时间: %v", err)
	}
}

func jobExists(t *testing.T, s *Store, id string) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("查询任务: %v", err)
	}
	return n > 0
}

// TestPruneTerminalJobs 只删到期的终态任务，排队/运行中的一律保留。
func TestPruneTerminalJobs(t *testing.T) {
	s := openTemp(t)
	old := now().AddDate(0, 0, -40)
	cutoff := now().AddDate(0, 0, -30)

	// 到期且终态 → 应删。
	doneOld := makeJob(t, s, "j_done_old", domain.ProviderAgnes, "old done")
	if err := s.UpdateJobStatus(doneOld.ID, domain.StatusDone, 100, time.Second, nil); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	backdateJob(t, s, doneOld.ID, old)
	backdateJobFinished(t, s, doneOld.ID, old)

	// 到期的非终态（模拟异常退出残留的 running）→ 必须保留。
	runningOld := makeJob(t, s, "j_running_old", domain.ProviderAgnes, "stuck running")
	if err := s.UpdateJobStatus(runningOld.ID, domain.StatusRunning, 10, 0, nil); err != nil {
		t.Fatalf("置运行中: %v", err)
	}
	backdateJob(t, s, runningOld.ID, old)

	// 终态但未到期 → 保留。
	doneNew := makeJob(t, s, "j_done_new", domain.ProviderAgnes, "fresh done")
	if err := s.UpdateJobStatus(doneNew.ID, domain.StatusDone, 100, time.Second, nil); err != nil {
		t.Fatalf("置终态: %v", err)
	}

	n, err := s.PruneTerminalJobs(cutoff)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("删除 %d 条, want 1", n)
	}
	if jobExists(t, s, doneOld.ID) {
		t.Error("到期的终态任务没被删除")
	}
	if !jobExists(t, s, runningOld.ID) {
		t.Error("运行中的任务被误删")
	}
	if !jobExists(t, s, doneNew.ID) {
		t.Error("未到期的任务被误删")
	}
}

// TestPruneTerminalJobsUsesFinishedAt 保留期以结束时间为准：
// 30 天前创建、昨天才跑完的任务不该被清掉。
func TestPruneTerminalJobsUsesFinishedAt(t *testing.T) {
	s := openTemp(t)
	cutoff := now().AddDate(0, 0, -30)

	j := makeJob(t, s, "j_long_running", domain.ProviderAgnes, "跑了很久")
	if err := s.UpdateJobStatus(j.ID, domain.StatusDone, 100, time.Second, nil); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	backdateJob(t, s, j.ID, now().AddDate(0, 0, -60))
	backdateJobFinished(t, s, j.ID, now().AddDate(0, 0, -1))

	if _, err := s.PruneTerminalJobs(cutoff); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if !jobExists(t, s, j.ID) {
		t.Error("按 created_at 而非 finished_at 判定，长任务被误删")
	}
}

// TestPruneTerminalJobsCascadesImages 删任务时其 job_images 应随外键级联清掉。
func TestPruneTerminalJobsCascadesImages(t *testing.T) {
	s := openTemp(t)
	old := now().AddDate(0, 0, -40)
	j := makeJob(t, s, "j_with_images", domain.ProviderAgnes, "带图任务")
	if err := s.RecordJobImage(j.ID, 0, "a_x", "done", nil); err != nil {
		t.Fatalf("记录产出: %v", err)
	}
	if err := s.UpdateJobStatus(j.ID, domain.StatusDone, 100, time.Second, nil); err != nil {
		t.Fatalf("置终态: %v", err)
	}
	backdateJob(t, s, j.ID, old)
	backdateJobFinished(t, s, j.ID, old)

	if _, err := s.PruneTerminalJobs(now().AddDate(0, 0, -30)); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM job_images WHERE job_id = '`+j.ID+`'`); n != 0 {
		t.Errorf("job_images 残留 %d 条, want 0（外键级联失效）", n)
	}
}

// TestPruneUnreferencedAssets 是清理逻辑里最容易误删的一块，逐个引用来源覆盖。
func TestPruneUnreferencedAssets(t *testing.T) {
	s := openTemp(t)
	old := now().AddDate(0, 0, -40)
	cutoff := now().AddDate(0, 0, -30)

	mk := func(sha string) string {
		t.Helper()
		a := mustAsset(t, sha)
		if _, _, err := s.UpsertAsset(a); err != nil {
			t.Fatalf("写入资产 %s: %v", sha, err)
		}
		backdateAsset(t, s, a.ID, old)
		return a.ID
	}

	// ① 孤儿：没人引用 → 应删。
	orphanID := mk(shaFor(0))

	// ② 被任务产出引用 → 保留。
	jobImageID := mk(shaFor(1))
	j := makeJob(t, s, "j_ref", domain.ProviderAgnes, "产出引用")
	if err := s.RecordJobImage(j.ID, 0, jobImageID, "done", nil); err != nil {
		t.Fatalf("记录产出: %v", err)
	}

	// ③ 被任务参数 refs 引用 → 保留。
	jobRefID := mk(shaFor(2))
	j2 := makeJob(t, s, "j_ref2", domain.ProviderAgnes, "参数引用")
	if _, err := s.db.Exec(`UPDATE jobs SET params_json = ? WHERE id = ?`,
		`{"refs":[{"assetId":"`+jobRefID+`"}]}`, j2.ID); err != nil {
		t.Fatalf("写任务参数: %v", err)
	}

	// ④ 被预设 body 引用 → 保留。
	presetRefID := mk(shaFor(3))
	if _, err := s.CreatePreset(PromptPreset{
		ID: "p_ref", Name: "带参考图的预设", Category: "x",
		Body: []byte(`{"prompt":"用 @1 改","refs":[{"assetId":"` + presetRefID + `","role":"scene"}]}`),
	}); err != nil {
		t.Fatalf("建预设: %v", err)
	}

	// ⑤ 未被引用但未到期（新建）→ 保留。
	fresh := mustAsset(t, shaFor(4))
	if _, _, err := s.UpsertAsset(fresh); err != nil {
		t.Fatalf("写入新资产: %v", err)
	}

	victims, err := s.PruneUnreferencedAssets(cutoff)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if len(victims) != 1 {
		t.Fatalf("删除 %d 个资产, want 1（仅孤儿）; got=%+v", len(victims), victims)
	}
	if victims[0].ID != orphanID {
		t.Errorf("删错了对象: got %s, want %s", victims[0].ID, orphanID)
	}

	// 被引用的三个必须都还在。
	for name, id := range map[string]string{
		"任务产出引用": jobImageID,
		"任务参数引用": jobRefID,
		"预设引用":   presetRefID,
	} {
		if _, err := s.GetAsset(id); err != nil {
			t.Errorf("%s 的资产被误删: %s", name, id)
		}
	}
	if _, err := s.GetAsset(fresh.ID); err != nil {
		t.Error("未到期的新资产被误删")
	}
}

// TestPruneUnreferencedAssetsReturnsPath 返回值要带磁盘路径，
// 上层（janitor）才能据此删文件。
func TestPruneUnreferencedAssetsReturnsPath(t *testing.T) {
	s := openTemp(t)
	a := mustAsset(t, shaFor(7))
	if _, _, err := s.UpsertAsset(a); err != nil {
		t.Fatalf("写入资产: %v", err)
	}
	backdateAsset(t, s, a.ID, now().AddDate(0, 0, -40))

	victims, err := s.PruneUnreferencedAssets(now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if len(victims) != 1 {
		t.Fatalf("删除 %d 个, want 1", len(victims))
	}
	if victims[0].Path == "" {
		t.Error("返回的资产没有 Path，上层无法删文件")
	}
}

// TestPruneNoopOnEmptyDB 空库清理不应报错。
func TestPruneNoopOnEmptyDB(t *testing.T) {
	s := openTemp(t)
	n, err := s.PruneTerminalJobs(now())
	if err != nil || n != 0 {
		t.Fatalf("空库清理任务: n=%d err=%v", n, err)
	}
	v, err := s.PruneUnreferencedAssets(now())
	if err != nil || len(v) != 0 {
		t.Fatalf("空库清理资产: len=%d err=%v", len(v), err)
	}
}

// TestPruneToleratesBadJSON 一条脏 JSON 不该让整轮清理失败。
func TestPruneToleratesBadJSON(t *testing.T) {
	s := openTemp(t)
	j := makeJob(t, s, "j_bad_json", domain.ProviderAgnes, "脏数据")
	if _, err := s.db.Exec(`UPDATE jobs SET params_json = 'not json at all' WHERE id = ?`, j.ID); err != nil {
		t.Fatalf("写脏数据: %v", err)
	}

	a := mustAsset(t, shaFor(8))
	if _, _, err := s.UpsertAsset(a); err != nil {
		t.Fatalf("写入资产: %v", err)
	}
	backdateAsset(t, s, a.ID, now().AddDate(0, 0, -40))

	victims, err := s.PruneUnreferencedAssets(now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("脏 JSON 让清理整体失败: %v", err)
	}
	if len(victims) != 1 {
		t.Errorf("删除 %d 个资产, want 1", len(victims))
	}
}
