package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const cipherPrefix = "v1:"

type Cipher struct {
	aead cipher.AEAD
}

func LoadOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("secret key must be 32 bytes, got %d", len(key))
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("set secret key permissions: %w", err)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create secret key directory: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write secret key: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("set secret key permissions: %w", err)
	}
	return key, nil
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate encryption nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, ciphertext...)
	return cipherPrefix + base64.RawStdEncoding.EncodeToString(payload), nil
}

func (c *Cipher) Decrypt(encoded string) (string, error) {
	if !strings.HasPrefix(encoded, cipherPrefix) {
		return "", errors.New("unsupported encrypted value")
	}
	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, cipherPrefix))
	if err != nil {
		return "", fmt.Errorf("decode encrypted value: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(payload) < nonceSize {
		return "", errors.New("encrypted value is truncated")
	}
	plaintext, err := c.aead.Open(nil, payload[:nonceSize], payload[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt value: %w", err)
	}
	return string(plaintext), nil
}
