package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/imgen/imgen/internal/domain"
)

// Provider 是持久化的通道配置。
type Provider struct {
	ID      domain.ProviderID `json:"id"`
	Name    string            `json:"name"`
	BaseURL string            `json:"baseUrl"`
	Enabled bool              `json:"enabled"`
	Timeout time.Duration     `json:"-"`
	// HasKey 指示是否已配置 API Key（密文永不回传前端）。
	HasKey    bool      `json:"hasKey"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListProviders 返回全部通道配置。
func (s *Store) ListProviders() ([]Provider, error) {
	rows, err := s.db.Query(`
		SELECT id, name, base_url, api_key_enc, enabled, timeout_ms, created_at
		FROM providers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询通道: %w", err)
	}
	defer rows.Close()

	var out []Provider
	for rows.Next() {
		var (
			p         Provider
			enc       []byte
			timeoutMS int64
			created   string
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &enc, &p.Enabled, &timeoutMS, &created); err != nil {
			return nil, fmt.Errorf("扫描通道: %w", err)
		}
		p.Timeout = time.Duration(timeoutMS) * time.Millisecond
		p.HasKey = len(enc) > 0
		if t := parseTS(sql.NullString{String: created, Valid: created != ""}); t != nil {
			p.CreatedAt = *t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProviderConfig 是写回通道时的可变字段；nil 字段表示不修改。
type ProviderConfig struct {
	BaseURL   string `json:"baseUrl"`
	APIKey    []byte // 已加密的密文
	ClearKey  bool   `json:"clearKey"`
	Enabled   *bool  `json:"enabled"`
	TimeoutMS *int   `json:"timeoutMs"`
}

// UpdateProvider 更新通道配置。
func (s *Store) UpdateProvider(id domain.ProviderID, c ProviderConfig) error {
	sets := make([]string, 0, 5)
	args := make([]any, 0, 6)

	if c.BaseURL != "" {
		sets = append(sets, "base_url = ?")
		args = append(args, strings.TrimRight(c.BaseURL, "/"))
	}
	switch {
	case c.ClearKey:
		sets = append(sets, "api_key_enc = NULL")
	case len(c.APIKey) > 0:
		sets = append(sets, "api_key_enc = ?")
		args = append(args, c.APIKey)
	}
	if c.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, boolToInt(*c.Enabled))
	}
	if c.TimeoutMS != nil {
		sets = append(sets, "timeout_ms = ?")
		args = append(args, *c.TimeoutMS)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, ts(now()), string(id))

	res, err := s.db.Exec(`UPDATE providers SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("更新通道: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: 通道 %s", domain.ErrNotFound, id)
	}
	return nil
}

// GetProviderKey 返回某通道的 API Key 密文，由 secret 层解密。
func (s *Store) GetProviderKey(id domain.ProviderID) ([]byte, error) {
	var enc []byte
	err := s.db.QueryRow(`SELECT api_key_enc FROM providers WHERE id = ?`, string(id)).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 通道 %s", domain.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("读取通道密钥: %w", err)
	}
	if len(enc) == 0 {
		return nil, fmt.Errorf("%w: 通道 %s 尚未配置 API Key", domain.ErrNoCredential, id)
	}
	return enc, nil
}

// ListModels 返回某通道的模型表。
func (s *Store) ListModels(providerID domain.ProviderID) ([]domain.Model, error) {
	rows, err := s.db.Query(
		`SELECT model_id, display_name FROM models WHERE provider_id = ? ORDER BY model_id`, string(providerID))
	if err != nil {
		return nil, fmt.Errorf("查询模型: %w", err)
	}
	defer rows.Close()

	out := []domain.Model{}
	for rows.Next() {
		var m domain.Model
		if err := rows.Scan(&m.ID, &m.Name); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
