package httpx

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRetryableStatus(t *testing.T) {
	retryable := []int{408, 429, 500, 502, 503, 504}
	for _, c := range retryable {
		if !RetryableStatus(c) {
			t.Errorf("状态码 %d 应当可重试", c)
		}
	}
	// 客户端错误与成功码都不该重试。
	for _, c := range []int{200, 400, 401, 402, 403, 404, 413, 422} {
		if RetryableStatus(c) {
			t.Errorf("状态码 %d 不应重试", c)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	if got := retryAfter("30"); got != 30*time.Second {
		t.Errorf("retryAfter(30) = %v, want 30s", got)
	}
	if got := retryAfter(""); got != 0 {
		t.Errorf("空值应返回 0, got %v", got)
	}
	if got := retryAfter("garbage"); got != 0 {
		t.Errorf("非法值应返回 0, got %v", got)
	}
	// 过去的 HTTP-date 不应产生负等待。
	if got := retryAfter("Wed, 21 Oct 2015 07:28:00 GMT"); got != 0 {
		t.Errorf("过去的日期应返回 0, got %v", got)
	}
}

func TestBackoffGrowsAndIsBounded(t *testing.T) {
	c := New(nil)
	c.Base, c.Cap = 100*time.Millisecond, 2*time.Second

	prev := time.Duration(0)
	for i := 0; i < 12; i++ {
		d := c.backoff(i)
		if d < time.Millisecond {
			t.Fatalf("attempt %d 退避 %v 低于下限", i, d)
		}
		if d > 2*c.Cap {
			t.Fatalf("attempt %d 退避 %v 突破上限", i, d)
		}
		if i > 0 {
			_ = prev
		}
		prev = d
	}
	// 高 attempt 必须被夹在 Cap 的抖动区间内。
	if d := c.backoff(50); d > 2*c.Cap {
		t.Errorf("极大 attempt 未被夹住: %v", d)
	}
}

func TestCrossHostRedirectRefused(t *testing.T) {
	via := []*http.Request{mustReq("https://api.example.com/v1/x")}
	err := CrossHostSafeRedirect(mustReq("https://evil.example/v1/x"), via)
	if err == nil {
		t.Fatal("跨主机重定向应当被拒绝")
	}
	// 跨主机忠诚携带 Permanent 标记，避免浪费退避重试。
	if got := err.Error(); !strings.Contains(got, "permanent") && !strings.Contains(got, "永久") {
		t.Errorf("错误应标记 permanent: %v", got)
	}
}

func TestSameHostRedirectAllowed(t *testing.T) {
	via := []*http.Request{mustReq("https://api.example.com/v1/x")}
	if err := CrossHostSafeRedirect(mustReq("https://api.example.com/v1/y"), via); err != nil {
		t.Errorf("同主机重定向不应被拒绝: %v", err)
	}
}

func TestTooManyRedirectsRefused(t *testing.T) {
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = mustReq("https://api.example.com/v1")
	}
	if err := CrossHostSafeRedirect(mustReq("https://api.example.com/v1"), via); err == nil {
		t.Error("重定向次数过多应当被拒绝")
	}
}

func TestMaskSecrets(t *testing.T) {
	cases := []string{
		"Authorization: Bearer sk-abcdefgh12345678",
		"key=xoxb-1234567890-abcdef",
		"AKIAIOSFODNN7EXAMPLE",
	}
	for _, c := range cases {
		got := MaskSecrets(c)
		if got == c {
			t.Errorf("未脱敏: %s", c)
		}
		if !strings.Contains(got, "***") {
			t.Errorf("脱敏结果缺少掩码: %s", got)
		}
	}
	// 普通文本不应被误伤。
	if MaskSecrets("model=gpt-image-2.5-flash") != "model=gpt-image-2.5-flash" {
		t.Error("普通文本被误伤")
	}
}

func TestReadBodyEnforcesLimit(t *testing.T) {
	got, err := ReadBody(strings.NewReader("hello"), 1024)
	if err != nil || string(got) != "hello" {
		t.Errorf("ReadBody = %q, %v", got, err)
	}
	if _, err := ReadBody(strings.NewReader(strings.Repeat("x", 100)), 10); err == nil {
		t.Error("超限应当报错")
	}
}

func mustReq(u string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		panic(err)
	}
	return req
}