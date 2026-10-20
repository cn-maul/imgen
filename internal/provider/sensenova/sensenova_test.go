package sensenova

import (
	"net/http"
	"testing"

	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/provider"
)

// TestSizeTable 锁定 (档位, 宽高比) → 像素尺寸的映射。
// 上游要求 W/H 为 32 的倍数、单边 ≤4096、宽高比 ≤3:1，任一条被破坏都会导致 400，
// 因此该表必须有测试兜底。
func TestSizeTable(t *testing.T) {
	cases := []struct {
		tier   domain.Tier
		aspect domain.Aspect
		w, h   int
	}{
		{domain.Tier2K, domain.Aspect16_9, 2720, 1536},
		{domain.Tier4K, domain.Aspect1_1, 4096, 4096},
		{domain.Tier1K, domain.Aspect9_16, 736, 1312},
	}

	for _, c := range cases {
		w, h, err := SizeFor(c.tier, c.aspect)
		if err != nil {
			t.Fatalf("SizeFor(%s,%s): %v", c.tier, c.aspect, err)
		}
		if w != c.w || h != c.h {
			t.Errorf("SizeFor(%s,%s) = %dx%d, want %dx%d", c.tier, c.aspect, w, h, c.w, c.h)
		}
	}

	// 遍历全表校验上游硬约束。
	for tier, byAspect := range recTable {
		for aspect := range byAspect {
			w, h, err := deriveSize(tier, aspect)
			if err != nil {
				t.Fatalf("deriveSize(%s,%s): %v", tier, aspect, err)
			}
			if w%32 != 0 || h%32 != 0 {
				t.Errorf("%s/%s = %dx%d 不是 32 的倍数", tier, aspect, w, h)
			}
			if w > 4096 || h > 4096 {
				t.Errorf("%s/%s = %dx%d 超过上游单边 4096 上限", tier, aspect, w, h)
			}
			if w < 512 || h < 512 {
				t.Errorf("%s/%s = %dx%d 低于上游单边 512 下限", tier, aspect, w, h)
			}
			long, short := w, h
			if short > long {
				long, short = short, long
			}
			if long > short*3 {
				t.Errorf("%s/%s = %dx%d 宽高比超过 3:1", tier, aspect, w, h)
			}
		}
	}
}

func TestSizeForRejectsUnknown(t *testing.T) {
	if _, _, err := SizeFor("8K", domain.Aspect1_1); err == nil {
		t.Error("未知档位应当报错")
	}
}

func baseReq(t *testing.T) domain.Request {
	t.Helper()
	return domain.Request{
		Model:  "sensenova-u1.5-fast",
		Prompt: "一只海豹",
		Mode:   domain.ModeTxt2Img,
		Tier:   domain.Tier2K,
		Aspect: domain.Aspect16_9,
	}
}

func TestTranslateTxt2Img(t *testing.T) {
	p := New("")
	r := baseReq(t)
	r.NoWatermark = true
	r.OutputFormat = domain.FormatJPEG

	pl, err := p.Translate(r, nil)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if pl.Method != http.MethodPost || pl.Path != "/images/generations" {
		t.Errorf("路由 = %s %s, want POST /images/generations", pl.Method, pl.Path)
	}

	b := pl.Body.(body)
	if b.Size != "2720x1536" {
		t.Errorf("size = %q, want 2720x1536", b.Size)
	}
	if b.N != 1 {
		t.Errorf("n = %d, 上游仅支持 1", b.N)
	}
	if b.Watermark {
		t.Error("NoWatermark=true 时 watermark 应为 false")
	}
	if b.ResponseFormat != "b64_json" {
		t.Errorf("response_format = %q, 期望固定 b64_json 以规避 url 过期", b.ResponseFormat)
	}
	if b.OutputFormat != domain.FormatJPEG {
		t.Errorf("output_format = %q, want jpeg", b.OutputFormat)
	}
	if len(b.Images) != 0 {
		t.Errorf("文生图不应带 images, got %d", len(b.Images))
	}
}

func TestTranslateAlwaysSendsWatermark(t *testing.T) {
	p := New("")
	pl, _ := p.Translate(baseReq(t), nil) // NoWatermark 默认 false
	b := pl.Body.(body)
	if !b.Watermark {
		t.Error("必须始终显式下发 watermark（默认值公测后可能变化）")
	}
}

func TestTranslateEditUsesSeparateEndpoint(t *testing.T) {
	p := New("")
	r := baseReq(t)
	r.Mode = domain.ModeImg2Img
	refs := []string{"https://example.com/a.png", "data:image/png;base64,AAA"}

	pl, err := p.Translate(r, refs)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if pl.Path != "/images/edits" {
		t.Errorf("编辑应走独立端点, got %s", pl.Path)
	}
	b := pl.Body.(body)
	if len(b.Images) != 2 {
		t.Fatalf("参考图数量 = %d, want 2", len(b.Images))
	}
	if b.Images[0].ImageURL != refs[0] {
		t.Error("首张参考图未按原序传递（首张即主图）")
	}
}

func TestTranslateRejectsTooManyRefs(t *testing.T) {
	p := New("")
	r := baseReq(t)
	r.Mode = domain.ModeCompose
	refs := make([]string, 6)
	if _, err := p.Translate(r, refs); err == nil {
		t.Error("超过 5 张参考图应当报错")
	}
}

func TestTranslateRejectsEmptyPrompt(t *testing.T) {
	p := New("")
	r := baseReq(t)
	r.Prompt = "   "
	if _, err := p.Translate(r, nil); err == nil {
		t.Error("空 prompt 应当报错")
	}
}

func TestTranslateImg2ImgRequiresRefs(t *testing.T) {
	p := New("")
	r := baseReq(t)
	r.Mode = domain.ModeImg2Img
	if _, err := p.Translate(r, nil); err == nil {
		t.Error("图生图缺少参考图应当报错")
	}
}

func TestExtractB64(t *testing.T) {
	p := New("")
	raw := []byte(`{"created":1780000000,"data":[{"b64_json":"AAAA"},{"b64_json":"BBBB"}],
	  "usage":{"input_tokens":1540,"output_tokens":4096,"images_count":2}}`)

	got, err := p.Extract(raw)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.B64) != 2 {
		t.Fatalf("b64 数量 = %d, want 2", len(got.B64))
	}
	if got.Usage.InputTokens != 1540 || got.Usage.ImagesCount != 2 {
		t.Errorf("usage = %+v", got.Usage)
	}
	if got.Empty() {
		t.Error("不应为空")
	}
}

func TestExtractURLFallback(t *testing.T) {
	p := New("")
	raw := []byte(`{"data":[{"url":"https://cdn.example/x.png"}]}`)
	got, err := p.Extract(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.URLs) != 1 {
		t.Errorf("urls = %v", got.URLs)
	}
}

func TestExtractRejectsNonJSON(t *testing.T) {
	p := New("")
	if _, err := p.Extract([]byte("<html>502</html>")); err == nil {
		t.Error("非 JSON 响应应当报错")
	}
}

func TestClassify(t *testing.T) {
	p := New("")
	cases := []struct {
		status   int
		raw      string
		wantCode string
		retry    bool
	}{
		{401, `{"error":{"message":"bad key"}}`, domain.CodeUpstreamAuth, false},
		{403, `{}`, domain.CodeUpstreamAuth, false},
		{402, `{}`, domain.CodeUpstreamRejected, false},
		{429, `{"error":{"type":"quota_exceeded_error","message":"slow down"}}`, domain.CodeUpstreamQuota, true},
		{400, `{"error":{"message":"bad size"}}`, domain.CodeUpstreamRejected, false},
		{500, `{}`, domain.CodeUpstreamUnavailable, true},
		{503, `{}`, domain.CodeUpstreamUnavailable, true},
		{504, `{}`, domain.CodeUpstreamTimeout, true},
	}
	for _, c := range cases {
		got := p.Classify(c.status, []byte(c.raw))
		if got.Code != c.wantCode {
			t.Errorf("Classify(%d) code = %s, want %s", c.status, got.Code, c.wantCode)
		}
		if got.Retryable != c.retry {
			t.Errorf("Classify(%d) retryable = %v, want %v", c.status, got.Retryable, c.retry)
		}
		if got.Provider != "sensenova" {
			t.Errorf("Classify(%d) provider = %s", c.status, got.Provider)
		}
	}
}

func TestDecodeBase64(t *testing.T) {
	if _, err := provider.DecodeBase64(""); err == nil {
		t.Error("空输入应当报错")
	}
	if _, err := provider.DecodeBase64("!!!not base64!!!"); err == nil {
		t.Error("非法 base64 应当报错")
	}
	got, err := provider.DecodeBase64("data:image/png;base64,QUJD")
	if err != nil {
		t.Fatalf("data URI 解码失败: %v", err)
	}
	if string(got) != "ABC" {
		t.Errorf("data URI 解码 = %q, want ABC", got)
	}
}
