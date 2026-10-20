package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setenv 设置环境变量并在测试结束后恢复。
// t.Setenv 会禁止并行测试，这里正好不需要并行。
func setenv(t *testing.T, k, v string) {
	t.Helper()
	t.Setenv(k, v)
}

// TestLoadDefaults 零配置时所有值都要有可用兜底。
func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	setenv(t, "IMGEN_DATA", dir)
	for _, k := range []string{
		"IMGEN_ADDR", "IMGEN_CONCURRENCY", "IMGEN_MAX_UPLOAD_MB",
		"IMGEN_MAX_REMOTE_MB", "IMGEN_KEEP_DAYS", "IMGEN_MASTER_KEY",
		"IMGEN_UPSTREAM_TIMEOUT",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", c.Addr)
	}
	if c.Concurrency != 2 {
		t.Errorf("Concurrency = %d, want 2", c.Concurrency)
	}
	if c.MaxUploadMB != 20 {
		t.Errorf("MaxUploadMB = %d, want 20", c.MaxUploadMB)
	}
	if c.MaxRemoteMB != 32 {
		t.Errorf("MaxRemoteMB = %d, want 32", c.MaxRemoteMB)
	}
	// 这条是「清理任务」的默认开关：30 天是 UI 与文档一致的默认值。
	if c.KeepDays != 30 {
		t.Errorf("KeepDays = %d, want 30", c.KeepDays)
	}
	// 派生路径必须跟着 DataDir 走，否则数据会散落两处。
	if c.DBPath != filepath.Join(dir, "imgen.db") {
		t.Errorf("DBPath = %q", c.DBPath)
	}
	if c.BlobDir != filepath.Join(dir, "blobs") {
		t.Errorf("BlobDir = %q", c.BlobDir)
	}
	if c.ThumbDir != filepath.Join(dir, "thumbs") {
		t.Errorf("ThumbDir = %q", c.ThumbDir)
	}
}

// TestLoadKeepDays 保留天数：0 表示关闭清理，必须被保留；
// 负数无意义，收敛到 0。
func TestLoadKeepDays(t *testing.T) {
	dir := t.TempDir()
	setenv(t, "IMGEN_DATA", dir)

	setenv(t, "IMGEN_KEEP_DAYS", "0")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.KeepDays != 0 {
		t.Errorf("KeepDays = %d, want 0（0 是合法值，表示不清理）", c.KeepDays)
	}

	setenv(t, "IMGEN_KEEP_DAYS", "90")
	c, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.KeepDays != 90 {
		t.Errorf("KeepDays = %d, want 90", c.KeepDays)
	}

	setenv(t, "IMGEN_KEEP_DAYS", "-5")
	c, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.KeepDays != 0 {
		t.Errorf("KeepDays = %d, want 0（负数应收敛到 0）", c.KeepDays)
	}
}

// TestLoadClamps 越界值必须被钳制而不是报错。
func TestLoadClamps(t *testing.T) {
	dir := t.TempDir()
	setenv(t, "IMGEN_DATA", dir)
	setenv(t, "IMGEN_CONCURRENCY", "999")
	setenv(t, "IMGEN_MAX_UPLOAD_MB", "0")
	setenv(t, "IMGEN_MAX_REMOTE_MB", "0")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Concurrency != 16 {
		t.Errorf("Concurrency = %d, want 16（上限）", c.Concurrency)
	}
	if c.MaxUploadMB != 20 {
		t.Errorf("MaxUploadMB = %d, want 20（回退默认）", c.MaxUploadMB)
	}
	if c.MaxRemoteMB != 32 {
		t.Errorf("MaxRemoteMB = %d, want 32（回退默认）", c.MaxRemoteMB)
	}

	setenv(t, "IMGEN_CONCURRENCY", "0")
	c, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Concurrency != 1 {
		t.Errorf("Concurrency = %d, want 1（下界）", c.Concurrency)
	}
}

// TestLoadRejectsTinyTimeout 上游超时过小属于配置错误，应报错而非静默降级。
func TestLoadRejectsTinyTimeout(t *testing.T) {
	dir := t.TempDir()
	setenv(t, "IMGEN_DATA", dir)
	setenv(t, "IMGEN_UPSTREAM_TIMEOUT", "1s")

	if _, err := Load(); err == nil {
		t.Fatal("超时 1s 应被拒绝")
	}
}

// TestLoadUpstreamTimeout 合法超时要被解析。
func TestLoadUpstreamTimeout(t *testing.T) {
	dir := t.TempDir()
	setenv(t, "IMGEN_DATA", dir)
	setenv(t, "IMGEN_UPSTREAM_TIMEOUT", "90s")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.UpstreamTimeout != 90*time.Second {
		t.Errorf("UpstreamTimeout = %v, want 90s", c.UpstreamTimeout)
	}
}

// TestEnsureDirs 三个目录都要被创建，且可重复调用。
func TestEnsureDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	c := Config{
		DataDir:  dir,
		BlobDir:  filepath.Join(dir, "blobs"),
		ThumbDir: filepath.Join(dir, "thumbs"),
	}
	if err := c.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{c.DataDir, c.BlobDir, c.ThumbDir} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Errorf("目录 %s 未创建: %v", d, err)
		}
	}
	// 第二次调用必须幂等。
	if err := c.EnsureDirs(); err != nil {
		t.Errorf("重复 EnsureDirs 失败: %v", err)
	}
}
