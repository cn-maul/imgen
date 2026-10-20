package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imgen/imgen/internal/domain"
)

// newTestStore 建一个临时目录上的 blob.Store。
// maxRemoteMB 给 1，各用例按需直接改 MaxBytes，避免造大图拖慢测试。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return NewStore(filepath.Join(dir, "blobs"), filepath.Join(dir, "thumbs"), 1)
}

// placeBlob 按内容寻址规则直接把字节放到 blob 目录，返回相对路径。
//
// 不用 Put 是为了避开 Put 内部 `go s.ensureThumb(...)` 的异步生成：
// 直接调用 ensureThumb 会与它争抢同一个 .tmp 文件（见报告中的已知问题），
// 让断言变得不确定。需要同步生成缩略图的用例走这条路。
func placeBlob(t *testing.T, s *Store, raw []byte, ext string) string {
	t.Helper()
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])
	rel := filepath.Join(sha[:2], sha[2:4], sha+ext)
	abs := filepath.Join(s.BlobDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	if err := os.WriteFile(abs, raw, 0o640); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	return filepath.ToSlash(rel)
}

// waitFile 轮询等待文件出现，避免测试里写固定 sleep。
func waitFile(t *testing.T, path string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x * y), A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("编码 PNG: %v", err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x + y), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("编码 JPEG: %v", err)
	}
	return buf.Bytes()
}

// TestPutSameImageTwice 内容寻址：同一张图两次 Put 返回相同 sha，且第二次不重复写盘。
func TestPutSameImageTwice(t *testing.T) {
	s := newTestStore(t)
	raw := pngBytes(t, 8, 8)

	a1, err := s.Put(raw, "upload", "", "")
	if err != nil {
		t.Fatalf("首次 Put: %v", err)
	}
	abs := s.Path(a1.Path)
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("首次 Put 未落盘: %v", err)
	}
	// 等 Put 的异步缩略图协程结束，避免与下面的哨兵写入抢同一文件句柄。
	waitFile(t, filepath.Join(s.ThumbDir, filepath.FromSlash(s.ThumbRel(a1.Path))), 5*time.Second)

	// 把文件内容改成哨兵值：若第二次 Put 又写一遍，哨兵就会被覆盖掉。
	const marker = "SENTINEL-NOT-REWRITTEN"
	if err := os.WriteFile(abs, []byte(marker), 0o640); err != nil {
		t.Fatalf("写哨兵: %v", err)
	}

	a2, err := s.Put(raw, "upload", "", "")
	if err != nil {
		t.Fatalf("第二次 Put: %v", err)
	}
	if a2.SHA256 != a1.SHA256 {
		t.Errorf("sha 不一致: %s != %s", a2.SHA256, a1.SHA256)
	}
	if a2.ID != a1.ID || a2.Path != a1.Path {
		t.Errorf("ID/Path 不一致: %s/%s vs %s/%s", a2.ID, a2.Path, a1.ID, a1.Path)
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("读回文件: %v", err)
	}
	if string(got) != marker {
		t.Errorf("第二次 Put 重复写盘了，文件内容 = %q", got)
	}
}

// TestPutMetadata 校验 Put 产出的资产元数据（尺寸、MIME、扩展名、来源）。
func TestPutMetadata(t *testing.T) {
	s := newTestStore(t)
	raw := pngBytes(t, 12, 7)

	pngAsset, err := s.Put(raw, "generated", "https://up.example/a.png", "")
	if err != nil {
		t.Fatalf("Put png: %v", err)
	}
	if pngAsset.Width != 12 || pngAsset.Height != 7 {
		t.Errorf("尺寸 = %dx%d, want 12x7", pngAsset.Width, pngAsset.Height)
	}
	if pngAsset.MIME != "image/png" || pngAsset.Ext != ".png" {
		t.Errorf("png mime/ext = %q/%q", pngAsset.MIME, pngAsset.Ext)
	}
	if pngAsset.Bytes != int64(len(raw)) {
		t.Errorf("字节数 = %d, want %d", pngAsset.Bytes, len(raw))
	}
	if pngAsset.Source != "generated" || pngAsset.SourceURL != "https://up.example/a.png" {
		t.Errorf("来源 = %q/%q", pngAsset.Source, pngAsset.SourceURL)
	}
	if !strings.HasPrefix(pngAsset.ID, "a_") || len(pngAsset.ID) != 18 {
		t.Errorf("ID 形态异常: %q", pngAsset.ID)
	}
	if !strings.HasSuffix(pngAsset.Path, ".png") || strings.Contains(pngAsset.Path, "\\") {
		t.Errorf("Path 应为 slash 形式且带扩展名: %q", pngAsset.Path)
	}
	// sha 前缀两级的分片目录：aa/bb/aabb....png
	if got := pngAsset.Path; got[:2]+"/"+got[3:5] != pngAsset.SHA256[:2]+"/"+pngAsset.SHA256[2:4] {
		t.Errorf("Path 分片与 sha 不符: %q vs %q", got, pngAsset.SHA256)
	}

	jpg, err := s.Put(jpegBytes(t, 9, 5), "upload", "", "jpeg")
	if err != nil {
		t.Fatalf("Put jpeg: %v", err)
	}
	if jpg.MIME != "image/jpeg" || jpg.Ext != ".jpg" {
		t.Errorf("jpeg mime/ext = %q/%q", jpg.MIME, jpg.Ext)
	}
}

// TestPutRejectsCorruptBytes 损坏或非图片的字节必须报错，且不落盘。
func TestPutRejectsCorruptBytes(t *testing.T) {
	s := newTestStore(t)
	cases := map[string][]byte{
		"空字节":        {},
		"纯文本":        []byte("this is definitely not an image"),
		"截断的 PNG":    append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...),
		"HTML 错误页":   []byte("<html><body>502 Bad Gateway</body></html>"),
		"JSON 错误体":   []byte(`{"error":{"message":"invalid"}}`),
		"base64 未解码": []byte("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4"),
	}
	for name, raw := range cases {
		if _, err := s.Put(raw, "upload", "", ""); err == nil {
			t.Errorf("%s：应当报错", name)
		}
	}
	// 失败路径不应留下任何文件。
	entries, err := os.ReadDir(s.BlobDir)
	if err == nil && len(entries) != 0 {
		t.Errorf("失败 Put 不应落盘，目录内却有 %d 项", len(entries))
	}
}

// TestPutRejectsOversize 覆盖 Put 的体积分支（DecodeConfig 成功之后才检查大小）。
func TestPutRejectsOversize(t *testing.T) {
	s := newTestStore(t)
	s.MaxBytes = 64 // 远小于一张 8x8 PNG

	if _, err := s.Put(pngBytes(t, 8, 8), "upload", "", ""); err == nil {
		t.Fatal("超过 MaxBytes 应当报错")
	} else if !strings.Contains(err.Error(), "超过上限") {
		t.Errorf("错误信息应提示超限: %v", err)
	}
}

// TestPutReaderLimit 覆盖 PutReader 自带的 limit 分支。
func TestPutReaderLimit(t *testing.T) {
	raw := pngBytes(t, 8, 8)

	t.Run("显式 limit 生效", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.PutReader(bytes.NewReader(raw), "upload", "", 10); err == nil {
			t.Fatal("内容超过 limit 应当报错")
		} else if !strings.Contains(err.Error(), "超过上限") {
			t.Errorf("错误信息应提示超限: %v", err)
		}
	})

	t.Run("limit 为 0 时回落到 MaxBytes", func(t *testing.T) {
		s := newTestStore(t) // MaxBytes = 1MB
		a, err := s.PutReader(bytes.NewReader(raw), "upload", "", 0)
		if err != nil {
			t.Fatalf("PutReader: %v", err)
		}
		if a.SHA256 == "" || a.Bytes != int64(len(raw)) {
			t.Errorf("资产 = %+v", a)
		}
	})

	t.Run("刚好等于 limit 放行", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.PutReader(bytes.NewReader(raw), "upload", "", int64(len(raw))); err != nil {
			t.Errorf("等于上限应当放行: %v", err)
		}
	})

	t.Run("读取失败向上传播", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.PutReader(errReader{}, "upload", "", 1024); err == nil {
			t.Error("底层读取错误应当上报")
		}
	})

	t.Run("损坏内容同样报错", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.PutReader(strings.NewReader("not an image"), "upload", "", 1024); err == nil {
			t.Error("非图片内容应当报错")
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestDataURIRoundTrip DataURI 前缀正确且 base64 能解回原字节。
func TestDataURIRoundTrip(t *testing.T) {
	s := newTestStore(t)
	raw := pngBytes(t, 6, 6)
	rel := placeBlob(t, s, raw, ".png")

	uri, err := s.DataURI(rel)
	if err != nil {
		t.Fatalf("DataURI: %v", err)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("前缀错误: %q", uri[:min(len(uri), 40)])
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatalf("解 base64: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Errorf("往返后字节不一致：%d vs %d", len(decoded), len(raw))
	}
}

// TestDataURIMimeByExt 不同扩展名走不同 MIME。
func TestDataURIMimeByExt(t *testing.T) {
	s := newTestStore(t)
	rel := placeBlob(t, s, jpegBytes(t, 8, 8), ".jpg")
	uri, err := s.DataURI(rel)
	if err != nil {
		t.Fatalf("DataURI: %v", err)
	}
	if !strings.HasPrefix(uri, "data:image/jpeg;base64,") {
		t.Errorf("jpeg 前缀错误: %q", uri[:min(len(uri), 40)])
	}
}

// TestDataURIErrors 文件不存在与超限两种失败。
func TestDataURIErrors(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.DataURI("00/11/does-not-exist.png"); err == nil {
		t.Error("文件不存在应当报错")
	}

	rel := placeBlob(t, s, pngBytes(t, 4, 4), ".png")
	s.MaxBytes = 8
	if _, err := s.DataURI(rel); err == nil {
		t.Error("超过 MaxBytes 应当报错")
	}
}

// TestThumbRel 缩略图相对路径规则。
func TestThumbRel(t *testing.T) {
	s := newTestStore(t)
	cases := map[string]string{
		"ab/cd/abcdef.png":  "ab/cd/abcdef.thumb.jpg",
		"ab/cd/abcdef.jpg":  "ab/cd/abcdef.thumb.jpg",
		"ab/cd/abcdef.webp": "ab/cd/abcdef.thumb.jpg",
		"abcdef.png":        "abcdef.thumb.jpg",
	}
	for in, want := range cases {
		if got := s.ThumbRel(in); got != want {
			t.Errorf("ThumbRel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestThumbPathFallsBackToOriginal 缩略图未生成时 ThumbPath 回退到原图。
func TestThumbPathFallsBackToOriginal(t *testing.T) {
	s := newTestStore(t)
	rel := "de/ad/beef.png"
	if got, want := s.ThumbPath(rel), s.Path(rel); got != want {
		t.Errorf("ThumbPath = %q, want 回退到 %q", got, want)
	}
}

// TestThumbPathUsesThumbWhenPresent 缩略图生成后 ThumbPath 指向缩略图。
func TestThumbPathUsesThumbWhenPresent(t *testing.T) {
	s := newTestStore(t)
	raw := pngBytes(t, 600, 200)
	rel := placeBlob(t, s, raw, ".png")
	s.ensureThumb(s.Path(rel), "", rel)

	want := filepath.Join(s.ThumbDir, filepath.FromSlash(s.ThumbRel(rel)))
	if got := s.ThumbPath(rel); got != want {
		t.Errorf("ThumbPath = %q, want %q", got, want)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("缩略图未生成: %v", err)
	}
	if info.Size() == 0 {
		t.Error("缩略图为空文件")
	}
}

// TestEnsureThumbScalesLongEdge 长边应被缩到 ThumbMax 以内，且是合法 JPEG。
func TestEnsureThumbScalesLongEdge(t *testing.T) {
	s := newTestStore(t)
	rel := placeBlob(t, s, pngBytes(t, 600, 200), ".png")
	s.ensureThumb(s.Path(rel), "", rel)

	cfg := decodeThumb(t, s, rel)
	if cfg.Width != 512 {
		t.Errorf("缩略图宽 = %d, want 512（长边钳到 ThumbMax）", cfg.Width)
	}
	if cfg.Height > 512 {
		t.Errorf("缩略图高 = %d 超过 ThumbMax", cfg.Height)
	}
}

// TestEnsureThumbKeepsSmallImage 小图不应被放大。
func TestEnsureThumbKeepsSmallImage(t *testing.T) {
	s := newTestStore(t)
	rel := placeBlob(t, s, pngBytes(t, 8, 4), ".png")
	s.ensureThumb(s.Path(rel), "", rel)

	cfg := decodeThumb(t, s, rel)
	if cfg.Width != 8 || cfg.Height != 4 {
		t.Errorf("小图被放大了: %dx%d, want 8x4", cfg.Width, cfg.Height)
	}
}

func decodeThumb(t *testing.T, s *Store, rel string) image.Config {
	t.Helper()
	f, err := os.Open(filepath.Join(s.ThumbDir, filepath.FromSlash(s.ThumbRel(rel))))
	if err != nil {
		t.Fatalf("缩略图未生成: %v", err)
	}
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		t.Fatalf("缩略图不是合法 JPEG: %v", err)
	}
	return cfg
}

// TestPutSpawnsAsyncThumb Put 返回后缩略图应在后台很快出现（异步生成路径）。
func TestPutSpawnsAsyncThumb(t *testing.T) {
	s := newTestStore(t)
	a, err := s.Put(pngBytes(t, 64, 64), "generated", "", "")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	thumb := filepath.Join(s.ThumbDir, filepath.FromSlash(s.ThumbRel(a.Path)))
	if !waitFile(t, thumb, 5*time.Second) {
		t.Fatal("Put 的异步缩略图未生成")
	}
	if got := s.ThumbPath(a.Path); got != thumb {
		t.Errorf("ThumbPath = %q, want %q", got, thumb)
	}
}

// TestFetchURL 下载远程图片落盘并按内容寻址去重。
func TestFetchURL(t *testing.T) {
	s := newTestStore(t)
	raw := pngBytes(t, 5, 5)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	a, err := s.FetchURL(context.Background(), srv.URL+"/x.png")
	if err != nil {
		t.Fatalf("FetchURL: %v", err)
	}
	if a.Source != "remote_url" {
		t.Errorf("Source = %q, want remote_url", a.Source)
	}
	if a.SourceURL != srv.URL+"/x.png" {
		t.Errorf("SourceURL = %q", a.SourceURL)
	}
	// 与本地 Put 同一内容应得到同一 sha。
	local, err := s.Put(raw, "upload", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if local.SHA256 != a.SHA256 {
		t.Errorf("远程与本地 sha 不一致: %s vs %s", a.SHA256, local.SHA256)
	}
}

// TestFetchURLErrors 非 http(s)、非 2xx、超限都要报错。
func TestFetchURLErrors(t *testing.T) {
	s := newTestStore(t)

	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/a.png", "data:image/png;base64,AAA"} {
		if _, err := s.FetchURL(context.Background(), u); err == nil {
			t.Errorf("不支持的 URL %q 应当报错", u)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := s.FetchURL(context.Background(), srv.URL); err == nil {
		t.Error("404 应当报错")
	}

	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{1, 2, 3, 4}, 4096))
	}))
	defer big.Close()
	s.MaxBytes = 1024
	if _, err := s.FetchURL(context.Background(), big.URL); err == nil {
		t.Error("超过 MaxBytes 的远程图应当报错")
	}
}

// TestRemove 删除资产同时清掉缩略图。
func TestRemove(t *testing.T) {
	s := newTestStore(t)
	rel := placeBlob(t, s, pngBytes(t, 16, 16), ".png")
	s.ensureThumb(s.Path(rel), "", rel)

	if err := s.Remove(rel); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(s.Path(rel)); !os.IsNotExist(err) {
		t.Errorf("原图仍存在: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.ThumbDir, filepath.FromSlash(s.ThumbRel(rel)))); !os.IsNotExist(err) {
		t.Errorf("缩略图仍存在: %v", err)
	}
	if err := s.Remove(rel); err == nil {
		t.Error("重复删除应当报错")
	}
}

// TestPutDistinctContentConcurrent 并发写入不同内容：不 panic、sha 互不相同且都落盘。
func TestPutDistinctContentConcurrent(t *testing.T) {
	s := newTestStore(t)
	const n = 8

	type result struct {
		a   domain.Asset
		err error
	}
	out := make(chan result, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			a, err := s.Put(pngBytes(t, 4+i, 4), "generated", "", "")
			out <- result{a: a, err: err}
		}()
	}

	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		r := <-out
		if r.err != nil {
			t.Fatalf("并发 Put: %v", r.err)
		}
		if seen[r.a.SHA256] {
			t.Errorf("不同内容得到重复 sha: %s", r.a.SHA256)
		}
		seen[r.a.SHA256] = true
		if _, err := os.Stat(s.Path(r.a.Path)); err != nil {
			t.Errorf("并发 Put 未落盘 %s: %v", r.a.Path, err)
		}
	}
	if len(seen) != n {
		t.Errorf("落盘数量 = %d, want %d", len(seen), n)
	}
}

// TestPutExistingContentConcurrent 文件已存在时并发 Put：走 os.Stat 短路，不应写盘、不应报错。
//
// 注意这里刻意不测「首次并发写入同一内容」——那会命中 writeFileSync 的
// 临时文件重名问题（blob.go:297，见交付报告）。
func TestPutExistingContentConcurrent(t *testing.T) {
	s := newTestStore(t)
	raw := pngBytes(t, 32, 32)
	rel := placeBlob(t, s, raw, ".png")
	abs := s.Path(rel)

	want, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	errCh := make(chan error, n)
	shaCh := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() {
			a, err := s.Put(raw, "generated", "", "")
			errCh <- err
			shaCh <- a.SHA256
		}()
	}
	first := ""
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("并发 Put 已存在内容: %v", err)
		}
		sha := <-shaCh
		if first == "" {
			first = sha
			continue
		}
		if sha != first {
			t.Errorf("并发 Put 得到不同 sha: %s vs %s", sha, first)
		}
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("已存在内容的并发 Put 重写了文件")
	}
}

// TestFormatHelpers 锁定格式推断表。
func TestFormatHelpers(t *testing.T) {
	cases := []struct{ mime, ext, want string }{
		{"image/png", "", "png"},
		{"image/jpeg", "", "jpeg"},
		{"image/jpg", "", "jpeg"},
		{"image/webp", "", "webp"},
		{"", ".JPG", "jpeg"},
		{"", "png", "png"},
		{"", "webp", "webp"},
		{"application/octet-stream", ".bin", "png"},
	}
	for _, c := range cases {
		if got := formatOf(c.mime, c.ext); got != c.want {
			t.Errorf("formatOf(%q,%q) = %q, want %q", c.mime, c.ext, got, c.want)
		}
	}
	if got := mimeOf("webp"); got != "image/webp" {
		t.Errorf("mimeOf(webp) = %q", got)
	}
	if got := extOf("jpeg", "png"); got != ".jpg" {
		t.Errorf("extOf(jpeg,png) = %q, want .jpg", got)
	}
}

// TestWriteFileSyncAtomic 写入后不应留下 .tmp 残留。
func TestWriteFileSyncAtomic(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "a.bin")
	if err := writeFileSync(dst, []byte("hello")); err != nil {
		t.Fatalf("writeFileSync: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "hello" {
		t.Errorf("内容 = %q, err = %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("留下临时文件: %s", e.Name())
		}
	}
}
