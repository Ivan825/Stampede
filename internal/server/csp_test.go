package server

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCSPHashesInlineScripts(t *testing.T) {
	script := "document.documentElement.dataset.theme='dark'"
	ui := fstest.MapFS{"index.html": {Data: []byte("<html><script>" + script + "</script><script type=module src=/a.js></script></html>")}}
	csp := contentSecurityPolicy(ui)
	sum := sha256.Sum256([]byte(script))
	if !strings.Contains(csp, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") {
		t.Errorf("inline script hash missing: %s", csp)
	}
	if strings.Count(csp, "sha256-") != 1 || strings.Contains(csp, "unsafe-inline';") && strings.Contains(csp, "script-src 'self' blob: 'unsafe-inline'") {
		t.Errorf("unexpected policy: %s", csp)
	}
}
