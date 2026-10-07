package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorExamples(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=abc")
		w.WriteHeader(503)
		fmt.Fprintf(w, "upstream down; you sent key %s and token %s", r.URL.Query().Get("key"), r.Header.Get("X-Token"))
	}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: ex}
target: {baseURL: %q}
journeys:
  - name: a
    steps:
      - post: /pay?key=${secret.KEY}
        headers: {Authorization: "Bearer ${secret.KEY}", X-Token: "${secret.KEY}"}
        json: {amount: 5, card: "${secret.KEY}"}
load: {iterations: 10, vus: 1}`, srv.URL), func(o *Options) { o.Secrets = map[string]string{"KEY": "sk_live_12345"} })
	st := out.total.Steps[0]
	exs := st.Examples["HTTP 503"]
	if st.Failed != 10 || len(exs) != 3 {
		t.Fatalf("failed %d, %d examples: %v", st.Failed, len(exs), st.Examples)
	}
	e := exs[0]
	all := fmt.Sprint(e)
	if strings.Contains(all, "sk_live_12345") {
		t.Fatalf("secret leaked: %+v", e)
	}
	if e.Status != 503 || !strings.HasPrefix(e.Request, "POST ") || !strings.Contains(e.Request, "key=%5Bredacted%5D") && !strings.Contains(e.Request, "key=[redacted]") {
		t.Errorf("request %q status %d", e.Request, e.Status)
	}
	if e.RequestHeaders["Authorization"] != "[redacted]" || e.ResponseHeaders["Set-Cookie"] != "[redacted]" {
		t.Errorf("headers %v / %v", e.RequestHeaders, e.ResponseHeaders)
	}
	if !strings.Contains(e.RequestBody, `"amount":5`) || !strings.Contains(e.ResponseBody, "upstream down") || len(e.TraceID) != 32 {
		t.Errorf("bodies %q / %q trace %q", e.RequestBody, e.ResponseBody, e.TraceID)
	}
}
