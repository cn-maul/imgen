package provider

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// DecodeBase64 解码图片 base64，兼容 data URI 前缀与 URL-safe 变体。
// 上游返回的 b64_json 未必带 data: 前缀，两种都要接受。
func DecodeBase64(b64 string) ([]byte, error) {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return nil, errors.New("空的 base64 内容")
	}
	if strings.HasPrefix(b64, "data:") {
		i := strings.Index(b64, ",")
		if i < 0 {
			return nil, errors.New("data URI 缺少逗号分隔符")
		}
		b64 = b64[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		if raw, err = base64.RawStdEncoding.DecodeString(b64); err != nil {
			return nil, fmt.Errorf("base64 解码失败: %w", err)
		}
	}
	if len(raw) == 0 {
		return nil, errors.New("解码后内容为空")
	}
	return raw, nil
}
