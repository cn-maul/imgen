package secret

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testCipher(t *testing.T) *Cipher {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	c, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	c := testCipher(t)
	plain := []byte("sk-abcdefghijklmnop1234567890")

	sealed, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("密文中不应出现明文片段")
	}
	got, err := c.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("解密结果 = %q, want %q", got, plain)
	}
}

func TestNonceIsUnique(t *testing.T) {
	c := testCipher(t)
	a, _ := c.Encrypt([]byte("same"))
	b, _ := c.Encrypt([]byte("same"))
	if bytes.Equal(a, b) {
		t.Error("相同明文两次加密结果不应相同（nonce 未随机）")
	}
}

func TestDecryptRejectsTampered(t *testing.T) {
	c := testCipher(t)
	sealed, _ := c.Encrypt([]byte("secret"))
	sealed[len(sealed)-1] ^= 0xff
	if _, err := c.Decrypt(sealed); err == nil {
		t.Error("被篡改的密文应当解密失败")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	a := testCipher(t)
	b, _ := NewCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 32)))
	sealed, _ := a.Encrypt([]byte("secret"))
	if _, err := b.Decrypt(sealed); err == nil {
		t.Error("错误主密钥应当解密失败")
	}
}

func TestNewCipherValidatesKey(t *testing.T) {
	if _, err := NewCipher(""); err == nil {
		t.Error("空主密钥应当报错")
	}
	if _, err := NewCipher("not-base64!!!"); err == nil {
		t.Error("非法 base64 应当报错")
	}
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	if _, err := NewCipher(short); err == nil {
		t.Error("非 32 字节主密钥应当报错")
	}
}

func TestMaskNeverLeaksMiddle(t *testing.T) {
	got := Mask("sk-proj-1234567890abcdef")
	if got == "sk-proj-1234567890abcdef" {
		t.Fatal("掩码不应等于明文")
	}
	if len(got) >= 16 {
		t.Errorf("掩码过长，可能泄漏信息: %q", got)
	}
	if got[:3] != "sk-" {
		t.Errorf("掩码应保留前缀用于识别: %q", got)
	}
	if Mask("") != "" {
		t.Error("空输入应返回空掩码")
	}
}