package ai

import (
	"net/http"
	"strings"
	"testing"
)

func TestRedactTextCatchesSecretsAndPersonalData(t *testing.T) {
	r := NewRedactor("hunter2-super-secret")
	in := strings.Join([]string{
		"Authorization: Bearer abc.def.ghijklmnop",
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJl",
		"key sk_live_51Habcdefghijkl and sk-ant-api03-abcdefghijklmnopqrst",
		"aws AKIAABCDEFGHIJKLMNOP",
		"mail alice.smith+test@example.co.uk",
		"call +1 (415) 555-0123 or +447911123456",
		"card 4111 1111 1111 1111 and 4111111111111111",
		"password=hunter2&next=/home",
		`"api_key": "zzz-123"`,
		"session 3f6c1b2a9d8e7f60514233a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8",
		"configured hunter2-super-secret value",
	}, "\n")
	out := r.Text(in)
	for _, leak := range []string{
		"abc.def.ghijklmnop", "eyJhbGci", "sk_live_51H", "sk-ant-api03", "AKIAABCDEFGHIJKLMNOP",
		"alice.smith", "555-0123", "+447911123456", "4111 1111", "4111111111111111", "hunter2", "zzz-123", "3f6c1b2a9d8e",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in:\n%s", leak, out)
		}
	}
	for _, keep := range []string{"Authorization: Bearer [REDACTED]", "[EMAIL]", "[CARD]", "[PHONE]", "[JWT]", "next=/home"} {
		if !strings.Contains(out, keep) {
			t.Errorf("expected %q in:\n%s", keep, out)
		}
	}
}

func TestRedactKeepsOrdinaryValues(t *testing.T) {
	r := NewRedactor()
	in := `{"id": 42, "price": 160.95, "sku": "SL-000001", "orderId": 20001, "name": "Urban Red Yo-Yo", "createdAt": "2026-10-06T12:00:01Z", "count": 1234567890123}`
	out := r.Body([]byte(in), 0)
	for _, keep := range []string{"42", "160.95", "SL-000001", "20001", "Urban Red Yo-Yo", "2026-10-06T12:00:01Z"} {
		if !strings.Contains(out, keep) {
			t.Errorf("over-redacted %q: %s", keep, out)
		}
	}
}

func TestRedactSecretsOnlyKeepsEmailsInDocuments(t *testing.T) {
	r := NewRedactor()
	doc := "Log in as user0001@shoplab.test with header Authorization: Bearer abcdefgh12345678"
	out := r.Secrets(doc)
	if !strings.Contains(out, "user0001@shoplab.test") || strings.Contains(out, "abcdefgh12345678") {
		t.Errorf("documents keep emails but lose credentials: %s", out)
	}
}

func TestRedactJSONBodyBySensitiveKeys(t *testing.T) {
	r := NewRedactor()
	out := r.Body([]byte(`{"email":"bob@example.com","password":"pw","nested":{"accessToken":"t0k","cardNumber":"x"},"items":[{"sessionId":"abc"}],"author":"Maya"}`), 0)
	for _, leak := range []string{"bob@example.com", `"pw"`, "t0k", `"x"`, `"abc"`} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %s: %s", leak, out)
		}
	}
	if !strings.Contains(out, "Maya") {
		t.Errorf("author is not a secret: %s", out)
	}
}

func TestRedactHeadersURLsAndForms(t *testing.T) {
	r := NewRedactor()
	h := r.Headers(http.Header{"Authorization": {"Bearer x"}, "Cookie": {"sid=1"}, "X-Session-Token": {"abc"}, "Accept": {"application/json"}})
	if h["Authorization"] != redacted || h["Cookie"] != redacted || h["X-Session-Token"] != redacted || h["Accept"] != "application/json" {
		t.Errorf("headers: %v", h)
	}
	u := r.URL("http://shop.test/api/reset?token=abc123&email=a@b.co&page=2")
	if strings.Contains(u, "abc123") || strings.Contains(u, "a@b.co") || !strings.Contains(u, "page=2") {
		t.Errorf("url: %s", u)
	}
	f := r.Body([]byte("username=joe&password=hunter22&remember=1"), 0)
	if strings.Contains(f, "hunter22") || !strings.Contains(f, "remember=1") {
		t.Errorf("form: %s", f)
	}
}

func TestRedactRegisteredSecretsAndTruncate(t *testing.T) {
	r := NewRedactor()
	r.AddSecret("tok-from-login")
	r.AddSecret("ab") // too short to register
	if out := r.Text("GET /api/x?t=tok-from-login ab"); strings.Contains(out, "tok-from-login") || !strings.Contains(out, " ab") {
		t.Errorf("registered secret: %s", out)
	}
	s := Truncate(strings.Repeat("é", 100), 11)
	if !strings.HasPrefix(s, "ééééé…") || !strings.Contains(s, "truncated 190 bytes") {
		t.Errorf("truncate: %s", s)
	}
}

func TestThirdPartyGuard(t *testing.T) {
	for host, want := range map[string]string{
		"api.stripe.com":           "payment",
		"API.Stripe.com:443":       "payment",
		"uploads.api.stripe.com":   "payment",
		"api.twilio.com":           "sms",
		"www.recaptcha.net":        "captcha",
		"api.sendgrid.com":         "email",
		"api-m.sandbox.paypal.com": "",
		"checkout-test.adyen.com":  "",
		"localhost":                "",
		"shop.example.com":         "",
		"notstripe.com":            "",
	} {
		if got := ThirdPartyCategory(host); got != want {
			t.Errorf("%s: got %q, want %q", host, got, want)
		}
	}
	blocked, test := GuardList()
	if len(blocked) < 20 || len(test) < 5 {
		t.Errorf("guard list too small: %d %d", len(blocked), len(test))
	}
}
