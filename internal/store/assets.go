package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/imgen/imgen/internal/domain"
)

// UpsertAsset 以 sha256 去重写入资产；已存在时返回既有记录与 created=false。
func (s *Store) UpsertAsset(a domain.Asset) (*domain.Asset, bool, error) {
	// 内容寻址：同图只存一份。
	existing, err := s.getAssetBySHA(a.SHA256)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("查询资产: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO assets(id, sha256, mime, ext, width, height, bytes, path, source, source_url, expires_at, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.SHA256, a.MIME, a.Ext, a.Width, a.Height, a.Bytes, a.Path,
		a.Source, nullStr(a.SourceURL), nullTS(a.ExpiresAt), ts(now()))
	if err != nil {
		// 并发插入时可能撞唯一约束，回退为读取既有记录。
		if isUnique(err) {
			if got, _, err2 := s.UpsertAsset(a); err2 == nil {
				return got, false, nil
			}
		}
		return nil, false, fmt.Errorf("写入资产: %w", err)
	}
	a.CreatedAt = now()
	return &a, true, nil
}

// assetColumns 是资产查询的公共列序，Scan 顺序与之对应。
const assetColumns = `id, sha256, mime, ext, width, height, bytes, path, source, source_url, expires_at, created_at`

func scanAsset(sc interface{ Scan(...any) error }) (*domain.Asset, error) {
	var (
		a                             domain.Asset
		sourceURL, expiresAt, created sql.NullString
	)
	if err := sc.Scan(&a.ID, &a.SHA256, &a.MIME, &a.Ext, &a.Width, &a.Height,
		&a.Bytes, &a.Path, &a.Source, &sourceURL, &expiresAt, &created); err != nil {
		return nil, err
	}
	a.SourceURL = sourceURL.String
	if t := parseTS(expiresAt); t != nil {
		a.ExpiresAt = t
	}
	if t := parseTS(created); t != nil {
		a.CreatedAt = *t
	}
	return &a, nil
}

func (s *Store) getAssetBySHA(sha string) (*domain.Asset, error) {
	return scanAsset(s.db.QueryRow(
		`SELECT `+assetColumns+` FROM assets WHERE sha256 = ?`, sha))
}

// GetAsset 按 id 读取资产。
func (s *Store) GetAsset(id string) (*domain.Asset, error) {
	a, err := scanAsset(s.db.QueryRow(
		`SELECT `+assetColumns+` FROM assets WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 资产 %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询资产: %w", err)
	}
	return a, nil
}

// AssetFilter 是资产库查询条件。
type AssetFilter struct {
	Source string
	Query  string
	Limit  int
	Offset int
}

// ListAssets 分页查询资产库。
func (s *Store) ListAssets(f AssetFilter) ([]domain.Asset, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}
	if f.Query != "" {
		where = append(where, "(source_url LIKE ? OR id LIKE ?)")
		args = append(args, "%"+f.Query+"%", "%"+f.Query+"%")
	}
	cond := joinWhere(where)

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计资产: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	qArgs := append(append([]any{}, args...), limit, f.Offset)
	rows, err := s.db.Query(`
		SELECT `+assetColumns+` FROM assets WHERE `+cond+`
		ORDER BY created_at DESC LIMIT ? OFFSET ?`, qArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询资产: %w", err)
	}
	defer rows.Close()

	out := []domain.Asset{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *a)
	}
	return out, total, rows.Err()
}

// DeleteAsset 删除资产记录；磁盘文件由 blob 层清理。
func (s *Store) DeleteAsset(id string) error {
	res, err := s.db.Exec(`DELETE FROM assets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除资产: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: 资产 %s", domain.ErrNotFound, id)
	}
	return nil
}

// AssetPath 返回资产在磁盘上的相对路径。
func (s *Store) AssetPath(id string) (string, error) {
	var p string
	err := s.db.QueryRow(`SELECT path FROM assets WHERE id = ?`, id).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: 资产 %s", domain.ErrNotFound, id)
	}
	return p, err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func isUnique(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint") || strings.Contains(msg, "constraint failed")
}
