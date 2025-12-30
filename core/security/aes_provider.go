package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// AESSecretProvider 实现基于 AES-GCM 的加解密
type AESSecretProvider struct {
	key []byte
}

// NewAESSecretProvider 创建新的 AES Secret Provider
// keyStr 必须是 16, 24, 或 32 字节长的字符串（对应 AES-128, AES-192, AES-256）
func NewAESSecretProvider(keyStr string) (*AESSecretProvider, error) {
	key := []byte(keyStr)
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, fmt.Errorf("invalid key length: %d. Must be 16, 24, or 32 bytes", len(key))
	}
	return &AESSecretProvider{key: key}, nil
}

func (p *AESSecretProvider) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(p.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (p *AESSecretProvider) Decrypt(ciphertextBase64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertextBase64)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(p.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// IsBase64 简单的 Base64 格式判断
func IsBase64(s string) bool {
	_, err := base64.StdEncoding.DecodeString(s)
	return err == nil
}
