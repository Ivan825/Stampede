package banklab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func start(t *testing.T, fixes string) (*server, string) {
	s, h := newServer(labkit.Config{Fast: true, Fixes: labkit.ParseFixes(fixes)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func call(t *testing.T, method, u, token, key, body string) (int, http.Header, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, resp.Header, m
}

func login(t *testing.T, base string, n int) string {
	t.Helper()
	code, _, m := call(t, "POST", base+"/api/login", "", "", fmt.Sprintf(`{"customerId":"c%04d","password":%q}`, n, Password))
	if code != 200 {
		t.Fatalf("login: %d %v", code, m)
	}
	return m["token"].(string)
}

func transferBody(from, to int, amount int, ref string) string {
	return fmt.Sprintf(`{"fromAccount":"acc_%04d_cur","toAccount":"acc_%04d_cur","amount":%d,"currency":"EUR","reference":%q}`, from, to, amount, ref)
}

func balanced(t *testing.T, base string) map[string]any {
	t.Helper()
	_, _, m := call(t, "GET", base+"/api/ledger/check", "", "", "")
	if m["balanced"] != true || m["duplicates"] != float64(0) {
		t.Fatalf("ledger: %v", m)
	}
	return m
}

func TestIdempotencyKey(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		_, base := start(t, fixes)
		tok := login(t, base, 1)
		code, _, first := call(t, "POST", base+"/api/transfers", tok, "k1", transferBody(1, 2, 500, "rent share"))
		if code != 201 || first["status"] != "completed" {
			t.Fatalf("transfer: %d %v", code, first)
		}
		code, h, again := call(t, "POST", base+"/api/transfers", tok, "k1", transferBody(1, 2, 500, "rent share"))
		if code != 201 || h.Get("Idempotent-Replayed") != "true" || again["id"] != first["id"] {
			t.Fatalf("replay: %d %v %v", code, h, again)
		}
		if code, _, _ := call(t, "POST", base+"/api/transfers", tok, "k1", transferBody(1, 2, 900, "rent share")); code != 422 {
			t.Fatalf("a reused key with another body: %d", code)
		}
		if code, _, _ := call(t, "POST", base+"/api/transfers", tok, "", transferBody(1, 2, 500, "x")); code != 400 {
			t.Fatalf("no key: %d", code)
		}
		if code, _, _ := call(t, "POST", base+"/api/transfers", tok, "k2", transferBody(2, 1, 500, "x")); code != 403 {
			t.Fatalf("someone else's account: %d", code)
		}
		if code, _, m := call(t, "POST", base+"/api/transfers", tok, "k3", transferBody(1, 2, 1_000_000, "too much")); code != 422 {
			t.Fatalf("insufficient funds: %d %v", code, m)
		}

		// Twenty clients retry the same payment at once: one moves money,
		// the rest get it replayed or are told to retry.
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				code, _, _ := call(t, "POST", base+"/api/payments", tok, "race", `{"fromAccount":"acc_0001_cur","payee":"electric","amount":4200,"reference":"oct bill"}`)
				if code != 201 && code != 409 {
					t.Errorf("racing payment: %d", code)
				}
			}()
		}
		wg.Wait()
		_, _, found := call(t, "GET", base+"/api/transfers?reference=oct+bill", tok, "", "")
		if n := len(found["transfers"].([]any)); n != 1 {
			t.Fatalf("fixes %q: %d payments for one key", fixes, n)
		}
		balanced(t, base)
	}
}

// Money is only ever moved: whatever runs concurrently, the ledger sums
// to zero and each balance is the sum of its postings.
func TestNoMoneyMadeOrLost(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		s, base := start(t, fixes)
		var wg sync.WaitGroup
		for i := 1; i <= 30; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tok := login(t, base, i)
				for k := range 10 {
					call(t, "POST", base+"/api/transfers", tok, fmt.Sprintf("k%d", k), transferBody(i, i%30+1, 100*(k+1), ""))
				}
			}()
		}
		wg.Wait()
		m := balanced(t, base)
		if m["transfers"] != float64(300) {
			t.Fatalf("fixes %q: %v transfers, want 300", fixes, m["transfers"])
		}
		for _, a := range s.accounts {
			var sum int64
			for _, p := range a.postings {
				sum += p.amount
			}
			if sum != a.balance {
				t.Fatalf("%s: postings %d, balance %d", a.ID, sum, a.balance)
			}
		}
	}
}

func TestLockBottleneck(t *testing.T) {
	const fraud = 10 * time.Millisecond
	elapsed := map[string]time.Duration{}
	for _, fixes := range []string{"", "lock"} {
		s, base := start(t, fixes)
		s.fraud = fraud
		// Everyone signs in first, then all transfers start together.
		toks := map[int]string{}
		for i := 1; i <= 40; i += 2 {
			toks[i] = login(t, base, i)
		}
		var wg sync.WaitGroup
		begin := make(chan struct{})
		for i := 1; i <= 40; i += 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-begin
				call(t, "POST", base+"/api/transfers", toks[i], "k", transferBody(i, i+1, 100, ""))
			}()
		}
		t0 := time.Now()
		close(begin)
		wg.Wait()
		elapsed[fixes] = time.Since(t0)
		if fixes == "" && s.inflight.Peak() != 1 {
			t.Errorf("without the fix %d transfers were in the ledger at once", s.inflight.Peak())
		}
	}
	// Under the ledger lock the 20 fraud checks run one after another;
	// with the fix they overlap. The bound is loose for busy CI machines.
	if elapsed[""] < 20*fraud || elapsed["lock"] > elapsed[""]/2 {
		t.Errorf("20 transfers took %s with the lock and %s without", elapsed[""], elapsed["lock"])
	}
}

func TestBalanceBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "balance"} {
		s, base := start(t, fixes)
		tok := login(t, base, 7)
		history := len(s.accounts["acc_0007_cur"].postings)
		before := s.summed.Load()
		call(t, "GET", base+"/api/accounts/acc_0007_cur", tok, "", "")
		got := s.summed.Load() - before
		if fixes == "" && got != int64(history) {
			t.Errorf("a balance read added up %d postings, want the account's %d", got, history)
		}
		if fixes != "" && got != 0 {
			t.Errorf("with the fix a balance read added up %d postings", got)
		}
	}
}

func TestStatementsBottleneck(t *testing.T) {
	var statements []map[string]any
	for _, fixes := range []string{"", "statements"} {
		s, base := start(t, fixes)
		tok := login(t, base, 9)
		_, _, list := call(t, "GET", base+"/api/accounts/acc_0009_cur/statements", tok, "", "")
		month := list["statements"].([]any)[0].(map[string]any)["month"].(string)
		code, _, st := call(t, "GET", base+"/api/accounts/acc_0009_cur/statements/"+month, tok, "", "")
		if code != 200 {
			t.Fatalf("statement: %d %v", code, st)
		}
		in, out := st["moneyIn"].(float64), st["moneyOut"].(float64)
		if st["closingBalance"].(float64) != st["openingBalance"].(float64)+in-out || len(st["transactions"].([]any)) < 10 {
			t.Fatalf("statement does not add up: %v", st)
		}
		statements = append(statements, st)
		got := s.scanned.Load()
		if fixes == "" && got != int64(len(s.journal)) {
			t.Errorf("without the fix a statement scanned %d postings, want the whole journal (%d)", got, len(s.journal))
		}
		if fixes != "" && got != 0 {
			t.Errorf("with the fix the journal was scanned (%d)", got)
		}
	}
	if !reflect.DeepEqual(statements[0], statements[1]) {
		t.Fatal("the fix changed the statement")
	}
}
