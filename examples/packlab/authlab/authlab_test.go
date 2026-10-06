package authlab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func post(t *testing.T, u string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(u, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Error(err)
		return 0, nil
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func login(t *testing.T, base string) map[string]any {
	code, m := post(t, base+"/oauth/token", url.Values{"grant_type": {"password"}, "username": {"user0001@authlab.test"}, "password": {Password}, "client_id": {"web"}})
	if code != 200 {
		t.Fatalf("login: %d %v", code, m)
	}
	return m
}

func TestTokenFlow(t *testing.T) {
	_, h := newServer(labkit.Config{Fast: true})
	srv := httptest.NewServer(h)
	defer srv.Close()
	m := login(t, srv.URL)
	req, _ := http.NewRequest("GET", srv.URL+"/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+m["access_token"].(string))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("userinfo: %v %v", err, resp)
	}
	resp.Body.Close()
	refresh := m["refresh_token"].(string)
	code, m2 := post(t, srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"web"}})
	if code != 200 {
		t.Fatalf("refresh: %d %v", code, m2)
	}
	// Reusing the rotated token fails and revokes the family.
	if code, _ := post(t, srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"web"}}); code != 400 {
		t.Fatalf("reuse: %d", code)
	}
	if code, _ := post(t, srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {m2["refresh_token"].(string)}, "client_id": {"web"}}); code != 400 {
		t.Fatalf("family not revoked: %d", code)
	}
	if code, m := post(t, srv.URL+"/oauth/token", url.Values{"grant_type": {"password"}, "username": {"user0001@authlab.test"}, "password": {"nope"}, "client_id": {"web"}}); code != 400 || m["error"] != "invalid_grant" {
		t.Fatalf("wrong password: %d %v", code, m)
	}
}

// Without the lock fix, password hashes are checked one at a time.
// Wrong passwords pay for the hash too, which is all this needs.
func TestLockBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes string
		one   bool
	}{{"", true}, {"lock", false}} {
		s, h := newServer(labkit.Config{Fast: true, Fixes: labkit.ParseFixes(tc.fixes)})
		s.iters = 50000
		srv := httptest.NewServer(h)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				post(t, srv.URL+"/oauth/token", url.Values{"grant_type": {"password"}, "username": {"user0001@authlab.test"}, "password": {"x"}, "client_id": {"web"}})
			}()
		}
		wg.Wait()
		srv.Close()
		if p := s.hashing.Peak(); (p == 1) != tc.one {
			t.Errorf("fixes %q: %d hashes at once", tc.fixes, p)
		}
	}
}

func TestIndexBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "index"} {
		s, h := newServer(labkit.Config{Fast: true, Fixes: labkit.ParseFixes(fixes)})
		srv := httptest.NewServer(h)
		for range 50 {
			login(t, srv.URL)
		}
		m := login(t, srv.URL)
		post(t, srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {m["refresh_token"].(string)}, "client_id": {"web"}})
		srv.Close()
		got := s.scanned.Load()
		if fixes == "" && got != 51 {
			t.Errorf("without the index a refresh scans every token issued: scanned %d, want 51", got)
		}
		if fixes == "index" && got != 0 {
			t.Errorf("with the index nothing is scanned, got %d", got)
		}
	}
}
