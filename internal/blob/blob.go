// Package blob 提供内容寻址的图片存储与缩略图生成。
package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"github.com/imgen/imgen/internal/domain"
)

// Store 把图片按 sha256 内容寻址落盘，天然去重。
type Store struct {
	BlobDir  string
	ThumbDir string
	Client   *http.Client
	MaxBytes int64
	ThumbMax int
}

// NewStore 创建存储，MaxRemoteMB 为远程下载体积上限。
func NewStore(blobDir, thumbDir string, maxRemoteMB int) *Store {
	return &Store{
		BlobDir:  blobDir,
		ThumbDir: thumbDir,
		MaxBytes: int64(maxRemoteMB) << 20,
		ThumbMax: 512,
		Client: &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("重定向次数过多")
				}
				return nil
			},
		},
	}
}

// DataURI 把本地图片编码为 data URI，供上游直接读取参考图。
// 体积上限由 MaxBytes 控制，避免构造出超大请求体。
func (s *Store) DataURI(rel string) (string, error) {
	abs := s.Path(rel)
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("读取图片: %w", err)
	}
	if int64(len(raw)) > s.MaxBytes {
		return "", fmt.Errorf("参考图超过上限 %d MB", s.MaxBytes>>20)
	}
	mime := mimeOf(formatFromExt(rel))
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func formatFromExt(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".webp":
		return "webp"
	default:
		return "png"
	}
}

// Put 校验并保存一份图片字节，返回资产元数据。
// extHint 来自上游声明的 output_format，仅作解码失败时的兜底。
func (s *Store) Put(raw []byte, source, sourceURL, extHint string) (domain.Asset, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return domain.Asset{}, fmt.Errorf("无法识别的图片数据: %w", err)
	}
	if int64(len(raw)) > s.MaxBytes {
		return domain.Asset{}, fmt.Errorf("图片超过上限 %d MB", s.MaxBytes>>20)
	}

	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])
	ext := extOf(format, extHint)
	rel := filepath.Join(sha[:2], sha[2:4], sha+ext)

	abs := filepath.Join(s.BlobDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return domain.Asset{}, fmt.Errorf("建目录: %w", err)
	}
	// 内容寻址意味着同内容只写一次；已存在则跳过。
	if _, err := os.Stat(abs); err == nil {
		return s.meta(sha, rel, format, len(raw), cfg, source, sourceURL), nil
	}
	if err := writeFileSync(abs, raw); err != nil {
		return domain.Asset{}, fmt.Errorf("写入图片: %w", err)
	}

	go s.ensureThumb(abs, sha, rel)
	return s.meta(sha, rel, format, len(raw), cfg, source, sourceURL), nil
}

// PutReader 从流写入，用于上传。
func (s *Store) PutReader(r io.Reader, source, extHint string, limit int64) (domain.Asset, error) {
	if limit <= 0 {
		limit = s.MaxBytes
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return domain.Asset{}, fmt.Errorf("读取上传内容: %w", err)
	}
	if int64(len(raw)) > limit {
		return domain.Asset{}, fmt.Errorf("内容超过上限 %d MB", limit>>20)
	}
	return s.Put(raw, source, "", extHint)
}

func (s *Store) meta(sha, rel, format string, n int, cfg image.Config, source, sourceURL string) domain.Asset {
	return domain.Asset{
		ID:        "a_" + sha[:16],
		SHA256:    sha,
		MIME:      mimeOf(format),
		Ext:       extOf(format, ""),
		Width:     cfg.Width,
		Height:    cfg.Height,
		Bytes:     int64(n),
		Path:      filepath.ToSlash(rel),
		Source:    source,
		SourceURL: sourceURL,
	}
}

// ensureThumb 生成缩略图；失败静默（列表页退化为原图）。
// 缩略图统一用 JPEG：标准库自带编码器，而 x/image/webp 仅支持解码。
func (s *Store) ensureThumb(abs, sha, rel string) {
	thumbRel := s.ThumbRel(rel)
	thumbAbs := filepath.Join(s.ThumbDir, filepath.FromSlash(thumbRel))
	if _, err := os.Stat(thumbAbs); err == nil {
		return
	}

	src, err := os.Open(abs)
	if err != nil {
		return
	}
	defer src.Close()

	img, _, err := image.Decode(src)
	if err != nil {
		return
	}

	b := img.Bounds()
	scale := float64(s.ThumbMax) / float64(maxInt(b.Dx(), b.Dy()))
	w, h := b.Dx(), b.Dy()
	if scale < 1 {
		w = maxInt(1, int(float64(b.Dx())*scale))
		h = maxInt(1, int(float64(b.Dy())*scale))
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)

	if err := os.MkdirAll(filepath.Dir(thumbAbs), 0o750); err != nil {
		return
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return
	}
	_ = writeFileSync(thumbAbs, buf.Bytes())
}

// ThumbRel 返回缩略图的相对路径（可能尚未生成）。
func (s *Store) ThumbRel(rel string) string {
	return filepath.ToSlash(strings.TrimSuffix(rel, filepath.Ext(rel)) + ".thumb.jpg")
}

// Path 返回资产原图的绝对路径。
func (s *Store) Path(rel string) string { return filepath.Join(s.BlobDir, filepath.FromSlash(rel)) }

// ThumbPath 返回缩略图路径；若尚未生成则回退到原图。
func (s *Store) ThumbPath(rel string) string {
	t := filepath.Join(s.ThumbDir, filepath.FromSlash(s.ThumbRel(rel)))
	if _, err := os.Stat(t); err == nil {
		return t
	}
	return s.Path(rel)
}

// FetchURL 下载远程图片并落盘。上游返回的 url 可能有有效期，必须立即取回。
func (s *Store) FetchURL(ctx context.Context, rawURL string) (domain.Asset, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return domain.Asset{}, fmt.Errorf("不支持的 URL: %s", truncate(rawURL, 80))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return domain.Asset{}, fmt.Errorf("构造请求: %w", err)
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return domain.Asset{}, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domain.Asset{}, fmt.Errorf("下载返回 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, s.MaxBytes+1))
	if err != nil {
		return domain.Asset{}, fmt.Errorf("读取下载内容: %w", err)
	}
	if int64(len(raw)) > s.MaxBytes {
		return domain.Asset{}, fmt.Errorf("远程图片超过上限 %d MB", s.MaxBytes>>20)
	}

	// 落盘并标记来源与原始 URL，便于排查过期问题。
	return s.Put(raw, "remote_url", rawURL, "")
}

// Remove 删除资产文件（不含缩略图之外的引用清理）。
func (s *Store) Remove(rel string) error {
	_ = os.Remove(filepath.Join(s.ThumbDir, filepath.FromSlash(s.ThumbRel(rel))))
	return os.Remove(s.Path(rel))
}

func formatOf(mime, ext string) string {
	switch strings.ToLower(mime) {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	}
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "png":
		return "png"
	case "jpg", "jpeg":
		return "jpeg"
	case "webp":
		return "webp"
	}
	return "png"
}

func mimeOf(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	}
	return "image/png"
}

func extOf(format, hint string) string {
	switch formatOf("", format) {
	case "png":
		return ".png"
	case "jpeg":
		return ".jpg"
	case "webp":
		return ".webp"
	}
	return ".png"
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// writeFileSync 先写临时文件再原子改名，避免读到半截文件。
func writeFileSync(dst string, raw []byte) error {
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
