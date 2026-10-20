// Package store 提供 SQLite 持久化。使用纯 Go 驱动，无 CGO。
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store 封装数据库句柄。
type Store struct {
	db *sql.DB
}

type migration struct {
	name string
	sql  string
}

// Open 打开（必要时创建）数据库并执行内嵌迁移。
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	// SQLite 单写者模型，限制连接数可避免写锁竞争。
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层句柄。
func (s *Store) Close() error { return s.db.Close() }

// DB 暴露原始句柄，供确需直接查询的场景使用。
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("建迁移表: %w", err)
	}

	applied := map[string]bool{}
	rows, err := s.db.Query(`SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("读迁移记录: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		applied[n] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("读迁移目录: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if applied[e.Name()] {
			continue
		}
		raw, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(raw)) {
			if _, err := s.db.Exec(stmt); err != nil {
				return fmt.Errorf("执行迁移 %s 失败: %w\n---\n%s", e.Name(), err, truncate(stmt, 300))
			}
		}
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations(name, applied_at) VALUES(?,?)`,
			e.Name(), now().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("记录迁移 %s: %w", e.Name(), err)
		}
	}
	return nil
}

// splitStatements 按分号切分 SQL，跳过注释行。
// 迁移文件不使用存储过程，故该简化是安全的。
func splitStatements(src string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(t, ";") {
			if s := strings.TrimSpace(cur.String()); s != "" && s != ";" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func now() time.Time { return time.Now().UTC() }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTS(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s.String); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}

func nullTS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
