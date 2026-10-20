package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imgen/imgen/internal/domain"
)

// openTemp 在 testing.TempDir() 下建库。
//
// ⚠️ 必须用真实文件路径：驱动与 db.SetMaxOpenConns(1) 冲突，
// `:memory:` 下每个连接各是一份独立数据库，会看到「表不存在」。
func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "imgen.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func countOf(t *testing.T, s *Store, q string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// embeddedMigrationCount 数一遍内嵌的迁移文件。
//
// 断言「已应用迁移数 == 文件数」而不是写死数字：
// 每次新增迁移都要改测试是纯粹的维护负担，而且很容易漏。
func embeddedMigrationCount(t *testing.T) int {
	t.Helper()
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("读迁移目录: %v", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			n++
		}
	}
	return n
}

func mustAsset(t *testing.T, sha string) domain.Asset {
	t.Helper()
	return domain.Asset{
		ID:     "a_" + sha[:16],
		SHA256: sha,
		MIME:   "image/png",
		Ext:    ".png",
		Width:  64,
		Height: 64,
		Bytes:  1234,
		Path:   "ab/cd/" + sha + ".png",
		Source: "generated",
	}
}

// TestOpenRunsMigrations 首次 Open 应当建表并灌入内置通道与模型。
func TestOpenRunsMigrations(t *testing.T) {
	s := openTemp(t)

	if n := countOf(t, s, `SELECT COUNT(*) FROM providers`); n != 2 {
		t.Errorf("providers = %d, want 2", n)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM models`); n != 5 {
		t.Errorf("models = %d, want 5", n)
	}
	// 迁移记录应被写入，供幂等判断使用。
	if n, want := countOf(t, s, `SELECT COUNT(*) FROM schema_migrations`), embeddedMigrationCount(t); n != want {
		t.Errorf("schema_migrations = %d, want %d", n, want)
	}

	providers, err := s.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("ListProviders = %d, want 2", len(providers))
	}
	for _, p := range providers {
		if !p.Enabled {
			t.Errorf("通道 %s 默认应为启用", p.ID)
		}
		if p.HasKey {
			t.Errorf("通道 %s 初始不应有 Key", p.ID)
		}
		if p.BaseURL == "" {
			t.Errorf("通道 %s 缺少 base_url", p.ID)
		}
	}

	models, err := s.ListModels(domain.ProviderAgnes)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 3 {
		t.Errorf("agnes 模型数 = %d, want 3", len(models))
	}
	if got, err := s.ListModels("nope"); err != nil || len(got) != 0 {
		t.Errorf("未知通道应返回空列表, got %d err %v", len(got), err)
	}
}

// TestOpenIsIdempotent 重复 Open 同一路径不报错、不重复插入内置数据。
func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imgen.db")

	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("第 %d 次 Open: %v", i+1, err)
		}
		if n := countOf(t, s, `SELECT COUNT(*) FROM providers`); n != 2 {
			t.Fatalf("第 %d 次 Open 后 providers = %d, want 2（内置数据被重复插入）", i+1, n)
		}
		if n := countOf(t, s, `SELECT COUNT(*) FROM models`); n != 5 {
			t.Fatalf("第 %d 次 Open 后 models = %d, want 5", i+1, n)
		}
		if n, want := countOf(t, s, `SELECT COUNT(*) FROM schema_migrations`), embeddedMigrationCount(t); n != want {
			t.Fatalf("第 %d 次 Open 后 schema_migrations = %d, want %d", i+1, n, want)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
}

// TestOpenIdempotentKeepsUserData 重复 Open 不应丢掉已有业务数据。
func TestOpenIdempotentKeepsUserData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imgen.db")

	s1 := mustOpen(t, path)
	j := &domain.Job{ID: "j_keep", Provider: domain.ProviderAgnes, Model: "m", Mode: domain.ModeTxt2Img,
		Prompt: "保留我", Count: 1}
	if err := s1.CreateJob(j); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := s1.SetKV("theme", map[string]string{"mode": "dark"}); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	_ = s1.Close()

	s2 := mustOpen(t, path)
	defer s2.Close()
	got, err := s2.GetJob("j_keep")
	if err != nil {
		t.Fatalf("重开后读任务: %v", err)
	}
	if got.Prompt != "保留我" {
		t.Errorf("prompt = %q", got.Prompt)
	}
	var theme map[string]string
	if _, err := s2.GetKV("theme", &theme); err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if theme["mode"] != "dark" {
		t.Errorf("theme = %v", theme)
	}
}

func mustOpen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	return s
}

// TestUpsertAssetDedup 同一 sha 二次写入返回 created=false 且不新增行。
func TestUpsertAssetDedup(t *testing.T) {
	s := openTemp(t)
	a := mustAsset(t, "1111111111111111111111111111111111111111111111111111111111111111")

	got, created, err := s.UpsertAsset(a)
	if err != nil {
		t.Fatalf("首次 UpsertAsset: %v", err)
	}
	if !created {
		t.Error("首次写入 created 应为 true")
	}
	if got.ID != a.ID || got.SHA256 != a.SHA256 {
		t.Errorf("返回资产不符: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt 应被回填")
	}

	again, created2, err := s.UpsertAsset(a)
	if err != nil {
		t.Fatalf("二次 UpsertAsset: %v", err)
	}
	if created2 {
		t.Error("二次写入 created 应为 false")
	}
	if again.ID != a.ID {
		t.Errorf("二次写入返回了不同记录: %s", again.ID)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM assets`); n != 1 {
		t.Errorf("assets = %d, want 1", n)
	}
}

// TestUpsertAssetRoundTrip 写入后按 id / sha 都能读回，字段无损。
func TestUpsertAssetRoundTrip(t *testing.T) {
	s := openTemp(t)
	a := mustAsset(t, "2222222222222222222222222222222222222222222222222222222222222222")
	a.SourceURL = "https://up.example/x.png"
	now := time.Now().UTC().Truncate(time.Second)
	a.ExpiresAt = &now

	if _, _, err := s.UpsertAsset(a); err != nil {
		t.Fatalf("UpsertAsset: %v", err)
	}
	got, err := s.GetAsset(a.ID)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if got.MIME != a.MIME || got.Ext != a.Ext || got.Width != a.Width || got.Height != a.Height {
		t.Errorf("字段回读不符: %+v", got)
	}
	if got.Bytes != a.Bytes || got.Path != a.Path || got.Source != a.Source {
		t.Errorf("字段回读不符: %+v", got)
	}
	if got.SourceURL != a.SourceURL {
		t.Errorf("SourceURL = %q", got.SourceURL)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(now) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, now)
	}

	path, err := s.AssetPath(a.ID)
	if err != nil || path != a.Path {
		t.Errorf("AssetPath = %q, %v", path, err)
	}
}

// TestUpsertAssetConcurrent 并发去重：不 panic、不撞 race、有且仅有一次 created=true。
func TestUpsertAssetConcurrent(t *testing.T) {
	s := openTemp(t)
	sha := "3333333333333333333333333333333333333333333333333333333333333333"

	const n = 24
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		createdN int
		errs     []error
		ids      = map[string]int{}
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			got, created, err := s.UpsertAsset(mustAsset(t, sha))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if created {
				createdN++
			}
			ids[got.ID]++
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 0 {
		t.Fatalf("并发 UpsertAsset 报错 %d 次，首个: %v", len(errs), errs[0])
	}
	if createdN != 1 {
		t.Errorf("created=true 的次数 = %d, want 1", createdN)
	}
	if len(ids) != 1 {
		t.Errorf("并发写入产出了 %d 种 ID: %v", len(ids), ids)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM assets`); n != 1 {
		t.Errorf("assets = %d, want 1", n)
	}
}

// TestUpsertAssetConcurrentDistinct 不同 sha 并发写入互不干扰。
func TestUpsertAssetConcurrentDistinct(t *testing.T) {
	s := openTemp(t)
	const n = 12
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			a := mustAsset(t, shaFor(i))
			_, created, err := s.UpsertAsset(a)
			if err != nil {
				errCh <- err
				return
			}
			if !created {
				errCh <- errors.New("不同 sha 却 reported created=false")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("并发写入: %v", err)
	}
	if got := countOf(t, s, `SELECT COUNT(*) FROM assets`); got != n {
		t.Errorf("assets = %d, want %d", got, n)
	}
}

func shaFor(i int) string {
	b := make([]byte, 64)
	for j := range b {
		b[j] = byte('a' + (i+j)%26)
	}
	return string(b)
}

// TestGetAssetNotFound 不存在的资产返回哨兵错误。
func TestGetAssetNotFound(t *testing.T) {
	s := openTemp(t)
	if _, err := s.GetAsset("a_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetAsset 缺失 = %v, want ErrNotFound", err)
	}
	if _, err := s.AssetPath("a_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("AssetPath 缺失 = %v, want ErrNotFound", err)
	}
}

// TestListAssets 过滤与分页。
func TestListAssets(t *testing.T) {
	s := openTemp(t)
	fixtures := []domain.Asset{
		mustAsset(t, shaFor(0)),
		mustAsset(t, shaFor(1)),
		mustAsset(t, shaFor(2)),
	}
	fixtures[0].Source = "generated"
	fixtures[1].Source = "upload"
	fixtures[1].SourceURL = "https://cdn.example/pic.png"
	fixtures[2].Source = "generated"
	for i := range fixtures {
		if _, _, err := s.UpsertAsset(fixtures[i]); err != nil {
			t.Fatalf("UpsertAsset %d: %v", i, err)
		}
	}
	// 用显式时间戳保证排序确定（created_at 默认取 now，同毫秒可能并列）。
	for i, a := range fixtures {
		if _, err := s.DB().Exec(`UPDATE assets SET created_at = ? WHERE id = ?`,
			ts(time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC)), a.ID); err != nil {
			t.Fatalf("设置 created_at: %v", err)
		}
	}

	all, total, err := s.ListAssets(AssetFilter{})
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("total/len = %d/%d, want 3/3", total, len(all))
	}
	// created_at DESC：最后的排最前。
	if all[0].ID != fixtures[2].ID {
		t.Errorf("未倒序: 首位 = %s, want %s", all[0].ID, fixtures[2].ID)
	}

	gen, total, err := s.ListAssets(AssetFilter{Source: "generated"})
	if err != nil {
		t.Fatalf("ListAssets(source): %v", err)
	}
	if total != 2 || len(gen) != 2 {
		t.Errorf("source=generated → %d/%d, want 2/2", total, len(gen))
	}
	for _, a := range gen {
		if a.Source != "generated" {
			t.Errorf("过滤失效: %s 是 %s", a.ID, a.Source)
		}
	}

	// Query 命中 source_url。
	q, total, err := s.ListAssets(AssetFilter{Query: "cdn.example"})
	if err != nil {
		t.Fatalf("ListAssets(query): %v", err)
	}
	if total != 1 || len(q) != 1 || q[0].ID != fixtures[1].ID {
		t.Errorf("query 过滤 = %d 项, want 仅 %s", total, fixtures[1].ID)
	}

	// 分页：limit + offset。
	page, total, err := s.ListAssets(AssetFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListAssets(page1): %v", err)
	}
	if total != 3 {
		t.Errorf("分页时 total = %d, want 3（total 不受 limit 影响）", total)
	}
	if len(page) != 2 {
		t.Fatalf("第一页 = %d, want 2", len(page))
	}
	page2, _, err := s.ListAssets(AssetFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListAssets(page2): %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("第二页 = %d, want 1", len(page2))
	}
	if page2[0].ID == page[0].ID || page2[0].ID == page[1].ID {
		t.Error("两页出现重复条目")
	}

	// 空结果应为非 nil 空切片（前端直接取 len）。
	none, total, err := s.ListAssets(AssetFilter{Source: "nope"})
	if err != nil || total != 0 || none == nil || len(none) != 0 {
		t.Errorf("空结果 = %v, total=%d, err=%v", none, total, err)
	}
}

// TestDeleteAsset 删除存在/不存在的资产。
func TestDeleteAsset(t *testing.T) {
	s := openTemp(t)
	a := mustAsset(t, shaFor(0))
	if _, _, err := s.UpsertAsset(a); err != nil {
		t.Fatalf("UpsertAsset: %v", err)
	}
	if err := s.DeleteAsset(a.ID); err != nil {
		t.Fatalf("DeleteAsset: %v", err)
	}
	if err := s.DeleteAsset(a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("重复删除 = %v, want ErrNotFound", err)
	}
	if err := s.DeleteAsset("a_never"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("删除不存在 = %v, want ErrNotFound", err)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM assets`); n != 0 {
		t.Errorf("删除后 assets = %d", n)
	}
}

// setJobCreatedAt 直接改 created_at，让 ORDER BY created_at DESC 的结果确定。
func setJobCreatedAt(t *testing.T, s *Store, id string, when time.Time) {
	t.Helper()
	if _, err := s.DB().Exec(`UPDATE jobs SET created_at = ? WHERE id = ?`, ts(when), id); err != nil {
		t.Fatalf("设置 created_at: %v", err)
	}
}

func makeJob(t *testing.T, s *Store, id string, provider domain.ProviderID, prompt string) *domain.Job {
	t.Helper()
	j := &domain.Job{
		ID:       id,
		Provider: provider,
		Model:    "m-" + string(provider),
		Mode:     domain.ModeTxt2Img,
		Prompt:   prompt,
		Count:    1,
		Params:   domain.Request{Prompt: prompt, Provider: provider, Count: 1},
	}
	if err := s.CreateJob(j); err != nil {
		t.Fatalf("CreateJob(%s): %v", id, err)
	}
	if j.CreatedAt.IsZero() {
		t.Errorf("CreateJob 应回填 CreatedAt: %s", id)
	}
	return j
}

// TestListJobsPagination 分页的 limit/offset/total。
// TestListJobsPagination 分页的 limit/offset/total 与排序。
func TestListJobsPagination(t *testing.T) {
	s := openTemp(t)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	ids := []string{"j_a", "j_b", "j_c", "j_d", "j_e"}
	for i, id := range ids {
		provider := domain.ProviderAgnes
		if i%2 == 0 {
			provider = domain.ProviderSenseNova
		}
		makeJob(t, s, id, provider, "prompt-"+id)
		setJobCreatedAt(t, s, id, base.Add(time.Duration(i)*time.Minute))
	}
	// 让一个任务处于 done，便于按状态过滤。
	if err := s.UpdateJobStatus("j_a", domain.StatusDone, 100, 2*time.Second, nil); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	page, total, err := s.ListJobs(JobFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5（total 不受 limit 影响）", total)
	}
	if len(page) != 2 {
		t.Fatalf("首页 = %d 项, want 2", len(page))
	}
	// created_at DESC：j_e 最新。
	if page[0].ID != "j_e" || page[1].ID != "j_d" {
		t.Errorf("首页 = %s/%s, want j_e/j_d（created_at DESC）", page[0].ID, page[1].ID)
	}
	// created_at 必须能解析回来，历史列表靠它显示时间。
	if page[0].CreatedAt.IsZero() {
		t.Error("ListJobs 返回的 CreatedAt 为零值")
	}
	if !page[0].CreatedAt.Equal(base.Add(4 * time.Minute)) {
		t.Errorf("CreatedAt = %v, want %v", page[0].CreatedAt, base.Add(4*time.Minute))
	}

	page2, total2, err := s.ListJobs(JobFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListJobs(page2): %v", err)
	}
	if total2 != 5 || len(page2) != 2 {
		t.Errorf("第二页 = %d 项, total=%d, want 2/5", len(page2), total2)
	}
	if page2[0].ID == page[0].ID || page2[0].ID == page[1].ID {
		t.Error("两页出现重复条目")
	}

	// limit 越界回落默认 50。
	all, _, err := s.ListJobs(JobFilter{Limit: 9999})
	if err != nil {
		t.Fatalf("ListJobs(limit 越界): %v", err)
	}
	if len(all) != 5 {
		t.Errorf("limit 越界时 = %d 项, want 5", len(all))
	}
}

// TestListJobsFilters provider / status / query 三个维度。
func TestListJobsFilters(t *testing.T) {
	s := openTemp(t)
	makeJob(t, s, "j_1", domain.ProviderSenseNova, "一只海豹")
	makeJob(t, s, "j_2", domain.ProviderAgnes, "一座灯塔")
	makeJob(t, s, "j_3", domain.ProviderAgnes, "海豹在灯塔下")
	if err := s.UpdateJobStatus("j_3", domain.StatusFailed, 0, time.Second,
		domain.NewError(domain.CodeUpstreamRejected, "被拒")); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	check := func(f JobFilter, want int, desc string) {
		t.Helper()
		got, total, err := s.ListJobs(f)
		if err != nil {
			t.Fatalf("ListJobs(%s) = %v", desc, err)
		}
		if total != want || len(got) != want {
			t.Errorf("%s: 得到 %d 项 / total=%d, want %d", desc, len(got), total, want)
		}
	}

	check(JobFilter{Provider: domain.ProviderAgnes}, 2, "provider=agnes")
	check(JobFilter{Provider: domain.ProviderSenseNova}, 1, "provider=sensenova")
	check(JobFilter{Status: domain.StatusFailed}, 1, "status=failed")
	check(JobFilter{Status: domain.StatusQueued}, 2, "status=queued")
	check(JobFilter{Query: "海豹"}, 2, "query=海豹")
	// 多条件 AND：agnes 且 prompt 含「海豹」→ 只有 j_3。
	check(JobFilter{Provider: domain.ProviderAgnes, Query: "海豹"}, 1, "agnes+海豹")
	// 条件互斥 → 空。
	check(JobFilter{Provider: domain.ProviderSenseNova, Status: domain.StatusFailed}, 0, "sensenova+failed")

	// query 应命中 prompt 子串而非全等。
	if got, _, _ := s.ListJobs(JobFilter{Query: "灯塔"}); len(got) != 2 {
		t.Errorf("query=灯塔 = %d 项, want 2", len(got))
	}
}

// TestCancelJob queued / running 可以取消。
func TestCancelJob(t *testing.T) {
	s := openTemp(t)
	for _, st := range []domain.JobStatus{domain.StatusQueued, domain.StatusRunning} {
		id := "j_" + string(st)
		makeJob(t, s, id, domain.ProviderAgnes, "p")
		if err := s.UpdateJobStatus(id, st, 0, 0, nil); err != nil {
			t.Fatalf("UpdateJobStatus(%s): %v", st, err)
		}
		if err := s.CancelJob(id); err != nil {
			t.Fatalf("CancelJob(%s): %v", st, err)
		}
		got, err := s.GetJob(id)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if got.Status != string(domain.StatusCanceled) {
			t.Errorf("%s 取消后状态 = %q, want canceled", st, got.Status)
		}
		if got.Finished == nil {
			t.Errorf("%s 取消后 finished_at 应被写入", st)
		}
	}
}

// TestCancelJobMissing 取消不存在的任务 → ErrNotFound。
func TestCancelJobMissing(t *testing.T) {
	s := openTemp(t)
	if err := s.CancelJob("j_ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("CancelJob 不存在 = %v, want ErrNotFound", err)
	}
}

// TestCancelJobAlreadyFinished 锁定「已完成任务」的现有行为：
// RowsAffected==0 但记录存在 → 返回 nil，即静默成功、状态不被改动。
//
// 这是当前实现（store/jobs.go）的刻意取舍，改之前先看这里。
//
// partial 同样是终态（Manager.finish 会写它），故 finished_at 应已存在，
// 取消不应该把它清掉。
func TestCancelJobAlreadyFinished(t *testing.T) {
	s := openTemp(t)
	for _, st := range []domain.JobStatus{domain.StatusDone, domain.StatusFailed, domain.StatusCanceled, domain.StatusPartial} {
		id := "j_done_" + string(st)
		makeJob(t, s, id, domain.ProviderAgnes, "p")
		if err := s.UpdateJobStatus(id, st, 100, time.Second, nil); err != nil {
			t.Fatalf("UpdateJobStatus(%s): %v", st, err)
		}
		before, err := s.GetJob(id)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}

		if err := s.CancelJob(id); err != nil {
			t.Errorf("CancelJob(%s) = %v, 当前实现为静默成功（返回 nil）", st, err)
			continue
		}
		after, err := s.GetJob(id)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if after.Status != before.Status {
			t.Errorf("%s：取消已完成任务不该改状态，%q → %q", st, before.Status, after.Status)
		}
		if after.Finished == nil {
			t.Errorf("%s：finished_at 被清空了", st)
		}
	}
}

// TestUpdateJobStatusTerminalSet 终态集合必须把 partial 算进去：
// Manager.finish 会把 partial 作为终态写入，前端历史页也依赖 finished_at 排序。
func TestUpdateJobStatusTerminalSet(t *testing.T) {
	s := openTemp(t)
	cases := map[domain.JobStatus]bool{ // status → 是否应写 finished_at
		domain.StatusDone:     true,
		domain.StatusFailed:   true,
		domain.StatusCanceled: true,
		domain.StatusPartial:  true,
		domain.StatusRunning:  false,
		domain.StatusQueued:   false,
	}
	for st, wantFinished := range cases {
		id := "j_term_" + string(st)
		makeJob(t, s, id, domain.ProviderAgnes, "p")
		if err := s.UpdateJobStatus(id, st, 10, 0, nil); err != nil {
			t.Fatalf("UpdateJobStatus(%s): %v", st, err)
		}
		got, err := s.GetJob(id)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if got.Status != string(st) {
			t.Errorf("%s：状态 = %q", st, got.Status)
		}
		if wantFinished && got.Finished == nil {
			t.Errorf("%s：应当写入 finished_at，实际为 nil", st)
		}
		if !wantFinished && got.Finished != nil {
			t.Errorf("%s：不该写 finished_at，实际 = %v", st, got.Finished)
		}
		// started_at 只应在进入 running 时写入。
		if st == domain.StatusRunning && got.StartedAt == nil {
			t.Errorf("%s：应写入 started_at", st)
		}
	}
}

// TestJobLifecycle CreateJob → UpdateJobStatus → RecordJobImage → GetJob 全链路。
func TestJobLifecycle(t *testing.T) {
	s := openTemp(t)
	j := &domain.Job{
		ID:       "j_life",
		Provider: domain.ProviderSenseNova,
		Model:    "sensenova-u1.5-lite",
		Mode:     domain.ModeTxt2Img,
		Prompt:   "全流程",
		Count:    2,
		Params: domain.Request{Provider: domain.ProviderSenseNova, Prompt: "全流程", Count: 2,
			Tier: domain.Tier2K, Aspect: domain.Aspect16_9},
	}
	if err := s.CreateJob(j); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := s.UpdateJobStatus(j.ID, domain.StatusRunning, 0, 0, nil); err != nil {
		t.Fatalf("UpdateJobStatus(running): %v", err)
	}
	if err := s.RecordJobImage(j.ID, 0, "a_ok", "done", nil); err != nil {
		t.Fatalf("RecordJobImage: %v", err)
	}
	derr := domain.NewError(domain.CodeUpstreamQuota, "限流了")
	derr.Retryable = true
	if err := s.RecordJobImage(j.ID, 1, "", "failed", derr); err != nil {
		t.Fatalf("RecordJobImage(failed): %v", err)
	}
	if err := s.UpdateJobStatus(j.ID, domain.StatusPartial, 50, 1500*time.Millisecond, derr); err != nil {
		t.Fatalf("UpdateJobStatus(partial): %v", err)
	}

	got, err := s.GetJob(j.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != string(domain.StatusPartial) {
		t.Errorf("status = %q, want partial", got.Status)
	}
	if got.Progress != 50 {
		t.Errorf("progress = %d, want 50", got.Progress)
	}
	if got.LatencyMS != 1500 {
		t.Errorf("latency_ms = %d, want 1500", got.LatencyMS)
	}
	if got.StartedAt == nil || got.Finished == nil {
		t.Error("started_at / finished_at 应被写入")
	}
	if got.Err == nil || got.Err.Code != domain.CodeUpstreamQuota || !got.Err.Retryable {
		t.Errorf("Err = %+v", got.Err)
	}
	if got.Params.Tier != domain.Tier2K || got.Params.Aspect != domain.Aspect16_9 {
		t.Errorf("params_json 往返丢失: %+v", got.Params)
	}
	if len(got.Images) != 2 {
		t.Fatalf("产出 = %d 条, want 2", len(got.Images))
	}
	if got.Images[0].AssetID != "a_ok" || got.Images[0].Status != "done" {
		t.Errorf("seq0 = %+v", got.Images[0])
	}
	if got.Images[1].Err == nil || got.Images[1].Err.Code != domain.CodeUpstreamQuota {
		t.Errorf("seq1 错误未回读: %+v", got.Images[1])
	}

	// 同 seq 重写应更新而非报错（ON CONFLICT DO UPDATE）。
	if err := s.RecordJobImage(j.ID, 1, "a_retry_ok", "done", nil); err != nil {
		t.Fatalf("重写 seq: %v", err)
	}
	got, _ = s.GetJob(j.ID)
	if len(got.Images) != 2 || got.Images[1].AssetID != "a_retry_ok" || got.Images[1].Err != nil {
		t.Errorf("重写后 seq1 = %+v", got.Images[1])
	}
}

// TestGetJobNotFound 读取不存在的任务。
func TestGetJobNotFound(t *testing.T) {
	s := openTemp(t)
	if _, err := s.GetJob("j_ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetJob 不存在 = %v, want ErrNotFound", err)
	}
}

// TestUpdateJobStatusClearsError 重新进入 running 时应清掉上一次的错误。
func TestUpdateJobStatusClearsError(t *testing.T) {
	s := openTemp(t)
	makeJob(t, s, "j_clr", domain.ProviderAgnes, "p")
	if err := s.UpdateJobStatus("j_clr", domain.StatusFailed, 0, 0,
		domain.NewError(domain.CodeUpstreamTimeout, "超时")); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob("j_clr")
	if got.Err == nil {
		t.Fatal("错误未写入")
	}
	if err := s.UpdateJobStatus("j_clr", domain.StatusRunning, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetJob("j_clr")
	if got.Err != nil {
		t.Errorf("进入 running 后应清空 error_json, got %+v", got.Err)
	}
}

// TestSetGetKV 配置读写往返，以及缺失键回落默认值。
func TestSetGetKV(t *testing.T) {
	s := openTemp(t)

	// 缺失键返回默认。
	type settings struct {
		Concurrency int  `json:"concurrency"`
		Dark        bool `json:"dark"`
	}
	def := settings{Concurrency: 2}
	got, err := s.GetKV("settings", &def)
	if err != nil {
		t.Fatalf("GetKV 缺失键: %v", err)
	}
	if got.(*settings).Concurrency != 2 {
		t.Errorf("默认值 = %+v", got)
	}

	if err := s.SetKV("settings", settings{Concurrency: 8, Dark: true}); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	var out settings
	if _, err := s.GetKV("settings", &out); err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if out.Concurrency != 8 || !out.Dark {
		t.Errorf("往返后 = %+v, want {8 true}", out)
	}

	// 覆盖写入（ON CONFLICT DO UPDATE）。
	if err := s.SetKV("settings", settings{Concurrency: 3}); err != nil {
		t.Fatalf("SetKV 覆盖: %v", err)
	}
	out = settings{}
	if _, err := s.GetKV("settings", &out); err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if out.Concurrency != 3 || out.Dark {
		t.Errorf("覆盖后 = %+v, want {3 false}", out)
	}

	// 标量值。
	if err := s.SetKV("maxUploadMB", 20); err != nil {
		t.Fatalf("SetKV 标量: %v", err)
	}
	var mb int
	if _, err := s.GetKV("maxUploadMB", &mb); err != nil || mb != 20 {
		t.Errorf("标量往返 = %d, %v", mb, err)
	}
}

// TestUpdateProvider 通道配置更新与缺失通道。
func TestUpdateProvider(t *testing.T) {
	s := openTemp(t)

	enabled := false
	if err := s.UpdateProvider(domain.ProviderAgnes, ProviderConfig{
		BaseURL: "https://proxy.example/v1/", Enabled: &enabled, TimeoutMS: intPtr(30000),
	}); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	// 密文长度 >0 即视为已配置 Key。
	if err := s.UpdateProvider(domain.ProviderAgnes, ProviderConfig{APIKey: []byte("cipher")}); err != nil {
		t.Fatalf("UpdateProvider(key): %v", err)
	}

	list, err := s.ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	var agnes Provider
	for _, p := range list {
		if p.ID == domain.ProviderAgnes {
			agnes = p
		}
	}
	// BaseURL 的尾斜杠应被裁掉。
	if agnes.BaseURL != "https://proxy.example/v1" {
		t.Errorf("BaseURL = %q", agnes.BaseURL)
	}
	if agnes.Enabled {
		t.Error("enabled 未更新为 false")
	}
	if agnes.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", agnes.Timeout)
	}
	if !agnes.HasKey {
		t.Error("HasKey 应为 true")
	}

	key, err := s.GetProviderKey(domain.ProviderAgnes)
	if err != nil || string(key) != "cipher" {
		t.Errorf("GetProviderKey = %q, %v", key, err)
	}

	// 清空 Key 后应报「未配置」。
	if err := s.UpdateProvider(domain.ProviderAgnes, ProviderConfig{ClearKey: true}); err != nil {
		t.Fatalf("ClearKey: %v", err)
	}
	if _, err := s.GetProviderKey(domain.ProviderAgnes); !errors.Is(err, domain.ErrNoCredential) {
		t.Errorf("清空后 GetProviderKey = %v, want ErrNoCredential", err)
	}

	// 未配置 Key 的通道同样是 ErrNoCredential，而不是 ErrNotFound。
	if _, err := s.GetProviderKey(domain.ProviderSenseNova); !errors.Is(err, domain.ErrNoCredential) {
		t.Errorf("空 Key = %v, want ErrNoCredential", err)
	}

	// 未知通道 → ErrNotFound。
	if err := s.UpdateProvider("nope", ProviderConfig{BaseURL: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("更新未知通道 = %v, want ErrNotFound", err)
	}
	if _, err := s.GetProviderKey("nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("读取未知通道 = %v, want ErrNotFound", err)
	}

	// 空配置应直接返回，不改动任何东西。
	if err := s.UpdateProvider(domain.ProviderAgnes, ProviderConfig{}); err != nil {
		t.Errorf("空配置不应报错: %v", err)
	}
}

func intPtr(n int) *int { return &n }

// TestRecordUsage 用量累加与查询窗口。
func TestRecordUsage(t *testing.T) {
	s := openTemp(t)
	s.RecordUsage("agnes", "agnes-image-2.5-flash", true, 1)
	s.RecordUsage("agnes", "agnes-image-2.5-flash", true, 1)
	s.RecordUsage("agnes", "agnes-image-2.5-flash", false, 0)

	stats, err := s.ListUsage(14)
	if err != nil {
		t.Fatalf("ListUsage: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("用量 = %d 条, want 1", len(stats))
	}
	if stats[0].Calls != 3 || stats[0].OK != 2 || stats[0].Failed != 1 || stats[0].Images != 2 {
		t.Errorf("用量 = %+v, want calls3/ok2/failed1/images2", stats[0])
	}
	if stats[0].Day == "" || stats[0].Provider != "agnes" {
		t.Errorf("用量维度 = %+v", stats[0])
	}

	// days 非法值回落到 14，不影响可查性。
	if _, err := s.ListUsage(0); err != nil {
		t.Errorf("ListUsage(0): %v", err)
	}
	if _, err := s.ListUsage(999); err != nil {
		t.Errorf("ListUsage(999): %v", err)
	}
}

// TestSplitStatements 迁移切分：跳过注释行、按分号断句。
func TestSplitStatements(t *testing.T) {
	src := "-- 这是注释\nCREATE TABLE a (x INT);\nCREATE TABLE b (\n  y INT\n);\n-- 尾部注释\n"
	got := splitStatements(src)
	if len(got) != 2 {
		t.Fatalf("切出 %d 条: %#v", len(got), got)
	}
	if !contains(got[0], "CREATE TABLE a") {
		t.Errorf("首条 = %q", got[0])
	}
	if !contains(got[1], "CREATE TABLE b") {
		t.Errorf("次条 = %q", got[1])
	}
	for _, s := range got {
		if contains(s, "-- 这是注释") || contains(s, "-- 尾部注释") {
			t.Errorf("注释未剔除: %q", s)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestParseTS 时间戳解析容错（RFC3339Nano / SQLite 默认格式 / 空值）。
func TestParseTS(t *testing.T) {
	want := time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)
	for _, raw := range []string{
		want.Format(time.RFC3339Nano),
		want.Format(time.RFC3339),
		"2026-05-01 12:30:00",
	} {
		got := parseTS(sql.NullString{String: raw, Valid: true})
		if got == nil || !got.Equal(want) {
			t.Errorf("parseTS(%q) = %v, want %v", raw, got, want)
		}
	}
	if got := parseTS(sql.NullString{}); got != nil {
		t.Errorf("空值应返回 nil, got %v", got)
	}
	if got := parseTS(sql.NullString{String: "not a time", Valid: true}); got != nil {
		t.Errorf("非法值应返回 nil, got %v", got)
	}
}
