package agnes

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/imgen/imgen/internal/domain"
)

func baseReq(t *testing.T) domain.Request {
	t.Helper()
	return domain.Request{
		Model:  "agnes-image-2.5-flash",
		Prompt: "一只海豹",
		Mode:   domain.ModeTxt2Img,
		Tier:   domain.Tier2K,
		Aspect: domain.Aspect16_9,
	}
}

func TestTranslateTxt2Img(t *testing.T) {
	p := New("")
	pl, err := p.Translate(baseReq(t), nil)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	// 文生图与图生图同端点。
	if pl.Method != http.MethodPost || pl.Path != "/images/generations" {
		t.Errorf("路由 = %s %s, want POST /images/generations", pl.Method, pl.Path)
	}

	b := pl.Body.(body)
	if b.Size != "2K" || b.Ratio != "16:9" {
		t.Errorf("size/ratio = %q/%q, want 2K/16:9", b.Size, b.Ratio)
	}
	if b.Extra.ResponseFormat != "b64_json" {
		t.Errorf("response_format = %q, 期望固定 b64_json", b.Extra.ResponseFormat)
	}
	if len(b.Extra.Image) != 0 {
		t.Errorf("文生图不应带参考图")
	}
}

// TestResponseFormatNeverTopLevel 锁定一个已知错误用法：
// 官方文档明确把顶层 response_format 列为错误示例，必须放在 extra_body 内。
func TestResponseFormatNeverTopLevel(t *testing.T) {
	p := New("")
	pl, _ := p.Translate(baseReq(t), nil)
	raw, err := json.Marshal(pl.Body)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if _, bad := probe["response_format"]; bad {
		t.Error("response_format 出现在顶层，这是官方列出的错误用法")
	}
	extra, ok := probe["extra_body"].(map[string]any)
	if !ok || extra["response_format"] != "b64_json" {
		t.Errorf("extra_body.response_format 缺失, got %v", probe)
	}
}

func TestTranslateImg2ImgUsesExtraBody(t *testing.T) {
	p := New("")
	r := baseReq(t)
	r.Mode = domain.ModeImg2Img
	refs := []string{"data:image/png;base64,AAA", "https://example.com/b.png"}

	pl, err := p.Translate(r, refs)
	if err != nil {
		t.Fatal(err)
	}
	// 端点与文生图相同。
	if pl.Path != "/images/generations" {
		t.Errorf("路径 = %s", pl.Path)
	}
	b := pl.Body.(body)
	if len(b.Extra.Image) != 2 {
		t.Fatalf("extra_body.image 数量 = %d, want 2", len(b.Extra.Image))
	}
	if b.Extra.Image[0] != refs[0] {
		t.Error("参考图顺序应保持原序")
	}
	if len(b.TopLevelImage) != 0 {
		t.Error("默认模式下不应使用顶层 image 字段")
	}
}

// TestTopLevelImageToggle 覆盖文档矛盾的另一分支，
// 使 M0 实测后只需翻转开关，无需改代码。
func TestTopLevelImageToggle(t *testing.T) {
	p := New("")
	p.TopLevelImage = true
	r := baseReq(t)
	r.Mode = domain.ModeImg2Img

	pl, err := p.Translate(r, []string{"https://example.com/a.png"})
	if err != nil {
		t.Fatal(err)
	}
	b := pl.Body.(body)
	if len(b.TopLevelImage) != 1 {
		t.Errorf("顶层 image 数量 = %d, want 1", len(b.TopLevelImage))
	}
	if len(b.Extra.Image) != 0 {
		t.Error("开关打开后不应重复写入 extra_body.image")
	}
}

func TestTranslateRejectsBadTier(t *testing.T) {
	p := New("")
	r := baseReq(t)
	r.Tier = "8K"
	if _, err := p.Translate(r, nil); err == nil {
		t.Error("非法档位应当报错")
	}
}

func TestTranslateRejectsBadRatio(t *testing.T) {
	p := New("")
	r := baseReq(t)
	r.Aspect = "5:4"
	if _, err := p.Translate(r, nil); err == nil {
		t.Error("非法宽高比应当报错")
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

func TestCapabilitiesReflectUpstream(t *testing.T) {
	c := New("").Capabilities("agnes-image-2.5-flash")
	if c.NoWatermark || c.PromptExtend {
		t.Error("Agnes 无 watermark / prompt_extend 参数，能力声明不应为真")
	}
	if len(c.OutputFormats) != 1 || c.OutputFormats[0] != domain.FormatPNG {
		t.Errorf("输出格式 = %v, 上游固定 png", c.OutputFormats)
	}
	if c.ExactSize {
		t.Error("Agnes 用档位+ratio，不是精确像素")
	}
}

func TestExtract(t *testing.T) {
	p := New("")
	raw := []byte(`{"created":1780000000,"data":[
	  {"url":"https://storage.googleapis.com/agnes-aigc/x.png","b64_json":null,"revised_prompt":"改写后的提示词"}]}`)

	got, err := p.Extract(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.URLs) != 1 {
		t.Errorf("urls = %v", got.URLs)
	}
	if got.RevisedPrompt != "改写后的提示词" {
		t.Errorf("revised_prompt = %q", got.RevisedPrompt)
	}
	if got.Usage.ImagesCount != 1 {
		t.Errorf("images_count = %d", got.Usage.ImagesCount)
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
		{400, `{"error":{"message":"bad param"}}`, domain.CodeUpstreamRejected, false},
		{401, `{}`, domain.CodeUpstreamAuth, false},
		{402, `{}`, domain.CodeUpstreamQuota, false},
		{403, `{}`, domain.CodeUpstreamAuth, false},
		{413, `{}`, domain.CodeUpstreamRejected, false},
		{422, `{"error":{"message":"bad image url"}}`, domain.CodeUpstreamRejected, false},
		{429, `{}`, domain.CodeUpstreamQuota, true},
		{500, `{}`, domain.CodeUpstreamUnavailable, true},
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
	}
}

// 该通道以纯 HTTP 状态码表达错误，需确认 402 被归到配额而非参数错误。
func TestPaymentRequiredIsQuota(t *testing.T) {
	p := New("")
	got := p.Classify(http.StatusPaymentRequired, []byte(`{}`))
	if got.Code != domain.CodeUpstreamQuota {
		t.Errorf("402 code = %s, want %s", got.Code, domain.CodeUpstreamQuota)
	}
	if got.Retryable {
		t.Error("余额不足时重试无意义，retryable 应为 false")
	}
}
