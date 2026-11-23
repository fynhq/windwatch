package secret

import (
	"strings"
	"testing"
)

func TestPurposeCipherBindsAssociatedData(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	blob, err := c.EncryptWithPurpose("proxy-form", "secret-value")
	if err != nil {
		t.Fatalf("EncryptWithPurpose: %v", err)
	}
	if !strings.HasPrefix(blob, "v2:") {
		t.Fatalf("purpose blob prefix = %q", blob[:3])
	}
	plain, err := c.DecryptWithPurpose("proxy-form", blob)
	if err != nil || plain != "secret-value" {
		t.Fatalf("DecryptWithPurpose = %q err=%v", plain, err)
	}
	if _, err := c.DecryptWithPurpose("other-purpose", blob); err == nil {
		t.Fatal("wrong purpose accepted")
	}
	if _, err := c.Decrypt(blob); err == nil {
		t.Fatal("legacy Decrypt accepted a purpose-bound blob")
	}
	legacy, err := c.Encrypt("legacy-value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c.DecryptWithPurpose("proxy-form", legacy); err == nil {
		t.Fatal("purpose decrypt accepted a legacy blob")
	}
	plain, err = c.Decrypt(legacy)
	if err != nil || plain != "legacy-value" {
		t.Fatalf("legacy Decrypt = %q err=%v", plain, err)
	}
}
