package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/imgen/imgen/internal/domain"
)

// PromptPreset 是一条可复用的提示词预设。
//
// Body 是模板自身的字段（原始提示词文本、参考图引用、尺寸偏好等），
// 以 JSON 存 text 列：字段会随前端演进，硬拆成列会反复改表结构。
// 这与 jobs.params_json 是刻意分开的两套东西 —— 预设是可编辑的草稿，
// job 参数是不可变的历史快照。
type PromptPreset struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Body        json.RawMessage `json:"body"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}

const presetColumns = `id, name, category, description, body_json, created_at, updated_at`

func scanPreset(sc interface{ Scan(...any) error }) (*PromptPreset, error) {
	var (
		p                    PromptPreset
		body                 []byte
		createdAt, updatedAt string
	)
	if err := sc.Scan(&p.ID, &p.Name, &p.Category, &p.Description,
		&body, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	p.Body = json.RawMessage(body)
	p.CreatedAt = createdAt
	p.UpdatedAt = updatedAt
	return &p, nil
}

// ListPresets 按更新时间倒序返回全部预设。
func (s *Store) ListPresets() ([]PromptPreset, error) {
	rows, err := s.db.Query(
		`SELECT ` + presetColumns + ` FROM prompt_presets ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("查询提示词预设: %w", err)
	}
	defer rows.Close()

	out := []PromptPreset{}
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetPreset 按 id 取单条预设。
func (s *Store) GetPreset(id string) (*PromptPreset, error) {
	row := s.db.QueryRow(`SELECT `+presetColumns+` FROM prompt_presets WHERE id = ?`, id)
	p, err := scanPreset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 预设 %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// CreatePreset 新建一条预设；id 为空时自动生成。
func (s *Store) CreatePreset(p PromptPreset) (*PromptPreset, error) {
	if p.ID == "" {
		id, err := newPresetID()
		if err != nil {
			return nil, err
		}
		p.ID = id
	}
	p.normalize()
	if len(p.Body) == 0 {
		p.Body = json.RawMessage(`{}`)
	}
	now := ts(now())
	_, err := s.db.Exec(`
		INSERT INTO prompt_presets(id, name, category, description, body_json, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Category, p.Description, string(p.Body), now, now)
	if err != nil {
		return nil, fmt.Errorf("写入预设: %w", err)
	}
	p.CreatedAt, p.UpdatedAt = now, now
	return &p, nil
}

// UpdatePreset 覆盖一条预设的可变字段。
//
// 采用整条覆盖而非 PATCH 语义：预设字段少且总是整体编辑，
// 部分更新只会让「哪些字段是必填」变模糊。updated_at 由本层维护。
func (s *Store) UpdatePreset(id string, p PromptPreset) (*PromptPreset, error) {
	p.normalize()
	if len(p.Body) == 0 {
		p.Body = json.RawMessage(`{}`)
	}
	now := ts(now())
	res, err := s.db.Exec(`
		UPDATE prompt_presets
		SET name = ?, category = ?, description = ?, body_json = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.Category, p.Description, string(p.Body), now, id)
	if err != nil {
		return nil, fmt.Errorf("更新预设: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("%w: 预设 %s", domain.ErrNotFound, id)
	}
	p.ID = id
	p.UpdatedAt = now
	return &p, nil
}

// DeletePreset 删除一条预设。
func (s *Store) DeletePreset(id string) error {
	res, err := s.db.Exec(`DELETE FROM prompt_presets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除预设: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: 预设 %s", domain.ErrNotFound, id)
	}
	return nil
}

func newPresetID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成预设 id: %w", err)
	}
	return "p_" + hex.EncodeToString(b[:]), nil
}

// normalize 收敛用户可见字段的空白，避免 UI 出现只有空格的条目。
//
// 放在存储层而非只在 HTTP 层：两条写入路径（API 与未来的内部调用）
// 必须有同一套行为，否则绕过 API 的写入会留下带空白的名字。
func (p *PromptPreset) normalize() {
	p.Name = strings.TrimSpace(p.Name)
	p.Category = strings.TrimSpace(p.Category)
	p.Description = strings.TrimSpace(p.Description)
}

// NormalizePresetName 导出给 HTTP 层做校验，避免空名条目被写入。
func NormalizePresetName(s string) string { return strings.TrimSpace(s) }
