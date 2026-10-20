// Package secret 负责 API Key 的加密存储。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Cipher 使用 AES-256-GCM 加解密凭据。
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher 由 base64 主密钥构造。主密钥须为 32 字节（base64 编码后 44 字符）。
func NewCipher(masterKeyB64 string) (*Cipher, error) {
	if masterKeyB64 == "" {
		return nil, errors.New("主密钥为空")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(masterKeyB64))
	if err != nil {
		return nil, fmt.Errorf("主密钥不是合法 base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("主密钥长度应为 32 字节，实际 %d", len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// NewCipherFromEnv 读取 IMGEN_MASTER_KEY；未配置时在 dataDir 生成并持久化。
// 生成的主密钥仅存于本地文件，权限 0600。
func NewCipherFromEnv(envKey, dataDir string) (*Cipher, error) {
	if envKey != "" {
		return NewCipher(envKey)
	}

	keyFile := filepath.Join(dataDir, "master.key")
	if raw, err := os.ReadFile(keyFile); err == nil {
		return NewCipher(strings.TrimSpace(string(raw)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取主密钥: %w", err)
	}

	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return nil, fmt.Errorf("生成主密钥: %w", err)
	}
	enc := base64.StdEncoding.EncodeToString(raw)
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyFile, []byte(enc), 0o600); err != nil {
		return nil, fmt.Errorf("保存主密钥: %w", err)
	}
	return NewCipher(enc)
}

// Encrypt 返回 nonce‖密文。
func (c *Cipher) Encrypt(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("待加密内容为空")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plain, nil), nil
}

// Decrypt 解析 Encrypt 的输出。
func (c *Cipher) Decrypt(sealed []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(sealed) < ns+1 {
		return nil, errors.New("密文长度不足")
	}
	plain, err := c.aead.Open(nil, sealed[:ns], sealed[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("解密失败（主密钥是否已变更）: %w", err)
	}
	return plain, nil
}

// Mask 返回用于前端展示的掩码，绝不回传明文。
func Mask(key string) string {
	k := strings.TrimSpace(key)
	if len(k) == 0 {
		return ""
	}
	if len(k) <= 8 {
		return strings.Repeat("*", len(k))
	}
	return k[:4] + strings.Repeat("*", 6) + k[len(k)-4:]
}