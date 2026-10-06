package keyring

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"testing"
)

func TestSealOpen(t *testing.T) {
	key, _ := base64.StdEncoding.DecodeString(GenerateKey())
	k, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := k.Seal([]byte("hunter2"), []byte("project/a:DB_PASSWORD"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(s.Ciphertext, []byte("hunter2")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	pt, err := k.Open(s, []byte("project/a:DB_PASSWORD"))
	if err != nil || string(pt) != "hunter2" {
		t.Fatalf("open: %q %v", pt, err)
	}
	if _, err := k.Open(s, []byte("project/b:DB_PASSWORD")); err == nil {
		t.Error("opening under a different context must fail")
	}
	s.Ciphertext[len(s.Ciphertext)-1] ^= 1
	if _, err := k.Open(s, []byte("project/a:DB_PASSWORD")); err == nil {
		t.Error("tampered ciphertext must fail")
	}

	other, _ := base64.StdEncoding.DecodeString(GenerateKey())
	k2, _ := New(other)
	s2, _ := k.Seal([]byte("x"), nil)
	if _, err := k2.Open(s2, nil); err == nil {
		t.Error("a different master key must not open the secret")
	}
	if _, err := New([]byte("short")); err == nil {
		t.Error("short key accepted")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("STAMPEDE_MASTER_KEY", "")
	t.Setenv("STAMPEDE_MASTER_KEY_FILE", "")
	if _, err := FromEnv(); !errors.Is(err, ErrNoKey) {
		t.Errorf("want ErrNoKey, got %v", err)
	}
	t.Setenv("STAMPEDE_MASTER_KEY", GenerateKey())
	if _, err := FromEnv(); err != nil {
		t.Error(err)
	}
}

func TestAutogenKeyFile(t *testing.T) {
	path := t.TempDir() + "/master.key"
	t.Setenv("STAMPEDE_MASTER_KEY", "")
	t.Setenv("STAMPEDE_MASTER_KEY_FILE", path)
	t.Setenv("STAMPEDE_MASTER_KEY_AUTOGEN", "true")
	k1, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := FromEnv()
	if err != nil || k1.KeyID() != k2.KeyID() {
		t.Fatalf("second start must reuse the generated key: %v", err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v", fi.Mode().Perm())
	}
}
