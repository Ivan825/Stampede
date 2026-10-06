package ai

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Redaction placeholders.
const (
	redacted     = "[REDACTED]"
	redactedMail = "[EMAIL]"
	redactedTel  = "[PHONE]"
	redactedCard = "[CARD]"
	redactedJWT  = "[JWT]"
)

// Redactor removes secrets and personal data from anything that is sent to
// a model provider. Recorded traffic (HAR files, access logs, dry-run
// traces) gets full redaction: credentials, tokens, cookies, emails, phone
// numbers and card numbers. Documents the user wrote on purpose (the
// OpenAPI spec and the description) only lose credentials, since they
// often name test accounts the model needs.
type Redactor struct {
	mu    sync.RWMutex
	known []string
}

// NewRedactor redacts the given exact values (secrets, extracted tokens)
// wherever they appear, plus everything the patterns catch.
func NewRedactor(known ...string) *Redactor {
	r := &Redactor{}
	for _, k := range known {
		r.AddSecret(k)
	}
	return r
}

// AddSecret registers an exact value to redact. Values shorter than 4
// characters are ignored to avoid erasing ordinary text.
func (r *Redactor) AddSecret(v string) {
	v = strings.TrimSpace(v)
	if len(v) < 4 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.known {
		if k == v {
			return
		}
	}
	r.known = append(r.known, v)
	// Longest first so a value containing another is replaced whole.
	sort.Slice(r.known, func(i, j int) bool { return len(r.known[i]) > len(r.known[j]) })
}

var (
	jwtRe     = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)
	schemeRe  = regexp.MustCompile(`(?i)\b(bearer|basic|token|digest)(\s+)[A-Za-z0-9._~+/=-]{8,}`)
	keyLikeRe = regexp.MustCompile(`\b(?:(?:sk|pk|rk)_(?:live|test)_[A-Za-z0-9]{8,}|sk-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,}|stp_[A-Za-z0-9_]{10,})`)
	kvRe      = regexp.MustCompile(`(?i)\b((?:[a-z]+[_-]?)?(?:password|passwd|pwd|secret|token|api[_-]?key|apikey|session[_-]?id|access[_-]?key|client[_-]?secret|signature))(["']?\s*[:=]\s*["']?)([^"'&\s,;}]+)`)
	hexRe     = regexp.MustCompile(`\b[A-Fa-f0-9]{32,}\b`)
	b64Re     = regexp.MustCompile(`[A-Za-z0-9+/_-]{40,}={0,2}`)
	emailRe   = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	phoneRe   = regexp.MustCompile(`(?:\+\d{1,3}[\s.-]?)?\(?\b\d{3}\)?[\s.-]\d{3}[\s.-]\d{4}\b|\+\d{10,15}\b`)
	cardRe    = regexp.MustCompile(`\b(?:\d{4}[ -]\d{4}[ -]\d{4}[ -]\d{1,7}|\d{15,16})\b`)
)

func (r *Redactor) knownValues(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, k := range r.known {
		if strings.Contains(s, k) {
			s = strings.ReplaceAll(s, k, redacted)
		}
	}
	return s
}

// Secrets removes credentials only: registered values, JWTs, bearer and
// basic credentials, well-known API key formats and key=value pairs whose
// key names a secret.
func (r *Redactor) Secrets(s string) string {
	s = r.knownValues(s)
	s = jwtRe.ReplaceAllString(s, redactedJWT)
	s = schemeRe.ReplaceAllString(s, "${1}${2}"+redacted)
	s = keyLikeRe.ReplaceAllString(s, redacted)
	s = kvRe.ReplaceAllString(s, "${1}${2}"+redacted)
	return s
}

// Text applies full redaction to recorded traffic.
func (r *Redactor) Text(s string) string {
	s = r.Secrets(s)
	s = emailRe.ReplaceAllString(s, redactedMail)
	s = cardRe.ReplaceAllStringFunc(s, func(m string) string {
		if luhn(m) {
			return redactedCard
		}
		return m
	})
	s = phoneRe.ReplaceAllString(s, redactedTel)
	s = hexRe.ReplaceAllString(s, redacted)
	s = b64Re.ReplaceAllStringFunc(s, func(m string) string {
		// Long runs of letters only are words or slugs, not secrets.
		if strings.IndexFunc(m, func(c rune) bool { return c >= '0' && c <= '9' }) < 0 {
			return m
		}
		return redacted
	})
	return s
}

func luhn(s string) bool {
	sum, n, double := 0, 0, false
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c < '0' || c > '9' {
			continue
		}
		d := int(c - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		n++
		double = !double
	}
	return n >= 13 && sum%10 == 0
}

var sensitiveContains = []string{
	"password", "passwd", "secret", "token", "apikey", "authorization", "cookie", "credential",
	"sessionid", "cardnumber", "ccnumber", "cvv", "cvc", "privatekey", "signature", "accesskey", "otp",
}

var sensitiveExact = map[string]bool{"auth": true, "session": true, "sid": true, "pwd": true, "pin": true, "ssn": true, "card": true, "pan": true}

// SensitiveName reports whether a header, field or parameter name usually
// holds a credential or payment data.
func SensitiveName(name string) bool {
	n := strings.ToLower(name)
	n = strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(n)
	if sensitiveExact[n] {
		return true
	}
	for _, s := range sensitiveContains {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// Header redacts one header value.
func (r *Redactor) Header(name, value string) string {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "x-auth-token", "x-csrf-token", "x-xsrf-token":
		return redacted
	}
	if SensitiveName(name) {
		return redacted
	}
	return r.Text(value)
}

// Headers redacts a header set into a flat map, one value per name.
func (r *Redactor) Headers(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, vs := range h {
		out[http.CanonicalHeaderKey(k)] = r.Header(k, strings.Join(vs, ", "))
	}
	return out
}

// URL redacts query parameters with sensitive names and personal data in
// the rest of the URL.
func (r *Redactor) URL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return r.Text(raw)
	}
	q := u.Query()
	for k, vs := range q {
		for i := range vs {
			if SensitiveName(k) {
				vs[i] = redacted
			} else {
				vs[i] = r.Text(vs[i])
			}
		}
	}
	u.RawQuery = ""
	base := r.Text(u.String())
	enc := q.Encode()
	// Keep placeholders readable.
	enc = strings.NewReplacer("%5BREDACTED%5D", redacted, "%5BEMAIL%5D", redactedMail, "%5BPHONE%5D", redactedTel, "%5BCARD%5D", redactedCard, "%5BJWT%5D", redactedJWT).Replace(enc)
	return base + "?" + enc
}

// Body redacts a request or response body and truncates it to limit bytes
// (0 means no limit). JSON bodies are redacted field by field so values
// under sensitive keys disappear even when they look harmless.
func (r *Redactor) Body(b []byte, limit int) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	var v any
	if (s[0] == '{' || s[0] == '[') && json.Unmarshal([]byte(s), &v) == nil {
		v = r.jsonValue(v)
		if out, err := json.Marshal(v); err == nil {
			s = string(out)
		}
	} else if strings.Contains(s, "=") && !strings.ContainsAny(s, " \n<") {
		// A form body.
		if q, err := url.ParseQuery(s); err == nil {
			for k, vs := range q {
				for i := range vs {
					if SensitiveName(k) {
						vs[i] = redacted
					} else {
						vs[i] = r.Text(vs[i])
					}
				}
			}
			s = q.Encode()
		}
		s = r.Text(s)
	} else {
		s = r.Text(s)
	}
	return Truncate(s, limit)
}

func (r *Redactor) jsonValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if SensitiveName(k) {
				x[k] = redacted
				continue
			}
			x[k] = r.jsonValue(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = r.jsonValue(e)
		}
		return x
	case string:
		return r.Text(x)
	}
	return v
}

// Truncate shortens s to at most limit bytes on a rune boundary, noting
// how much was cut. limit <= 0 leaves s alone.
func Truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncated " + itoa(len(s)-cut) + " bytes]"
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
