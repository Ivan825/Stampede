package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPasswords(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected encoding %s", h)
	}
	if !CheckPassword(h, "correct horse battery") || CheckPassword(h, "correct horse batterY") {
		t.Error("check wrong")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("salts must differ")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Error("short password accepted")
	}
	for _, bad := range []string{"", "$argon2i$v=19$m=1,t=1,p=1$a$b", "$argon2id$v=19$garbage$a$b"} {
		if CheckPassword(bad, "x") {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTokens(t *testing.T) {
	tok, hash := NewToken(APITokenPrefix)
	if !strings.HasPrefix(tok, "stp_") || len(tok) < 40 {
		t.Errorf("token %q", tok)
	}
	if !bytes.Equal(HashToken(tok), hash) {
		t.Error("hash mismatch")
	}
	tok2, _ := NewToken(APITokenPrefix)
	if tok == tok2 {
		t.Error("tokens must be random")
	}
}

func TestRoles(t *testing.T) {
	if !RoleAdmin.AtLeast(RoleEditor) || RoleRunner.AtLeast(RoleEditor) || !RoleViewer.AtLeast(RoleViewer) {
		t.Error("ranking wrong")
	}
	if Lower(RoleOwner, RoleRunner) != RoleRunner || Lower(RoleViewer, RoleAdmin) != RoleViewer {
		t.Error("Lower wrong")
	}
	if Role("root").Valid() {
		t.Error("unknown role valid")
	}
	var p *Principal
	if p.Can(RoleViewer) {
		t.Error("nil principal can do nothing")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.Allowed("a") {
			t.Fatal("blocked too early")
		}
		l.Fail("a")
	}
	if l.Allowed("a") {
		t.Error("should be blocked after 3 failures")
	}
	if !l.Allowed("b") {
		t.Error("other keys unaffected")
	}
	now = now.Add(61 * time.Second)
	if !l.Allowed("a") {
		t.Error("window should expire")
	}
	l.Fail("a")
	l.Reset("a")
	if !l.Allowed("a") {
		t.Error("reset should clear")
	}
}
