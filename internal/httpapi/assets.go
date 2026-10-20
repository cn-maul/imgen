package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/store"
)

// uploadFormField 是上传接口的 multipart 字段名。
// 同时接受 file 与 files（后者用于一次多张），其余字段名一律 400。
const (
	uploadFormField      = "file"
	uploadFormFieldMulti = "files"
)

// ============================================================
// POST /api/v1/assets —— 上传
// ============================================================

func (a *API) uploadAssets(w http.ResponseWriter, r *http.Request) {
	limit := a.maxUploadBytes()

	// MaxBytesReader 在读取超限时直接报错，避免先把 20MB 读进内存再拒绝。
	// 多留 1MB 给 multipart 边界与字段自身的开销。
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
	defer r.Body.Close()

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if isTooLarge(err) {
			writeDomainErr(w, domain.NewError(domain.CodeBadRequest,
				fmt.Sprintf("上传内容超过 %d MB 上限", int(limit>>20))))
			return
		}
		writeDomainErr(w, errBadRequest("请求不是合法的 multipart/form-data: "+err.Error()))
		return
	}

	form := r.MultipartForm
	if form == nil {
		writeDomainErr(w, errBadRequest("缺少 multipart 表单内容"))
		return
	}

	headers := make([]*multipart.FileHeader, 0, 2)
	headers = append(headers, form.File[uploadFormField]...)
	headers = append(headers, form.File[uploadFormFieldMulti]...)
	if len(headers) == 0 {
		writeDomainErr(w, errBadRequest(fmt.Sprintf("缺少文件字段 %q", uploadFormField)))
		return
	}

	assets := make([]assetDTO, 0, len(headers))
	for _, h := range headers {
		asset, de := a.ingestUpload(h, limit)
		if de != nil {
			writeDomainErr(w, de)
			return
		}
		assets = append(assets, asset)
	}

	status := http.StatusOK
	if len(assets) == 1 {
		status = http.StatusCreated
	}
	writeJSON(w, status, paged{Items: assets, Total: len(assets), Limit: len(assets), Offset: 0})
}

// ingestUpload 落盘一个上传文件：嗅探真实 MIME → 写入 blob → 记录资产。
func (a *API) ingestUpload(h *multipart.FileHeader, limit int64) (domain.Asset, *domain.Error) {
	if h == nil || h.Size == 0 {
		return domain.Asset{}, errBadRequest("上传文件为空")
	}
	if h.Size > limit {
		return domain.Asset{}, domain.NewError(domain.CodeBadRequest,
			fmt.Sprintf("文件 %s 超过 %d MB 上限", h.Filename, int(limit>>20)))
	}

	f, err := h.Open()
	if err != nil {
		return domain.Asset{}, errInternal("读取上传文件失败", err)
	}
	defer f.Close()

	// 全量读入内存再嗅探：上限已由 MaxBytesReader 与 h.Size 双重约束，
	// 且这样可以对同一份字节做「魔数 + 解码头」两道校验，不必回退重读流。
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return domain.Asset{}, errInternal("读取上传内容失败", err)
	}
	if int64(len(raw)) > limit {
		return domain.Asset{}, domain.NewError(domain.CodeBadRequest,
			fmt.Sprintf("文件 %s 超过 %d MB 上限", h.Filename, int(limit>>20)))
	}
	if len(raw) == 0 {
		return domain.Asset{}, errBadRequest("上传文件为空")
	}

	// ① 魔数嗅探：绝不信客户端给的 Content-Type。
	mime := http.DetectContentType(raw)
	if !strings.HasPrefix(mime, "image/") {
		return domain.Asset{}, domain.NewError(domain.CodeBadRequest,
			fmt.Sprintf("文件不是图片，嗅探得到 %s", mime))
	}

	// ② 解码头复核：光看魔数挡不住「伪装成 PNG 的垃圾」。
	if _, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
		return domain.Asset{}, domain.NewError(domain.CodeBadRequest,
			fmt.Sprintf("文件不是可识别的图片（嗅探类型 %s）", mime))
	}

	// ③ 交给 blob 层统一落盘与内容寻址去重。
	asset, err := a.deps.Blobs.PutReader(bytes.NewReader(raw), "upload", extOfMIME(mime), limit)
	if err != nil {
		return domain.Asset{}, domain.NewError(domain.CodeBadRequest,
			"保存上传图片失败: "+err.Error())
	}

	rec, _, err := a.deps.St.UpsertAsset(asset)
	if err != nil {
		return domain.Asset{}, errInternal("记录资产失败", err)
	}
	return *rec, nil
}

// extOfMIME 由嗅探出的 MIME 推出扩展名提示。
func extOfMIME(mime string) string {
	switch strings.ToLower(mime) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}

// isTooLarge 判断 multipart 解析失败是否源于体积超限。
// net/http 返回 *http.MaxBytesError，用 errors.As 判别而非字符串匹配。
func isTooLarge(err error) bool {
	var mbErr *http.MaxBytesError
	return errors.As(err, &mbErr)
}

// ============================================================
// GET /api/v1/assets
// ============================================================

func (a *API) listAssets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AssetFilter{
		Source: strings.TrimSpace(q.Get("source")),
		Query:  strings.TrimSpace(q.Get("q")),
		Limit:  queryInt(q.Get("limit"), 60),
		Offset: queryInt(q.Get("offset"), 0),
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	items, total, err := a.deps.St.ListAssets(f)
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []domain.Asset{}
	}
	writeJSON(w, http.StatusOK, paged{
		Items: items, Total: total, Limit: normAssetLimit(f.Limit), Offset: f.Offset,
	})
}

// normAssetLimit 回显实际生效的分页大小，与 store.ListAssets 的钳制规则
// 完全一致：`if limit <= 0 || limit > 200 { limit = 60 }`。
func normAssetLimit(v int) int {
	if v > 0 && v <= 200 {
		return v
	}
	return 60
}

// ============================================================
// GET /api/v1/assets/:id/raw 与 /thumb
// ============================================================

func (a *API) serveAssetRaw(w http.ResponseWriter, r *http.Request) {
	a.serveAssetFile(w, r, false)
}

func (a *API) serveAssetThumb(w http.ResponseWriter, r *http.Request) {
	a.serveAssetFile(w, r, true)
}

// serveAssetFile 直出图片字节。缩略图缺失时回落到原图
// （blob.ThumbPath 内部已做这个判断）。
func (a *API) serveAssetFile(w http.ResponseWriter, r *http.Request, thumb bool) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeDomainErr(w, errBadRequest("缺少资产 id"))
		return
	}

	asset, err := a.deps.St.GetAsset(id)
	if err != nil {
		writeErr(w, err) // 未知资产 → 404
		return
	}

	abs := a.deps.Blobs.Path(asset.Path)
	if thumb {
		abs = a.deps.Blobs.ThumbPath(asset.Path)
	}

	f, err := os.Open(abs)
	if err != nil {
		writeErr(w, errInternal("读取图片文件失败", err))
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		writeErr(w, errInternal("读取图片信息失败", err))
		return
	}

	// 缩略图统一是 JPEG；原图按资产记录的 MIME 出。
	mime := asset.MIME
	if strings.EqualFold(filepath.Ext(abs), ".jpg") {
		mime = "image/jpeg"
	}
	if mime == "" {
		mime = "image/png"
	}

	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	h.Set("X-Content-Type-Options", "nosniff")
	// 内容寻址意味着同一路径永不变更内容，可放心长缓存。
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Content-Disposition", contentDisposition(asset, thumb))
	w.WriteHeader(http.StatusOK)

	if r.Method == http.MethodHead {
		return
	}
	// 图片可能很大，流式写出而非整读进内存。
	if _, err := io.Copy(w, f); err != nil {
		a.log.Warn("写出图片失败", "asset", id, "err", err)
	}
}

// contentDisposition 给下载一个可识别的文件名。
func contentDisposition(a *domain.Asset, thumb bool) string {
	name := a.ID
	if thumb {
		name += "-thumb"
	}
	ext := a.Ext
	if thumb {
		ext = ".jpg"
	} else if ext == "" {
		ext = ".png"
	}
	return fmt.Sprintf(`inline; filename="%s%s"`, name, ext)
}

// ============================================================
// DELETE /api/v1/assets/:id
// ============================================================

func (a *API) deleteAsset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeDomainErr(w, errBadRequest("缺少资产 id"))
		return
	}

	// 先取记录再删：记录没了就再也找不到磁盘路径。
	asset, err := a.deps.St.GetAsset(id)
	if err != nil {
		writeErr(w, err) // 未知资产 → 404
		return
	}
	if err := a.deps.St.DeleteAsset(id); err != nil {
		writeErr(w, err)
		return
	}

	// 磁盘清理失败不影响已完成的删除：记录已消失，残文件留待下次 GC。
	if err := a.deps.Blobs.Remove(asset.Path); err != nil {
		a.log.Warn("删除图片文件失败", "asset", id, "path", asset.Path, "err", err)
	}

	writeNoContent(w)
}

// ============================================================
// GET /api/v1/usage
// ============================================================

func (a *API) usage(w http.ResponseWriter, r *http.Request) {
	days := queryInt(r.URL.Query().Get("days"), 14)
	if days <= 0 || days > 90 {
		days = 14
	}
	items, err := a.deps.St.ListUsage(days)
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []store.UsageStat{}
	}
	writeJSON(w, http.StatusOK, usageDTO{Days: days, Items: items})
}

// ============================================================
// 工具
// ============================================================

// maxUploadBytes 返回当前上传上限（字节）。
func (a *API) maxUploadBytes() int64 {
	mb := a.MaxUploadMB()
	if mb <= 0 {
		mb = defaultMaxUploadMB
	}
	return int64(mb) << 20
}
