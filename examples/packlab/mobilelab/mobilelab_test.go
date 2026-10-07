package mobilelab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func start(t *testing.T, fixes string) (*server, string) {
	s, h := newServer(labkit.Config{Fast: true, Fixes: labkit.ParseFixes(fixes)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func call(t *testing.T, method, u, token, body string, hdr ...string) (int, http.Header, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Version", LatestVersion)
	req.Header.Set("X-Platform", "ios")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
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

func login(t *testing.T, base, user string) string {
	t.Helper()
	code, _, m := call(t, "POST", base+"/api/v1/auth/login", "", fmt.Sprintf(`{"userId":%q,"password":%q}`, user, Password))
	if code != 200 {
		t.Fatalf("login: %d %v", code, m)
	}
	return m["accessToken"].(string)
}

const home = `query Home { home { greeting streak today(limit: 5) { id title done } suggestions { title } } }`

func TestLaunch(t *testing.T) {
	_, base := start(t, "")
	code, _, cfg := call(t, "GET", base+"/api/v1/config", "", "", "X-Device-Id", "dev-1")
	if code != 200 || len(cfg["features"].(map[string]any)) != flags {
		t.Fatalf("config: %d %v", code, cfg["minVersion"])
	}
	if code, _, m := call(t, "GET", base+"/api/v1/me", "", "", "X-App-Version", "2.9.1"); code != 426 || m["minVersion"] != MinVersion {
		t.Fatalf("old app: %d %v", code, m)
	}
	tok := login(t, base, "u00042")
	dev := `{"deviceId":"dev-1","platform":"ios","pushToken":"apns-abc"}`
	if code, _, _ := call(t, "POST", base+"/api/v1/devices", tok, dev); code != 201 {
		t.Fatalf("register device: %d", code)
	}
	if code, _, _ := call(t, "POST", base+"/api/v1/devices", tok, dev); code != 200 {
		t.Fatalf("register again: %d", code)
	}
	code, _, sync := call(t, "POST", base+"/api/v1/sync", tok, `{"since":""}`)
	if code != 200 || len(sync["changes"].([]any)) < 2 || !strings.HasPrefix(sync["next"].(string), "v") {
		t.Fatalf("sync: %d %v", code, sync["next"])
	}
	// Automatic persisted query: the hash alone first, then with the query.
	sum := sha256.Sum256([]byte(home))
	ext := fmt.Sprintf(`"extensions":{"persistedQuery":{"version":1,"sha256Hash":%q}}`, hex.EncodeToString(sum[:]))
	_, _, miss := call(t, "POST", base+"/api/v1/graphql", tok, `{"operationName":"Home",`+ext+`}`)
	if !strings.Contains(fmt.Sprint(miss["errors"]), "PersistedQueryNotFound") {
		t.Fatalf("persisted miss: %v", miss)
	}
	q, _ := json.Marshal(home)
	_, _, full := call(t, "POST", base+"/api/v1/graphql", tok, `{"operationName":"Home","query":`+string(q)+`,`+ext+`}`)
	h := full["data"].(map[string]any)["home"].(map[string]any)
	today := h["today"].([]any)
	if len(today) == 0 || len(today) > 5 || h["greeting"] == nil {
		t.Fatalf("home: %v", full)
	}
	_, _, hit := call(t, "POST", base+"/api/v1/graphql", tok, `{"operationName":"Home",`+ext+`}`)
	if hit["errors"] != nil {
		t.Fatalf("persisted hit: %v", hit)
	}
	id := today[0].(map[string]any)["id"].(string)
	_, _, tog := call(t, "POST", base+"/api/v1/graphql", tok, fmt.Sprintf(`{"query":"mutation { toggleItem(id: \"%s\") { id done } }"}`, id))
	if tog["data"].(map[string]any)["toggleItem"].(map[string]any)["done"] != true {
		t.Fatalf("toggle: %v", tog)
	}
}

func TestConfigBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "config"} {
		s, base := start(t, fixes)
		var etag string
		for i := range 10 {
			_, h, _ := call(t, "GET", base+"/api/v1/config", "", "", "X-Device-Id", fmt.Sprintf("dev-%d", i))
			etag = h.Get("ETag")
		}
		want := int64(10)
		if fixes != "" {
			want = 1
		}
		if n := s.parses.Load(); n != want {
			t.Errorf("fixes %q: ten launches parsed the config %d times, want %d", fixes, n, want)
		}
		if fixes != "" {
			if code, _, _ := call(t, "GET", base+"/api/v1/config", "", "", "X-Device-Id", "dev-9", "If-None-Match", etag); code != 304 {
				t.Errorf("revalidation: %d", code)
			}
		}
	}
}

func TestSyncBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "sync"} {
		s, base := start(t, fixes)
		tok := login(t, base, "u00100")
		_, _, first := call(t, "POST", base+"/api/v1/sync", tok, `{"since":""}`)
		all := len(first["changes"].([]any))
		id := first["changes"].([]any)[0].(map[string]any)["id"].(string)
		before := s.serialized.Load()
		body := fmt.Sprintf(`{"since":%q,"changes":[{"id":%q,"done":true}]}`, first["next"], id)
		_, _, second := call(t, "POST", base+"/api/v1/sync", tok, body)
		got := len(second["changes"].([]any))
		if fixes == "" && got != all {
			t.Errorf("without the fix a delta sync sent %d items, want all %d", got, all)
		}
		if fixes != "" && got != 1 {
			t.Errorf("with the fix a delta sync sent %d items, want the one changed", got)
		}
		if s.serialized.Load()-before != int64(got) || second["applied"] != float64(1) {
			t.Errorf("serialized %d, applied %v", s.serialized.Load()-before, second["applied"])
		}
	}
}

func TestEventsBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "events"} {
		s, base := start(t, fixes)
		tok := login(t, base, "u00200")
		var evs []string
		for i := range 50 {
			evs = append(evs, fmt.Sprintf(`{"name":"screen_view","ts":%d,"props":{"screen":"home"}}`, i))
		}
		body := `{"events":[` + strings.Join(evs, ",") + `]}`
		for range 3 {
			if code, _, m := call(t, "POST", base+"/api/v1/events", tok, body); code != 202 || m["accepted"] != float64(50) {
				t.Fatalf("events: %d %v", code, m)
			}
		}
		want := int64(150)
		if fixes != "" {
			want = 3
		}
		if n := s.writes.Load(); n != want {
			t.Errorf("fixes %q: %d writes for three batches of 50, want %d", fixes, n, want)
		}
		if s.events != 150 {
			t.Errorf("stored %d events", s.events)
		}
	}
}
