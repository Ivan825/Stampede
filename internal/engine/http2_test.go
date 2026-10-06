package engine

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTP2FromScenario(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.Proto) })
	tlsSrv := httptest.NewUnstartedServer(handler)
	tlsSrv.EnableHTTP2 = true
	tlsSrv.StartTLS()
	defer tlsSrv.Close()

	h2cSrv := httptest.NewUnstartedServer(handler)
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	h2cSrv.Config.Protocols = p
	h2cSrv.Start()
	defer h2cSrv.Close()

	tests := []struct {
		name string
		url  string
		http string
		want string
	}{
		{"http2 over TLS", tlsSrv.URL, "{http2: true, insecureSkipVerify: true}", "HTTP/2.0"},
		{"TLS without http2", tlsSrv.URL, "{insecureSkipVerify: true}", "HTTP/1.1"},
		{"h2c prior knowledge", h2cSrv.URL, "{h2c: true}", "HTTP/2.0"},
		{"cleartext default", h2cSrv.URL, "{}", "HTTP/1.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := run(t, fmt.Sprintf(`
metadata: {name: h2}
target: {baseURL: %q, http: %s}
journeys:
  - name: a
    steps:
      - get: /
        check: {bodyContains: %q}
load: {iterations: 5}`, tc.url, tc.http, tc.want), nil)
			st := out.total.Steps[0]
			if st == nil || st.Failed != 0 || st.Protocols[tc.want] != 5 {
				t.Fatalf("want 5 %s requests, got %+v", tc.want, st)
			}
		})
	}
}
