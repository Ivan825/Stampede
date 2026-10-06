package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/Ivan825/Stampede/internal/metrics"
)

func TestBrowserSteps(t *testing.T) {
	if _, err := FindChrome(); err != nil {
		t.Skip(err)
	}
	var thirdParty atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { thirdParty.Add(1) }))
	defer other.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><title>Shop</title><h1>Shop</h1>
<img src="%s/pixel.gif" alt="">
<p>Welcome</p><a id="buy" href="/cart">Buy</a>`, other.URL)
	})
	mux.HandleFunc("GET /cart", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Cart</title><h1>Cart</h1>
<form action="/done" method="get"><input id="email" name="email"><button>Pay</button></form>`)
	})
	mux.HandleFunc("GET /done", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><title>Done</title><p class="msg">Thanks %s</p>`, r.URL.Query().Get("email"))
	})
	mux.HandleFunc("GET /missing", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gone", http.StatusNotFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := mustHost(t, srv.URL)

	out := run(t, fmt.Sprintf(`
metadata: {name: browser}
target: {baseURL: %q}
journeys:
  - name: buy
    steps:
      - browser: /
        timeout: 15s
        steps:
          - waitFor: h1
          - click: "#buy"
          - waitFor: "#email"
          - fill: {"#email": "ana@shop.test"}
          - press: Enter
          - waitFor: .msg
          - assert: {".msg": "Thanks ana@shop.test"}
          - goto: /missing
load: {iterations: 2, vus: 1}`, srv.URL), func(o *Options) {
		o.AllowHost = func(u *url.URL) bool { return u.Host == host }
	})
	tot := out.total.Totals()
	if tot.Failed > 0 && out.total.Steps[0] != nil && out.total.Steps[0].Errors["browser unavailable"] > 0 {
		t.Fatalf("Chrome did not start (set STAMPEDE_CHROME_NO_SANDBOX=true where its sandbox is unavailable): %v", tot.Errors)
	}
	ids := map[string]int{}
	for _, st := range out.prog.Steps {
		ids[st.Name] = st.ID
	}
	for _, name := range []string{"browser /", "waitFor h1", "click #buy", "fill #email", "press Enter", "assert .msg"} {
		st := out.total.Steps[ids[name]]
		if st == nil || st.Requests != 2 || st.Failed != 0 {
			t.Errorf("step %q: %+v (errors %v)", name, st, tot.Errors)
		}
	}
	load := out.total.Steps[ids["browser /"]]
	if load == nil {
		t.Fatalf("no stats for the page load; errors %v", tot.Errors)
	}
	if load.PhaseSum[metrics.PhaseLoad] == 0 || load.PhaseSum[metrics.PhaseFCP] == 0 {
		t.Errorf("the page load must record load and FCP: %v", load.PhaseSum)
	}
	if as := out.total.Steps[ids["assert .msg"]]; as.ChecksPassed != 2 {
		t.Errorf("assert checks passed %d", as.ChecksPassed)
	}
	if miss := out.total.Steps[ids["goto /missing"]]; miss.Failed != 2 || miss.Errors["HTTP 404"] != 2 {
		t.Errorf("goto /missing: %+v", miss)
	}
	if n := thirdParty.Load(); n != 0 {
		t.Errorf("the page reached a host outside the policy %d times", n)
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}
