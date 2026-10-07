package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRunRefusesThirdParties: a run whose scenario calls a payment, SMS,
// email or CAPTCHA provider is refused unless the host is one of the
// target's allowed hosts.
func TestRunRefusesThirdParties(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt, sc map[string]any
	c.do("POST", "/projects", map[string]string{"name": "p"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)
	c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: pay}
journeys:
  - name: checkout
    steps:
      - get: /cart
      - name: sms
        post: https://api.twilio.com/2010-04-01/Messages.json
load: {iterations: 1}`}, &sc)
	var e errBody
	code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &e)
	if code != 403 || e.Error.Code != "third_party" || len(e.Error.Details) != 1 || e.Error.Details[0] != "journey checkout, step sms calls api.twilio.com, a sms provider" {
		t.Fatalf("third-party call: %d %+v", code, e)
	}
	// Allow-listing the host on the target lets the run start.
	if code := c.do("PATCH", "/targets/"+tgt["id"].(string), map[string]any{"name": "t", "baseURL": target.URL, "allowHosts": []string{"api.twilio.com"}}, nil); code != 200 {
		t.Fatalf("allow host: %d", code)
	}
	if code := c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &e); code != 201 {
		t.Errorf("allow-listed: %d %+v", code, e)
	}
	if !strings.Contains(e.Error.Message, "allowed hosts") {
		t.Errorf("message should say how to allow it: %q", e.Error.Message)
	}
}
