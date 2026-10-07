// Package keyring encrypts secrets at rest with AES-256-GCM envelope
// encryption: every value gets a fresh data key, and the data key is
// encrypted ("wrapped") with the server's master key. Rotating the master
// key only requires re-wrapping data keys.
package keyring

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Keyring holds the master key.
type Keyring struct {
	master cipher.AEAD
	id     string
	root   []byte // HKDF input for Derive
}

// ErrNoKey means no master key was configured.
var ErrNoKey = errors.New("no master key: set STAMPEDE_MASTER_KEY (32 bytes, base64) or STAMPEDE_MASTER_KEY_FILE")

// New builds a keyring from a 32-byte key.
func New(key []byte) (*Keyring, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(key)
	return &Keyring{master: aead, id: hex.EncodeToString(sum[:4]), root: bytes.Clone(key)}, nil
}

// FromEnv reads STAMPEDE_MASTER_KEY (base64) or STAMPEDE_MASTER_KEY_FILE.
func FromEnv() (*Keyring, error) {
	raw := os.Getenv("STAMPEDE_MASTER_KEY")
	if f := os.Getenv("STAMPEDE_MASTER_KEY_FILE"); raw == "" && f != "" {
		b, err := os.ReadFile(f) //nolint:gosec // the operator chooses the key file
		if errors.Is(err, os.ErrNotExist) && os.Getenv("STAMPEDE_MASTER_KEY_AUTOGEN") == "true" {
			// First start of a self-contained install (Docker Compose): create
			// the key on the data volume. Back this file up; secrets cannot be
			// decrypted without it.
			key := GenerateKey()
			if err := os.WriteFile(f, []byte(key+"\n"), 0o600); err != nil { //nolint:gosec // the operator chooses the key file
				return nil, fmt.Errorf("create master key file: %w", err)
			}
			b, err = []byte(key), nil
		}
		if err != nil {
			return nil, fmt.Errorf("read master key file: %w", err)
		}
		raw = string(b)
	}
	if c := os.Getenv("STAMPEDE_MASTER_KEY_COMMAND"); raw == "" && c != "" {
		out, err := runKeyCommand(c)
		if err != nil {
			return nil, err
		}
		raw = out
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNoKey
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("master key is not valid base64: %w", err)
	}
	return New(key)
}

// KeyCommandTimeout bounds STAMPEDE_MASTER_KEY_COMMAND.
const KeyCommandTimeout = 30 * time.Second

// runKeyCommand runs a shell command that prints the base64 master key,
// such as a cloud KMS or secret manager CLI, so the key never sits in the
// environment or on disk.
func runKeyCommand(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), KeyCommandTimeout)
	defer cancel()
	shell, flag := "/bin/sh", "-c"
	if runtime.GOOS == "windows" {
		shell, flag = "cmd", "/C"
	}
	cmd := exec.CommandContext(ctx, shell, flag, command) //nolint:gosec // the operator chooses the command
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("STAMPEDE_MASTER_KEY_COMMAND failed: %w: %s", err, msg)
	}
	return string(out), nil
}

// GenerateKey returns a new random master key, base64 encoded.
func GenerateKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// Derive returns n bytes derived from the master key for one purpose
// (HKDF-SHA256 with the purpose as info). Different purposes give
// independent keys, and the master key cannot be recovered from them.
func (k *Keyring) Derive(purpose string, n int) []byte {
	b, err := hkdf.Key(sha256.New, k.root, nil, purpose, n)
	if err != nil {
		panic(err) // only for n beyond 255*32
	}
	return b
}

// KeyID identifies the master key (a short hash), stored with each secret
// so a rotation can find values wrapped with an older key.
func (k *Keyring) KeyID() string { return k.id }

// Sealed is an encrypted value.
type Sealed struct {
	Ciphertext []byte // nonce || AES-GCM(dataKey, plaintext)
	WrappedKey []byte // nonce || AES-GCM(master, dataKey)
	KeyID      string
}

// Seal encrypts plaintext. aad binds the ciphertext to its context (for
// example the project and secret name) so it cannot be swapped elsewhere.
func (k *Keyring) Seal(plaintext, aad []byte) (Sealed, error) {
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return Sealed{}, err
	}
	ct, err := sealWith(dataKey, plaintext, aad)
	if err != nil {
		return Sealed{}, err
	}
	nonce := make([]byte, k.master.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, err
	}
	wrapped := k.master.Seal(nonce, nonce, dataKey, []byte(k.id))
	return Sealed{Ciphertext: ct, WrappedKey: wrapped, KeyID: k.id}, nil
}

// Open decrypts a sealed value.
func (k *Keyring) Open(s Sealed, aad []byte) ([]byte, error) {
	if s.KeyID != k.id {
		return nil, fmt.Errorf("secret was sealed with master key %s, current key is %s", s.KeyID, k.id)
	}
	ns := k.master.NonceSize()
	if len(s.WrappedKey) < ns {
		return nil, errors.New("wrapped key too short")
	}
	dataKey, err := k.master.Open(nil, s.WrappedKey[:ns], s.WrappedKey[ns:], []byte(k.id))
	if err != nil {
		return nil, errors.New("cannot unwrap data key: wrong master key or tampered data")
	}
	return openWith(dataKey, s.Ciphertext, aad)
}

func sealWith(key, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

func openWith(key, ct, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ct) < aead.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	pt, err := aead.Open(nil, ct[:aead.NonceSize()], ct[aead.NonceSize():], aad)
	if err != nil {
		return nil, errors.New("cannot decrypt secret: tampered data or wrong context")
	}
	return pt, nil
}
