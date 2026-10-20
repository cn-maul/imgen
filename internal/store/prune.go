package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/imgen/imgen/internal/domain"
)

// retentionTimeSQL 是「任务到期判定依据」：终态任务按结束时间算保留期，
// 没有结束时间（历史数据 / 异常退出）时退回创建时间，避免永远删不掉。
//
// 用 COALESCE 而不是只比 created_at：用户的心智模型是「跑完的任务保留 N 天」，
// 一个 30 天前创建、昨天才跑完的长任务不应该立刻被清掉。
const retentionTimeSQL = `COALESCE(finished_at, created_at)`

// terminalStatuses 是可被清理的任务状态。排队中 / 运行中的任务绝不能删。
var terminalStatuses = []string{
	string(domain.StatusDone),
	string(domain.StatusPartial),
	string(domain.StatusFailed),
	string(domain.StatusCanceled),
}

// PruneTerminalJobs 删除到期判定早于 cutoff 的终态任务，返回删除条数。
//
// 时间比较刻意放在 Go 侧而不是 SQL：created_at / finished_at 存的是
// time.RFC3339Nano 文本，而 RFC3339Nano 会裁掉小数秒的尾随零
// （.4 秒写成 ".4Z"、.45 秒写成 ".45Z"）。按字符串比较时 ".4Z" > ".45Z"，
// 与时间先后相反，会把该删的漏掉、该留的删掉。
//
// job_images 由外键 ON DELETE CASCADE 随任务一并删除。
func (s *Store) PruneTerminalJobs(cutoff time.Time) (int, error) {
	args := make([]any, len(terminalStatuses))
	for i, st := range terminalStatuses {
		args[i] = st
	}
	rows, err := s.db.Query(`
		SELECT id, `+retentionTimeSQL+`
		FROM jobs
		WHERE status IN (?,?,?,?)`, args...)
	if err != nil {
		return 0, fmt.Errorf("查询待清理任务: %w", err)
	}

	var ids []string
	for rows.Next() {
		var (
			id  string
			ref sql.NullString
		)
		if err := rows.Scan(&id, &ref); err != nil {
			rows.Close()
			return 0, fmt.Errorf("扫描待清理任务: %w", err)
		}
		t := parseTS(ref)
		if t != nil && t.Before(cutoff) {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("遍历待清理任务: %w", err)
	}
	// 必须先关 rows 再开事务：SetMaxOpenConns(1) 下，未关闭的游标会占住唯一连接。
	rows.Close()

	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("开启清理事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM jobs WHERE id = ?`, id); err != nil {
			return 0, fmt.Errorf("删除任务 %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交清理事务: %w", err)
	}
	return len(ids), nil
}

// PruneUnreferencedAssets 删除创建时间早于 cutoff 且无人引用的资产。
//
// 「有人引用」的判定覆盖三处（缺一处就会误删仍在用的图）：
//   - job_images.asset_id：某个任务的产出；
//   - jobs.params_json 里的 refs[].assetId：某个任务拿它当过参考图；
//   - prompt_presets.body_json 里的 refs[].assetId：某个预设绑定了它。
//
// 返回被删除的资产记录（含磁盘相对路径），供上层删除原图与缩略图 ——
// store 层不碰文件系统。
//
// 注意调用顺序：应先 PruneTerminalJobs 再调本函数。旧任务被删后，
// 它们对资产的引用随之消失，那些资产才应该在下一轮被判为孤儿。
func (s *Store) PruneUnreferencedAssets(cutoff time.Time) ([]domain.Asset, error) {
	referenced, err := s.referencedAssetIDs()
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`SELECT ` + assetColumns + ` FROM assets`)
	if err != nil {
		return nil, fmt.Errorf("查询待清理资产: %w", err)
	}
	var victims []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("扫描待清理资产: %w", err)
		}
		if a.CreatedAt.Before(cutoff) {
			if _, used := referenced[a.ID]; !used {
				victims = append(victims, *a)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("遍历待清理资产: %w", err)
	}
	rows.Close()

	if len(victims) == 0 {
		return nil, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("开启清理事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, a := range victims {
		if _, err := tx.Exec(`DELETE FROM assets WHERE id = ?`, a.ID); err != nil {
			return nil, fmt.Errorf("删除资产 %s: %w", a.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交清理事务: %w", err)
	}
	return victims, nil
}

// referencedAssetIDs 汇总当前仍被引用的资产 id。
func (s *Store) referencedAssetIDs() (map[string]struct{}, error) {
	out := map[string]struct{}{}

	// ① 任务产出。
	if err := s.collectStrings(
		`SELECT asset_id FROM job_images WHERE asset_id IS NOT NULL AND asset_id <> ''`,
		out); err != nil {
		return nil, fmt.Errorf("收集任务产出引用: %w", err)
	}

	// ② 任务参数里的参考图。
	jobRefs, err := s.collectJSONRefs(`SELECT params_json FROM jobs`)
	if err != nil {
		return nil, fmt.Errorf("收集任务参数引用: %w", err)
	}
	// ③ 预设绑定的参考图。
	presetRefs, err := s.collectJSONRefs(`SELECT body_json FROM prompt_presets`)
	if err != nil {
		return nil, fmt.Errorf("收集预设引用: %w", err)
	}
	for id := range jobRefs {
		out[id] = struct{}{}
	}
	for id := range presetRefs {
		out[id] = struct{}{}
	}
	return out, nil
}

// collectStrings 把单列查询结果并入集合。
func (s *Store) collectStrings(query string, dst map[string]struct{}) error {
	rows, err := s.db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return err
		}
		if v != "" {
			dst[v] = struct{}{}
		}
	}
	return rows.Err()
}

// refCarrier 是 jobs.params_json 与 prompt_presets.body_json 的共同形状：
// 两者都把参考图放在顶层 refs 数组里，元素含 assetId。
type refCarrier struct {
	Refs []struct {
		AssetID string `json:"assetId"`
	} `json:"refs"`
}

// collectJSONRefs 从一列 JSON 文本里抽取 refs[].assetId。
// 解析失败的行直接跳过：清理任务是尽力而为的后台工作，
// 不该因为一条脏数据就整体失败（脏数据由各自的写入路径负责校验）。
func (s *Store) collectJSONRefs(query string) (map[string]struct{}, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]struct{}{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c refCarrier
		if err := json.Unmarshal(raw, &c); err != nil {
			continue
		}
		for _, r := range c.Refs {
			if r.AssetID != "" {
				out[r.AssetID] = struct{}{}
			}
		}
	}
	return out, rows.Err()
}
