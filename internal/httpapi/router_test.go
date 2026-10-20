package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imgen/imgen/internal/blob"
	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/httpx"
	"github.com/imgen/imgen/internal/job"
	"github.com/imgen/imgen/internal/provider"
	"github.com/imgen/imgen/internal/provider/agnes"
	"github.com/imgen/imgen/internal/provider/sensenova"
	"github.com/imgen/imgen/internal/secret"
	"github.com/imgen/imgen/internal/store"
)

// ----------------------------------------------------------------
// 测试脚手架
// ----------------------------------------------------------------

// testEnv 是一套最小可运行的 API 装配。
type testEnv struct {
	API   *API
	St    *store.Store
	Blobs *blob.Store
	Q     *job.Queue
	Jobs  *job.Manager
	Dir   string
}

// fakeKeys 固定返回一个假 Key，避免测试依赖真实凭据。
type fakeKeys struct{}

func (fakeKeys) APIKey(_ context.Context, _ domain.ProviderID) (string, error) {
	return "sk-test-key-1234567890", nil
}

// testProviders 返回两家适配器，与 internal/app 的装配保持一致。
func testProviders() map[domain.ProviderID]provider.Provider {
	return map[domain.ProviderID]provider.Provider{
		domain.ProviderSenseNova: sensenova.New(""),
		domain.ProviderAgnes:     agnes.New(""),
	}
}

// newTestEnv 建一套临时环境。
//
// 用 testing.TempDir() 里的 sqlite 文件而非 :memory: —— driver 与
// store 的 SetMaxOpenConns(1) 组合下，内存库会在并发访问时出问题。
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "imgen.db"))
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	blobs := blob.NewStore(filepath.Join(dir, "blobs"), filepath.Join(dir, "thumbs"), 32)

	provs := testProviders()
	exec := &job.Executor{
		Providers: provs,
		Keys:      fakeKeys{},
		Blobs:     blobs,
		St:        st,
		Log:       slog.Default(),
		HTTP:      httpx.New(slog.Default()),
	}
	q := job.NewQueue(2)
	mgr := job.NewManager(exec, st, q, slog.Default())
	t.Cleanup(func() { q.Close() })

	api := New(Deps{
		Log:         slog.Default(),
		St:          st,
		Blobs:       blobs,
		Jobs:        mgr,
		Providers:   provs,
		Keys:        fakeKeys{},
		Cipher:      testCipher(t, dir),
		MaxUploadMB: 20,
	})

	return &testEnv{API: api, St: st, Blobs: blobs, Q: q, Jobs: mgr, Dir: dir}
}

// testCipher 构造一个测试用密钥。空环境变量 → 在 dir 下生成并落盘。
func testCipher(t *testing.T, dir string) *secret.Cipher {
	t.Helper()
	c, err := secret.NewCipherFromEnv("", dir)
	if err != nil {
		t.Fatalf("构造密钥: %v", err)
	}
	return c
}

// do 发起一次请求并返回响应。body 非空时按 JSON 发送。
func (e *testEnv) do(t *testing.T, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.API.Handler().ServeHTTP(w, req)
	return w
}

// serve 直接驱动一个已构造好的请求（用于 multipart 等非 JSON 场景）。
func (e *testEnv) serve(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	e.API.Handler().ServeHTTP(w, req)
	return w
}

// ----------------------------------------------------------------
// 断言工具
// ----------------------------------------------------------------

// errBody 是错误响应的解析目标。
type errBody struct {
	Error struct {
		Code           string `json:"code"`
		Message        string `json:"message"`
		Provider       string `json:"provider,omitempty"`
		UpstreamStatus int    `json:"upstreamStatus,omitempty"`
		UpstreamType   string `json:"upstreamType,omitempty"`
		Retryable      bool   `json:"retryable"`
	} `json:"error"`
}

// parseErr 解析错误响应，并顺带断言外层一定是 {"error": {...}}。
func parseErr(t *testing.T, w *httptest.ResponseRecorder) errBody {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("响应不是合法 JSON: %v / %s", err, w.Body.String())
	}
	if _, ok := raw["error"]; !ok {
		t.Fatalf("错误响应缺少 error 字段: %s", w.Body.String())
	}
	var out errBody
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析错误体: %v / %s", err, w.Body.String())
	}
	return out
}

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("状态码 = %d, 期望 %d; body=%s", w.Code, want, w.Body.String())
	}
}

// ----------------------------------------------------------------
// 健康检查
// ----------------------------------------------------------------

func TestHealth(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/api/v1/health", nil)
	wantStatus(t, w, http.StatusOK)

	var got healthDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析 health: %v", err)
	}
	if !got.OK {
		t.Errorf("health.ok = false, 期望 true")
	}
	if got.API != "v1" {
		t.Errorf("health.api = %q, 期望 v1", got.API)
	}
}

// TestRootIsNotHandledByAPI 确认 httpapi 只挂 /api/v1：
// SPA fallback 属于 internal/web，httpapi 不应接管根路径。
func TestRootIsNotHandledByAPI(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("根路径状态码 = %d, 期望 404（SPA fallback 属于 internal/web）", w.Code)
	}
}

// ----------------------------------------------------------------
// Providers
// ----------------------------------------------------------------

func TestListProviders(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/api/v1/providers", nil)
	wantStatus(t, w, http.StatusOK)

	var got providerListDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析 providers: %v", err)
	}
	if len(got.Providers) != 2 {
		t.Fatalf("通道数 = %d, 期望 2; body=%s", len(got.Providers), w.Body.String())
	}

	byID := map[string]providerDTO{}
	for _, p := range got.Providers {
		byID[p.ID] = p
	}
	for _, id := range []string{"sensenova", "agnes"} {
		p, ok := byID[id]
		if !ok {
			t.Fatalf("缺少通道 %s", id)
		}
		if len(p.Models) == 0 {
			t.Errorf("通道 %s 没有模型", id)
		}
		if len(p.Caps) != len(p.Models) {
			t.Errorf("通道 %s 的 caps 数 = %d, 应与模型数 %d 一致",
				id, len(p.Caps), len(p.Models))
		}
		for _, m := range p.Models {
			if _, ok := p.Caps[m.ID]; !ok {
				t.Errorf("通道 %s 的模型 %s 缺少 caps", id, m.ID)
			}
		}
		if p.BaseURL == "" {
			t.Errorf("通道 %s 的 baseUrl 为空", id)
		}
		if !p.HasKey {
			// 迁移只插了通道定义，Key 是空的。
			t.Logf("通道 %s 的 hasKey=false（预期：尚未配置 Key）", id)
		}
	}

	// 能力差异必须体现在响应里：前端靠它决定参数面板显隐。
	sn, ag := byID["sensenova"], byID["agnes"]
	if !sn.Caps[sn.Models[0].ID].NoWatermark {
		t.Errorf("sensenova 应支持 noWatermark")
	}
	if ag.Caps[ag.Models[0].ID].NoWatermark {
		t.Errorf("agnes 不应支持 noWatermark")
	}
	if len(ag.Caps[ag.Models[0].ID].OutputFormats) != 1 {
		t.Errorf("agnes 的 outputFormats 应只有 png，实际 %v",
			ag.Caps[ag.Models[0].ID].OutputFormats)
	}
}

func TestListProvidersMasksKey(t *testing.T) {
	e := newTestEnv(t)

	// 存一个真实长度的 Key，验证回显是掩码而非明文。
	const plain = "sk-secret-abcdefghijklmnop"
	cipher := testCipher(t, e.Dir)
	sealed, err := cipher.Encrypt([]byte(plain))
	if err != nil {
		t.Fatalf("加密: %v", err)
	}
	if err := e.St.UpdateProvider(domain.ProviderSenseNova,
		store.ProviderConfig{APIKey: sealed}); err != nil {
		t.Fatalf("写入 Key: %v", err)
	}

	w := e.do(t, http.MethodGet, "/api/v1/providers", nil)
	wantStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), plain) {
		t.Fatalf("响应泄露了明文 Key: %s", w.Body.String())
	}

	var got providerListDTO
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	var found bool
	for _, p := range got.Providers {
		if p.ID != "sensenova" {
			continue
		}
		found = true
		if !p.HasKey {
			t.Errorf("hasKey = false, 期望 true")
		}
		if p.APIKeyMask == "" {
			t.Errorf("apiKeyMask 为空，期望掩码")
		}
		if !strings.Contains(p.APIKeyMask, "*") {
			t.Errorf("apiKeyMask = %q, 应含掩码字符", p.APIKeyMask)
		}
		if p.APIKeyMask == plain {
			t.Errorf("apiKeyMask 等于明文，掩码未生效")
		}
	}
	if !found {
		t.Fatalf("响应里没有 sensenova 通道")
	}
}

func TestUnknownProviderIDReturns404(t *testing.T) {
	e := newTestEnv(t)

	for _, tc := range []struct{ name, method, path string }{
		{"patch 未知通道", http.MethodPatch, "/api/v1/providers/does-not-exist"},
		{"test 未知通道", http.MethodPost, "/api/v1/providers/does-not-exist/test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := e.do(t, tc.method, tc.path, []byte(`{"enabled":true}`))
			wantStatus(t, w, http.StatusNotFound)
			got := parseErr(t, w)
			if got.Error.Code != domain.CodeNotFound {
				t.Errorf("error.code = %q, 期望 %q", got.Error.Code, domain.CodeNotFound)
			}
		})
	}
}

func TestPatchProviderRejectsEmptyBody(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodPatch, "/api/v1/providers/sensenova", []byte(`{}`))
	wantStatus(t, w, http.StatusBadRequest)
	if got := parseErr(t, w); got.Error.Code != domain.CodeBadRequest {
		t.Errorf("error.code = %q, 期望 %q", got.Error.Code, domain.CodeBadRequest)
	}
}

func TestPatchProviderUpdatesEnabledAndBaseURL(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodPatch, "/api/v1/providers/agnes",
		[]byte(`{"baseUrl":"https://example.test/v1/","enabled":false}`))
	wantStatus(t, w, http.StatusOK)

	var got providerMutationDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if got.Provider.Enabled {
		t.Errorf("enabled = true, 期望 false")
	}
	// store.UpdateProvider 会 TrimRight 掉结尾斜杠。
	if got.Provider.BaseURL != "https://example.test/v1" {
		t.Errorf("baseUrl = %q, 期望 %q", got.Provider.BaseURL, "https://example.test/v1")
	}
}

// ----------------------------------------------------------------
// Jobs
// ----------------------------------------------------------------

func TestCreateJobValidation(t *testing.T) {
	e := newTestEnv(t)

	cases := []struct {
		name string
		body string
	}{
		{"缺 provider", `{"model":"m","prompt":"hi"}`},
		{"缺 prompt", `{"provider":"sensenova","model":"m"}`},
		{"缺 model", `{"provider":"sensenova","prompt":"hi"}`},
		{"非法 mode", `{"provider":"sensenova","model":"m","prompt":"hi","mode":"nope"}`},
		{"非法 tier", `{"provider":"sensenova","model":"m","prompt":"hi","tier":"8K"}`},
		{"非法 aspect", `{"provider":"sensenova","model":"m","prompt":"hi","aspect":"1:7"}`},
		{"count 过大", `{"provider":"sensenova","model":"m","prompt":"hi","count":99}`},
		{"非法 outputFormat", `{"provider":"sensenova","model":"m","prompt":"hi","outputFormat":"bmp"}`},
		{"prompt 只有空白", `{"provider":"sensenova","model":"m","prompt":"   "}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := e.do(t, http.MethodPost, "/api/v1/jobs", []byte(tc.body))
			wantStatus(t, w, http.StatusBadRequest)
			got := parseErr(t, w)
			if got.Error.Code != domain.CodeBadRequest {
				t.Errorf("error.code = %q, 期望 %q", got.Error.Code, domain.CodeBadRequest)
			}
			// 参数错误改成重试没有意义：retryable 必须是 false。
			if got.Error.Retryable {
				t.Errorf("retryable = true, 参数错误应为 false")
			}
		})
	}
}

func TestCreateJobMalformedJSON(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodPost, "/api/v1/jobs", []byte(`{not json`))
	wantStatus(t, w, http.StatusBadRequest)
	if got := parseErr(t, w); got.Error.Code != domain.CodeBadRequest {
		t.Errorf("error.code = %q, 期望 %q", got.Error.Code, domain.CodeBadRequest)
	}
}

func TestCreateJobUnknownProvider(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodPost, "/api/v1/jobs",
		[]byte(`{"provider":"nope","model":"m","prompt":"hi"}`))
	// Manager.Submit 对未知通道返回 ErrBadRequest → 400。
	wantStatus(t, w, http.StatusBadRequest)
	if got := parseErr(t, w); got.Error.Code != domain.CodeBadRequest {
		t.Errorf("error.code = %q, 期望 %q", got.Error.Code, domain.CodeBadRequest)
	}
}

func TestGetUnknownJobReturns404(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/api/v1/jobs/j_nope", nil)
	wantStatus(t, w, http.StatusNotFound)

	got := parseErr(t, w)
	if got.Error.Code != domain.CodeNotFound {
		t.Errorf("error.code = %q, 期望 %q", got.Error.Code, domain.CodeNotFound)
	}
}

func TestCancelUnknownJobReturns404(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodPost, "/api/v1/jobs/j_nope/cancel", nil)
	wantStatus(t, w, http.StatusNotFound)
	if got := parseErr(t, w); got.Error.Code != domain.CodeNotFound {
		t.Errorf("error.code = %q, 期望 %q", got.Error.Code, domain.CodeNotFound)
	}
}

func TestListJobsEmpty(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/api/v1/jobs", nil)
	wantStatus(t, w, http.StatusOK)

	var got paged
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析列表: %v", err)
	}
	if got.Total != 0 {
		t.Errorf("total = %d, 期望 0", got.Total)
	}
	if got.Items == nil {
		t.Errorf("items 为 null，期望空数组")
	}
}

// ----------------------------------------------------------------
// 错误码 → 状态码映射
// ----------------------------------------------------------------

func TestStatusCodeMapping(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{domain.CodeUpstreamAuth, http.StatusUnauthorized},
		{domain.CodeUpstreamQuota, http.StatusTooManyRequests},
		{domain.CodeUpstreamTimeout, http.StatusGatewayTimeout},
		{domain.CodeUpstreamRejected, http.StatusBadGateway},
		{domain.CodeUpstreamUnavailable, http.StatusBadGateway},
		{domain.CodeUpstreamUnparseable, http.StatusBadGateway},
		{domain.CodeBadRequest, http.StatusBadRequest},
		{domain.CodeNotFound, http.StatusNotFound},
		{domain.CodeInternal, http.StatusInternalServerError},
		{domain.CodeUpstreamModeration, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if got := statusForCode(tc.code); got != tc.want {
			t.Errorf("statusForCode(%q) = %d, 期望 %d", tc.code, got, tc.want)
		}
	}
}

// TestErrorBodyCarriesRetryable 是「retryable 必须透出」的端到端断言：
// 前端靠这个字段决定弹「重试」还是「改提示词」。
func TestErrorBodyCarriesRetryable(t *testing.T) {
	cases := []struct {
		name      string
		derr      *domain.Error
		wantCode  int
		retryable bool
	}{
		{"可重试的配额错误", &domain.Error{Code: domain.CodeUpstreamQuota,
			Message: "超出 RPM", Provider: "agnes", UpstreamStatus: 429, Retryable: true},
			http.StatusTooManyRequests, true},
		{"不可重试的鉴权错误", &domain.Error{Code: domain.CodeUpstreamAuth,
			Message: "Key 无效", Provider: "agnes", UpstreamStatus: 401, Retryable: false},
			http.StatusUnauthorized, false},
		{"可重试的超时", &domain.Error{Code: domain.CodeUpstreamTimeout,
			Message: "上游超时", Provider: "sensenova", Retryable: true},
			http.StatusGatewayTimeout, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeDomainErr(w, tc.derr)
			wantStatus(t, w, tc.wantCode)

			got := parseErr(t, w)
			if got.Error.Code != tc.derr.Code {
				t.Errorf("code = %q, 期望 %q", got.Error.Code, tc.derr.Code)
			}
			if got.Error.Retryable != tc.retryable {
				t.Errorf("retryable = %v, 期望 %v —— 该字段必须原样透出",
					got.Error.Retryable, tc.retryable)
			}
			if got.Error.UpstreamStatus != tc.derr.UpstreamStatus {
				t.Errorf("upstreamStatus = %d, 期望 %d",
					got.Error.UpstreamStatus, tc.derr.UpstreamStatus)
			}
			if got.Error.Provider != tc.derr.Provider {
				t.Errorf("provider = %q, 期望 %q", got.Error.Provider, tc.derr.Provider)
			}
		})
	}
}

// TestNotFoundUsesErrorsIs 断言错误识别走哨兵而非字符串匹配：
// store.GetJob 返回的是 fmt.Errorf("%w: 任务 %s", domain.ErrNotFound, id)，
// 消息里带了 id，只有 errors.Is 能可靠判别。
func TestNotFoundUsesErrorsIs(t *testing.T) {
	e := newTestEnv(t)

	_, err := e.St.GetJob("j_missing")
	if err == nil {
		t.Fatalf("查询不存在的任务应返回错误")
	}

	w := httptest.NewRecorder()
	writeErr(w, err)
	wantStatus(t, w, http.StatusNotFound)

	got := parseErr(t, w)
	if got.Error.Code != domain.CodeNotFound {
		t.Fatalf("error.code = %q, 期望 %q（错误识别必须走 errors.Is）",
			got.Error.Code, domain.CodeNotFound)
	}
}

// ----------------------------------------------------------------
// 上传
// ----------------------------------------------------------------

// pngBytes 生成一张合法的 PNG。
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("生成 PNG: %v", err)
	}
	return buf.Bytes()
}

// multipartUpload 构造一个 multipart 上传请求。
// 故意把客户端 Content-Type 写成 application/octet-stream：
// 服务端必须嗅探真实类型而不是采信它。
func multipartUpload(t *testing.T, field, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+filename+`"`)
	h.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("创建 part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestUploadValidPNG(t *testing.T) {
	e := newTestEnv(t)
	w := e.serve(t, multipartUpload(t, "file", "ok.png", pngBytes(t)))
	wantStatus(t, w, http.StatusCreated)

	// 嗅探出真实 MIME，而不是客户端声称的 application/octet-stream。
	if !strings.Contains(w.Body.String(), `"mime":"image/png"`) {
		t.Errorf("响应里的 mime 不是 image/png: %s", w.Body.String())
	}

	// 资产应已入库。
	list := e.do(t, http.MethodGet, "/api/v1/assets", nil)
	wantStatus(t, list, http.StatusOK)
	var got paged
	_ = json.Unmarshal(list.Body.Bytes(), &got)
	if got.Total != 1 {
		t.Errorf("资产总数 = %d, 期望 1; body=%s", got.Total, list.Body.String())
	}
}

func TestUploadRejectsNonImageBytes(t *testing.T) {
	e := newTestEnv(t)
	// 一段文本即使被声明成图片，也必须被嗅探挡下。
	w := e.serve(t, multipartUpload(t, "file", "evil.png",
		[]byte("this is definitely not an image, just plain text")))
	wantStatus(t, w, http.StatusBadRequest)

	got := parseErr(t, w)
	if got.Error.Code != domain.CodeBadRequest {
		t.Errorf("error.code = %q, 期望 %q", got.Error.Code, domain.CodeBadRequest)
	}
}

func TestUploadRejectsImageWithBadPayload(t *testing.T) {
	e := newTestEnv(t)
	// PNG 魔数正确（能通过 http.DetectContentType），但 IHDR 是垃圾：
	// 只有 image.DecodeConfig 那道复核能挡住它。
	raw := pngBytes(t)
	bad := make([]byte, len(raw))
	copy(bad, raw)
	// 覆盖 IHDR 里的宽高为 0：解码器会拒绝。
	for i := 16; i < 24 && i < len(bad); i++ {
		bad[i] = 0xFF
	}
	w := e.serve(t, multipartUpload(t, "file", "bad.png", bad))
	wantStatus(t, w, http.StatusBadRequest)

	if got := parseErr(t, w); got.Error.Code != domain.CodeBadRequest {
		t.Errorf("error.code = %q, 期望 %q", got.Error.Code, domain.CodeBadRequest)
	}
}

func TestUploadRejectsMissingField(t *testing.T) {
	e := newTestEnv(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("notfile", "x")
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	w := e.serve(t, req)
	wantStatus(t, w, http.StatusBadRequest)
}

func TestUploadRejectsEmptyFile(t *testing.T) {
	e := newTestEnv(t)
	w := e.serve(t, multipartUpload(t, "file", "empty.png", []byte{}))
	wantStatus(t, w, http.StatusBadRequest)
}

func TestUploadRejectsNotMultipart(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets",
		strings.NewReader("nope"))
	req.Header.Set("Content-Type", "application/json")
	w := e.serve(t, req)
	wantStatus(t, w, http.StatusBadRequest)
}

// ----------------------------------------------------------------
// 资产读取与删除
// ----------------------------------------------------------------

func TestAssetRawAndDelete(t *testing.T) {
	e := newTestEnv(t)
	w := e.serve(t, multipartUpload(t, "file", "ok.png", pngBytes(t)))
	wantStatus(t, w, http.StatusCreated)

	var created paged
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析上传响应: %v", err)
	}
	items, ok := created.Items.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("上传响应 items 异常: %s", w.Body.String())
	}
	m, _ := items[0].(map[string]any)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("上传响应没有 id: %s", w.Body.String())
	}

	// /raw 应直出图片字节。
	raw := e.do(t, http.MethodGet, "/api/v1/assets/"+id+"/raw", nil)
	wantStatus(t, raw, http.StatusOK)
	if ct := raw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Errorf("raw 的 Content-Type = %q, 期望 image/*", ct)
	}
	if len(raw.Body.Bytes()) == 0 {
		t.Errorf("raw 响应体为空")
	}

	// /thumb 在缩略图尚未生成时应回落到原图，仍然 200。
	thumb := e.do(t, http.MethodGet, "/api/v1/assets/"+id+"/thumb", nil)
	wantStatus(t, thumb, http.StatusOK)

	// 删除后应 404。
	del := e.do(t, http.MethodDelete, "/api/v1/assets/"+id, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("删除状态码 = %d, 期望 204; body=%s", del.Code, del.Body.String())
	}
	again := e.do(t, http.MethodDelete, "/api/v1/assets/"+id, nil)
	wantStatus(t, again, http.StatusNotFound)
}

func TestUnknownAssetReturns404(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{"/api/v1/assets/a_nope/raw", "/api/v1/assets/a_nope/thumb"} {
		w := e.do(t, http.MethodGet, path, nil)
		wantStatus(t, w, http.StatusNotFound)
	}
	w := e.do(t, http.MethodDelete, "/api/v1/assets/a_nope", nil)
	wantStatus(t, w, http.StatusNotFound)
}

// ----------------------------------------------------------------
// Settings
// ----------------------------------------------------------------

func TestGetSettings(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/api/v1/settings", nil)
	wantStatus(t, w, http.StatusOK)

	var got settingsDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析 settings: %v", err)
	}
	if got.MaxUploadMB != 20 {
		t.Errorf("maxUploadMB = %d, 期望 20（来自 Deps 注入）", got.MaxUploadMB)
	}
	if got.Concurrency != 2 {
		t.Errorf("concurrency = %d, 期望 2（来自 Queue）", got.Concurrency)
	}
}

func TestPutSettingsConcurrency(t *testing.T) {
	e := newTestEnv(t)

	// 合法值：调 Queue.SetWorkers 后回显生效值。
	w := e.do(t, http.MethodPut, "/api/v1/settings", []byte(`{"concurrency":8}`))
	wantStatus(t, w, http.StatusOK)
	var got settingsMutationDTO
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Settings.Concurrency != 8 {
		t.Errorf("concurrency = %d, 期望 8", got.Settings.Concurrency)
	}
	if e.Q.Workers() != 8 {
		t.Errorf("Queue.Workers() = %d, 期望 8（SetWorkers 未生效）", e.Q.Workers())
	}

	// 超上限：应被拒（clamp 上限是 16，但 500 属于明显越界）。
	w = e.do(t, http.MethodPut, "/api/v1/settings", []byte(`{"concurrency":500}`))
	wantStatus(t, w, http.StatusBadRequest)

	// 下限：0 也应被拒，而不是被 Queue 悄悄改成 1。
	w = e.do(t, http.MethodPut, "/api/v1/settings", []byte(`{"concurrency":0}`))
	wantStatus(t, w, http.StatusBadRequest)
}

func TestPutSettingsMaxUploadAndKeepDays(t *testing.T) {
	e := newTestEnv(t)

	w := e.do(t, http.MethodPut, "/api/v1/settings",
		[]byte(`{"maxUploadMB":64,"keepDays":7}`))
	wantStatus(t, w, http.StatusOK)

	var got settingsMutationDTO
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Settings.MaxUploadMB != 64 {
		t.Errorf("maxUploadMB = %d, 期望 64", got.Settings.MaxUploadMB)
	}
	if got.Settings.KeepDays != 7 {
		t.Errorf("keepDays = %d, 期望 7", got.Settings.KeepDays)
	}
	if e.API.MaxUploadMB() != 64 {
		t.Errorf("API.MaxUploadMB() = %d, 期望 64", e.API.MaxUploadMB())
	}

	// 空 body 应 400 而不是静默成功。
	w = e.do(t, http.MethodPut, "/api/v1/settings", []byte(`{}`))
	wantStatus(t, w, http.StatusBadRequest)
}

// TestSettingsPersistAcrossRestart 验证 kv 持久化：新建的 API 实例
// 应从 kv 读回上一个实例写下的设置。
func TestSettingsPersistAcrossRestart(t *testing.T) {
	e := newTestEnv(t)

	w := e.do(t, http.MethodPut, "/api/v1/settings",
		[]byte(`{"maxUploadMB":5,"keepDays":3,"concurrency":4}`))
	wantStatus(t, w, http.StatusOK)

	// 用同一份 DB 重新装配一个 API，模拟重启。
	reborn := New(Deps{
		Log:         slog.Default(),
		St:          e.St,
		Blobs:       e.Blobs,
		Jobs:        e.Jobs,
		Providers:   testProviders(),
		Keys:        fakeKeys{},
		Cipher:      testCipher(t, e.Dir),
		MaxUploadMB: 20, // 注入值应被 kv 里的 5 覆盖
	})
	if reborn.MaxUploadMB() != 5 {
		t.Errorf("重启后 maxUploadMB = %d, 期望 5（kv 应覆盖注入值）", reborn.MaxUploadMB())
	}
	if reborn.KeepDays() != 3 {
		t.Errorf("重启后 keepDays = %d, 期望 3", reborn.KeepDays())
	}
}

// ----------------------------------------------------------------
// SSE
// ----------------------------------------------------------------

// TestJobEventsUnknownJob 断言未知任务的 SSE 请求返回 404，
// 而不是挂着一条永不产生事件的流。
func TestJobEventsUnknownJob(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/api/v1/jobs/j_nope/events", nil)
	wantStatus(t, w, http.StatusNotFound)
}

func TestJobEventsTerminalJobEmitsOneEvent(t *testing.T) {
	e := newTestEnv(t)

	// 直接插一条已完成的任务。
	j := &domain.Job{
		ID: "j_done", Provider: domain.ProviderAgnes, Model: "agnes-image-2.5-flash",
		Mode: domain.ModeTxt2Img, Prompt: "hi", Status: string(domain.StatusQueued), Count: 1,
	}
	if err := e.St.CreateJob(j); err != nil {
		t.Fatalf("建任务: %v", err)
	}
	if err := e.St.UpdateJobStatus(j.ID, domain.StatusDone, 100, time.Second, nil); err != nil {
		t.Fatalf("更新状态: %v", err)
	}

	w := e.do(t, http.MethodGet, "/api/v1/jobs/j_done/events", nil)
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, 期望 text/event-stream", ct)
	}
	if w.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("X-Accel-Buffering = %q, 期望 no", w.Header().Get("X-Accel-Buffering"))
	}

	body := w.Body.String()
	if !strings.Contains(body, "data: ") {
		t.Fatalf("终态任务应推送一条事件: %q", body)
	}
	// 只有一条 data 帧，不应刷屏。
	if n := strings.Count(body, "data: "); n != 1 {
		t.Errorf("data 帧数 = %d, 期望 1; body=%q", n, body)
	}
}

// TestStreamSSEStopsOnClosedChannel 是本次实现的关键回归点：
// Manager.broadcast 在终态会 close 订阅频道，读已关闭频道会立刻返回零值。
// 若不检测 ok==false 就退出，SSE 会无限刷屏空事件。
//
// 这里直接喂一个已关闭的频道，确定性地验证 handler 会立即结束循环。
func TestStreamSSEStopsOnClosedChannel(t *testing.T) {
	e := newTestEnv(t)

	ch := make(chan job.Event)
	close(ch) // 模拟 broadcast 在终态关闭订阅频道

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/j_x/events", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.API.streamSSE(w, req, ch)
	}()

	select {
	case <-done:
		// 正常：循环立即退出。
	case <-time.After(3 * time.Second):
		t.Fatal("streamSSE 没有在频道关闭后返回 —— 说明没有检测 ok==false，会刷屏空事件")
	}

	body := w.Body.String()
	if strings.Count(body, "data: ") != 0 {
		t.Errorf("关闭的频道不应产生任何 data 帧，实际: %q", body)
	}
}

// TestJobEventsEndsAfterTerminalEvent 走完整链路：提交一个真实任务，
// 上游快速失败，SSE 应在终态事件后自然结束而不是挂住。
func TestJobEventsEndsAfterTerminalEvent(t *testing.T) {
	e := newTestEnv(t)

	// 打桩上游：延迟一小会儿再返回 401，确保订阅发生在终态之前。
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer up.Close()

	// 把通道地址指到打桩服务。
	patch := e.do(t, http.MethodPatch, "/api/v1/providers/agnes",
		[]byte(`{"baseUrl":"`+up.URL+`"}`))
	wantStatus(t, patch, http.StatusOK)

	// 提交任务。
	created := e.do(t, http.MethodPost, "/api/v1/jobs", []byte(
		`{"provider":"agnes","model":"agnes-image-2.5-flash","prompt":"hi","mode":"txt2img","tier":"1K","aspect":"1:1","count":1}`))
	wantStatus(t, created, http.StatusAccepted)

	var cj createJobDTO
	if err := json.Unmarshal(created.Body.Bytes(), &cj); err != nil {
		t.Fatalf("解析建任务响应: %v", err)
	}
	if cj.JobID == "" {
		t.Fatalf("建任务没有返回 jobId: %s", created.Body.String())
	}

	// 订阅 SSE。
	srv := httptest.NewServer(e.API.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, srv.URL+"/api/v1/jobs/"+cj.JobID+"/events", nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("发起 SSE 请求: %v", err)
	}
	defer resp.Body.Close()
	wantHTTPStatus(t, resp.StatusCode, http.StatusOK)

	// 流应在终态事件后结束；若 handler 没检测频道关闭，这里会挂住。
	done := make(chan string, 1)
	go func() {
		raw, _ := io.ReadAll(resp.Body)
		done <- string(raw)
	}()

	select {
	case body := <-done:
		if !strings.Contains(body, "data: ") {
			t.Errorf("事件流里没有任何 data 帧: %q", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SSE 流在终态后没有结束 —— 说明没有处理频道关闭")
	}
}

func wantHTTPStatus(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("HTTP 状态码 = %d, 期望 %d", got, want)
	}
}

// ----------------------------------------------------------------
// Usage
// ----------------------------------------------------------------

// TestPagedLimitEchoMatchesStoreClamp 断言回显的 limit 与 store 实际使用的
// 翻页步长一致。store 的规则是「越界一律回到默认值」而非夹到边界：
//   ListJobs:   limit<=0 || limit>100  → 50
//   ListAssets: limit<=0 || limit>200  → 60
// 前端若拿到不一致的值，翻页会错位。
func TestPagedLimitEchoMatchesStoreClamp(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, 50}, {-5, 50}, {200, 50}, {101, 50}, {100, 100}, {1, 1},
	}
	for _, tc := range cases {
		if got := normLimit(tc.in); got != tc.want {
			t.Errorf("normLimit(%d) = %d, 期望 %d", tc.in, got, tc.want)
		}
	}

	assetCases := []struct {
		in   int
		want int
	}{{0, 60}, {-1, 60}, {500, 60}, {201, 60}, {200, 200}, {10, 10}}
	for _, tc := range assetCases {
		if got := normAssetLimit(tc.in); got != tc.want {
			t.Errorf("normAssetLimit(%d) = %d, 期望 %d", tc.in, got, tc.want)
		}
	}
}

func TestUsageEmpty(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, http.MethodGet, "/api/v1/usage", nil)
	wantStatus(t, w, http.StatusOK)

	var got usageDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析 usage: %v", err)
	}
	if got.Items == nil {
		t.Errorf("items 为 null，期望空数组")
	}
}
