package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestScriptStep(t *testing.T) {
	var mu sync.Mutex
	paths := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/cart" {
			fmt.Fprint(w, `{"items": [{"price": 2.5, "qty": 2}, {"price": 10, "qty": 1}]}`)
		}
	}))
	defer srv.Close()
	out := run(t, fmt.Sprintf(`
metadata: {name: script}
target: {baseURL: %q}
journeys:
  - name: a
    steps:
      - get: /cart
        extract: {cart: "$"}
      - script: |
          let total = 0;
          for (const it of vars.cart.items) total += it.price * it.qty;
          vars.total = total;
          if (iteration %% 2 === 1) fail("odd iteration");
        sets: [total]
      - get: /pay/${total}
load: {iterations: 10, vus: 1}`, srv.URL), nil)
	tot := out.total.Totals()
	// Half the iterations fail in the script, before paying.
	if paths["/cart"] != 10 || paths["/pay/15"] != 5 || tot.Requests != 15 {
		t.Fatalf("paths %v requests %d", paths, tot.Requests)
	}
	if out.total.Journeys[0].Failed != 5 {
		t.Errorf("journey failures %d", out.total.Journeys[0].Failed)
	}
}
