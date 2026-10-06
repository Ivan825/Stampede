// Package auth handles passwords, session and API tokens, roles and the
// identity of the caller.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP recommendation: m=19 MiB, t=2, p=1).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// MinPasswordLen is the shortest accepted password.
const MinPasswordLen = 10

// HashPassword returns an encoded argon2id hash.
func HashPassword(pw string) (string, error) {
	if len(pw) < MinPasswordLen {
		return "", fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// CheckPassword reports whether pw matches an encoded hash, in constant
// time with respect to the hash contents.
func CheckPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[4])
	want, err2 := enc.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want))) //nolint:gosec // len(want) is a small key length
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked when a login names an unknown user, so the response
// time does not reveal which emails exist.
var dummyHash, _ = HashPassword("not-a-real-password-for-timing")

// CheckDummy burns the same time as a real password check.
func CheckDummy(pw string) { _ = CheckPassword(dummyHash, pw) }

// Token prefixes.
const (
	APITokenPrefix = "stp_"
	SessionPrefix  = "sts_"
)

// NewToken returns a random token with a prefix and its SHA-256 hash.
// Only the hash is stored.
func NewToken(prefix string) (token string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token = prefix + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

// HashToken hashes a presented token for lookup. Tokens are 256-bit random
// values, so a fast hash is sufficient.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
