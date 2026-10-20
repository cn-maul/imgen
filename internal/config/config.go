package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Config 汇总所有运行期配置。
// 除 DataDir 外均有零值兜底，便于零配置启动。
type Config struct {
	Addr     string
	DataDir  string
	DBPath   string
	BlobDir  string
	ThumbDir string

	// 上游请求超时，由 Provider 能力声明进一步覆盖。
	UpstreamTimeout time.Duration

	// 全局并发上限：防止打爆上游限流与本机内存。
	Concurrency int

	// AES-GCM 主密钥（base64）。为空时自动生成并落盘到 DataDir。
	MasterKey string

	MaxUploadMB int
	MaxRemoteMB int
	// KeepDays 是历史保留天数，由 internal/janitor 执行清理；0 表示不清理。
	KeepDays int
}

func Load() (Config, error) {
	c := Config{
		Addr:            env("IMGEN_ADDR", ":8080"),
		DataDir:         env("IMGEN_DATA", "./data"),
		UpstreamTimeout: envDuration("IMGEN_UPSTREAM_TIMEOUT", 180*time.Second),
		Concurrency:     envInt("IMGEN_CONCURRENCY", 2),
		MasterKey:       os.Getenv("IMGEN_MASTER_KEY"),
		MaxUploadMB:     envInt("IMGEN_MAX_UPLOAD_MB", 20),
		MaxRemoteMB:     envInt("IMGEN_MAX_REMOTE_MB", 32),
		KeepDays:        envInt("IMGEN_KEEP_DAYS", 30),
	}
	c.DBPath = filepath.Join(c.DataDir, "imgen.db")
	c.BlobDir = filepath.Join(c.DataDir, "blobs")
	c.ThumbDir = filepath.Join(c.DataDir, "thumbs")

	if c.Concurrency < 1 {
		c.Concurrency = 1
	}
	if c.Concurrency > 16 {
		c.Concurrency = 16
	}
	if c.MaxUploadMB < 1 {
		c.MaxUploadMB = 20
	}
	if c.MaxRemoteMB < 1 {
		c.MaxRemoteMB = 32
	}
	// 保留天数不设上限（0 = 不清理），但负数无意义，统一收敛到 0。
	if c.KeepDays < 0 {
		c.KeepDays = 0
	}
	if c.UpstreamTimeout < 10*time.Second {
		return Config{}, fmt.Errorf("IMGEN_UPSTREAM_TIMEOUT 过小")
	}
	return c, nil
}

// EnsureDirs 创建所有运行时目录。
func (c Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.BlobDir, c.ThumbDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
