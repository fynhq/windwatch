package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	key, err := LoadOrCreateKey(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	cipher, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	encoded, err := cipher.Encrypt("socks5-password")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if encoded == "socks5-password" {
		t.Fatal("value was not encrypted")
	}
	plaintext, err := cipher.Decrypt(encoded)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plaintext != "socks5-password" {
		t.Fatalf("Decrypt = %q", plaintext)
	}

	otherKey, err := LoadOrCreateKey(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatalf("second LoadOrCreateKey: %v", err)
	}
	otherCipher, err := NewCipher(otherKey)
	if err != nil {
		t.Fatalf("second NewCipher: %v", err)
	}
	if _, err := otherCipher.Decrypt(encoded); err == nil {
		t.Fatal("expected decryption with a different key to fail")
	}
}

func TestLoadOrCreateKeyIsStableAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "secret.key")
	first, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	second, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("second LoadOrCreateKey: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("key should persist between loads")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key permissions = %o", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if _, err := LoadOrCreateKey(path); err != nil {
		t.Fatalf("LoadOrCreateKey loose permissions: %v", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("Stat after reload: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key permissions were not tightened: %o", info.Mode().Perm())
	}
}
