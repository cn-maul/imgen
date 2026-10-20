package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/imgen/imgen/internal/domain"
)

// CreateJob 插入一个新任务。
func (s *Store) CreateJob(j *domain.Job) error {
	params, err := json.Marshal(j.Params)
	if err != nil {
		return fmt.Errorf("序列化任务参数: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO jobs(id, provider, model, mode, prompt, params_json, status, progress, count, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		j.ID, string(j.Provider), j.Model, string(j.Mode), j.Prompt,
		string(params), string(domain.StatusQueued), 0, j.Count, ts(now()))
	if err != nil {
		return fmt.Errorf("创建任务: %w", err)
	}
	j.CreatedAt = now()
	return nil
}

// UpdateJobStatus 更新任务状态、进度与耗时。
func (s *Store) UpdateJobStatus(id string, st domain.JobStatus, progress int, latency time.Duration, e *domain.Error) error {
	sets := []string{"status = ?", "progress = ?"}
	args := []any{string(st), progress}

	switch st {
	case domain.StatusRunning:
		sets = append(sets, "started_at = ?", "error_json = NULL")
		args = append(args, ts(now()))
	// partial 也是终态（见 Manager.finish），故与 done/failed 一样要落 finished_at，
	// 否则历史列表里「部分成功」的任务永远没有结束时间。
	case domain.StatusDone, domain.StatusPartial, domain.StatusFailed, domain.StatusCanceled:
		sets = append(sets, "finished_at = ?")
		args = append(args, ts(now()))
	}
	if latency > 0 {
		sets = append(sets, "latency_ms = ?")
		args = append(args, latency.Milliseconds())
	}
	if e != nil {
		raw, err := json.Marshal(e)
		if err == nil {
			sets = append(sets, "error_json = ?")
			args = append(args, string(raw))
		}
	}
	args = append(args, id)

	if _, err := s.db.Exec(`UPDATE jobs SET `+joinSet(sets)+` WHERE id = ?`, args...); err != nil {
		return fmt.Errorf("更新任务状态: %w", err)
	}
	return nil
}

// RecordJobImage 记录批量任务中的单张产出。
func (s *Store) RecordJobImage(jobID string, seq int, assetID, status string, e *domain.Error) error {
	var errJSON any
	if e != nil {
		if raw, err := json.Marshal(e); err == nil {
			errJSON = string(raw)
		}
	}
	var asset any
	if assetID != "" {
		asset = assetID
	}
	_, err := s.db.Exec(`
		INSERT INTO job_images(job_id, seq, asset_id, status, error_json)
		VALUES(?,?,?,?,?)
		ON CONFLICT(job_id, seq) DO UPDATE
		  SET asset_id = excluded.asset_id,
		      status   = excluded.status,
		      error_json = excluded.error_json`,
		jobID, seq, asset, status, errJSON)
	if err != nil {
		return fmt.Errorf("记录任务产出: %w", err)
	}
	return nil
}

// GetJob 读取单个任务及其产出。
func (s *Store) GetJob(id string) (*domain.Job, error) {
	var (
		j                                          domain.Job
		paramsJSON, errJSON, startedAt, finishedAt sql.NullString
		createdAt                                  string
	)
	err := s.db.QueryRow(`
		SELECT id, provider, model, mode, prompt, params_json, status, progress,
		       count, latency_ms, error_json, created_at, started_at, finished_at
		FROM jobs WHERE id = ?`, id).Scan(
		&j.ID, &j.Provider, &j.Model, &j.Mode, &j.Prompt, &paramsJSON, &j.Status,
		&j.Progress, &j.Count, &j.LatencyMS, &errJSON, &createdAt, &startedAt, &finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 任务 %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询任务: %w", err)
	}

	_ = json.Unmarshal([]byte(paramsJSON.String), &j.Params)
	if errJSON.Valid && errJSON.String != "" {
		var e domain.Error
		if json.Unmarshal([]byte(errJSON.String), &e) == nil {
			j.Err = &e
		}
	}
	if t := parseTS(sql.NullString{String: createdAt, Valid: createdAt != ""}); t != nil {
		j.CreatedAt = *t
	}
	if t := parseTS(startedAt); t != nil {
		j.StartedAt = t
	}
	if t := parseTS(finishedAt); t != nil {
		j.Finished = t
	}

	images, err := s.listJobImages(id)
	if err != nil {
		return nil, err
	}
	j.Images = images
	return &j, nil
}

func (s *Store) listJobImages(jobID string) ([]domain.JobImage, error) {
	rows, err := s.db.Query(
		`SELECT seq, asset_id, status, error_json FROM job_images WHERE job_id = ? ORDER BY seq`, jobID)
	if err != nil {
		return nil, fmt.Errorf("查询任务产出: %w", err)
	}
	defer rows.Close()

	out := []domain.JobImage{}
	for rows.Next() {
		var (
			img     domain.JobImage
			assetID sql.NullString
			errJSON sql.NullString
		)
		if err := rows.Scan(&img.Seq, &assetID, &img.Status, &errJSON); err != nil {
			return nil, err
		}
		if assetID.Valid {
			img.AssetID = assetID.String
		}
		if errJSON.Valid && errJSON.String != "" {
			var e domain.Error
			if json.Unmarshal([]byte(errJSON.String), &e) == nil {
				img.Err = &e
			}
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

// JobFilter 是任务列表的过滤条件。
type JobFilter struct {
	Provider domain.ProviderID
	Status   domain.JobStatus
	Query    string
	Limit    int
	Offset   int
}

// ListJobs 按条件分页查询任务。
func (s *Store) ListJobs(f JobFilter) ([]domain.Job, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Provider != "" {
		where = append(where, "provider = ?")
		args = append(args, string(f.Provider))
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Query != "" {
		where = append(where, "prompt LIKE ?")
		args = append(args, "%"+f.Query+"%")
	}
	cond := joinWhere(where)

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计任务: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	qArgs := append(append([]any{}, args...), limit, f.Offset)
	rows, err := s.db.Query(`
		SELECT id, provider, model, mode, prompt, status, progress, count, latency_ms, created_at
		FROM jobs WHERE `+cond+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, qArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询任务: %w", err)
	}
	defer rows.Close()

	out := []domain.Job{}
	for rows.Next() {
		var (
			j         domain.Job
			createdAt string
		)
		// created_at 存的是 RFC3339 文本，直接 Scan 进 time.Time 会失败
		// （driver 只认 time.Time 列类型），故先取字符串再解析。
		if err := rows.Scan(&j.ID, &j.Provider, &j.Model, &j.Mode, &j.Prompt,
			&j.Status, &j.Progress, &j.Count, &j.LatencyMS, &createdAt); err != nil {
			return nil, 0, err
		}
		if t := parseTS(sql.NullString{String: createdAt, Valid: createdAt != ""}); t != nil {
			j.CreatedAt = *t
		}
		out = append(out, j)
	}
	return out, total, rows.Err()
}

// CancelJob 尝试取消一个尚未完成的任务。
func (s *Store) CancelJob(id string) error {
	res, err := s.db.Exec(`
		UPDATE jobs SET status = ?, finished_at = ?
		WHERE id = ? AND status IN (?, ?)`,
		string(domain.StatusCanceled), ts(now()), id,
		string(domain.StatusQueued), string(domain.StatusRunning))
	if err != nil {
		return fmt.Errorf("取消任务: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 任务已结束或不存在，交由调用方区分。
		var exists bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM jobs WHERE id = ?)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: 任务 %s", domain.ErrNotFound, id)
		}
	}
	return nil
}

// RecordUsage 累加当日用量统计。
func (s *Store) RecordUsage(provider, model string, ok bool, images int) {
	day := now().Format("2006-01-02")
	okDelta, failDelta := 0, 0
	if ok {
		okDelta = 1
	} else {
		failDelta = 1
	}
	_, _ = s.db.Exec(`
		INSERT INTO usage_daily(day, provider, model, calls, ok, failed, images)
		VALUES(?,?,?,1,?,?,?)
		ON CONFLICT(day, provider, model) DO UPDATE SET
		  calls  = calls + 1,
		  ok     = ok + excluded.ok,
		  failed = failed + excluded.failed,
		  images = images + excluded.images`,
		day, string(provider), model, okDelta, failDelta, images)
}

// UsageStat 是一天的用量统计。
type UsageStat struct {
	Day      string `json:"day"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Calls    int    `json:"calls"`
	OK       int    `json:"ok"`
	Failed   int    `json:"failed"`
	Images   int    `json:"images"`
}

// ListUsage 返回最近 n 天的用量。
func (s *Store) ListUsage(days int) ([]UsageStat, error) {
	if days <= 0 || days > 90 {
		days = 14
	}
	rows, err := s.db.Query(`
		SELECT day, provider, model, calls, ok, failed, images
		FROM usage_daily WHERE day >= date('now', ?) ORDER BY day DESC, provider`,
		fmt.Sprintf("-%d days", days))
	if err != nil {
		return nil, fmt.Errorf("查询用量: %w", err)
	}
	defer rows.Close()

	out := []UsageStat{}
	for rows.Next() {
		var u UsageStat
		if err := rows.Scan(&u.Day, &u.Provider, &u.Model, &u.Calls, &u.OK, &u.Failed, &u.Images); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetKV 写入键值配置。
func (s *Store) SetKV(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO kv(key, value_json, updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
		key, string(raw), ts(now()))
	return err
}

// GetKV 读取键值配置；不存在时返回 def。
func (s *Store) GetKV(key string, def any) (any, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value_json FROM kv WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置 %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(raw), def); err != nil {
		return def, fmt.Errorf("解析配置 %s: %w", key, err)
	}
	return def, nil
}

// joinSet 用逗号连接 SET 子句。
func joinSet(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// joinWhere 用 AND 连接过滤条件。起手放 "1=1" 以便无条件时也是合法 SQL。
func joinWhere(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += " AND "
		}
		out += s
	}
	return out
}
