package govlab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func call(t *testing.T, method, u, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func download(t *testing.T, u string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func signIn(t *testing.T, base, mobile string) string {
	t.Helper()
	_, o := call(t, "POST", base+"/api/otp/request", "", `{"mobile":"`+mobile+`"}`)
	code, v := call(t, "POST", base+"/api/otp/verify", "", fmt.Sprintf(`{"requestId":%q,"code":%q}`, o["requestId"], o["testCode"]))
	if code != 200 {
		t.Fatalf("verify: %d %v", code, v)
	}
	return v["token"].(string)
}

func TestResults(t *testing.T) {
	for _, fixes := range []string{"", "all"} {
		s, base := start(t, fixes)
		roll := FirstRoll + 4242
		code, m := call(t, "POST", base+"/api/results/lookup", "", fmt.Sprintf(`{"rollNumber":"%d","dateOfBirth":%q}`, roll, DOB(roll)))
		if code != 200 || len(m["subjects"].([]any)) != 6 || m["maximum"] != float64(600) {
			t.Fatalf("lookup: %d %v", code, m)
		}
		if code, _ := call(t, "POST", base+"/api/results/lookup", "", fmt.Sprintf(`{"rollNumber":"%d","dateOfBirth":"2001-01-01"}`, roll)); code != 404 {
			t.Fatalf("wrong date of birth: %d", code)
		}
		code, pdf := download(t, fmt.Sprintf("%s/api/results/%d/marksheet?dob=%s", base, roll, DOB(roll)))
		if code != 200 || !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) || !bytes.Contains(pdf, []byte("Mathematics")) {
			t.Fatalf("marksheet: %d %q", code, pdf[:min(40, len(pdf))])
		}
		if fixes == "" && s.scanned.Load() != 3*Candidates {
			t.Errorf("without the index fix three lookups scanned %d rows", s.scanned.Load())
		}
		if fixes != "" && s.scanned.Load() != 0 {
			t.Errorf("with the index fix lookups scanned %d rows", s.scanned.Load())
		}
	}
}

func TestPDFBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "pdf"} {
		s, base := start(t, fixes)
		var pdfs [][]byte
		for i := range 5 {
			roll := FirstRoll + i
			_, b := download(t, fmt.Sprintf("%s/api/results/%d/marksheet?dob=%s", base, roll, DOB(roll)))
			pdfs = append(pdfs, b)
		}
		want := int64(5)
		if fixes != "" {
			want = 1
		}
		if n := s.compressed.Load(); n != want {
			t.Errorf("fixes %q: five marksheets compressed the letterhead %d times, want %d", fixes, n, want)
		}
		if bytes.Equal(pdfs[0], pdfs[1]) {
			t.Error("two candidates got the same marksheet")
		}
	}
}

func TestApplication(t *testing.T) {
	_, base := start(t, "")
	tok := signIn(t, base, "9876543210")
	if code, _ := call(t, "POST", base+"/api/applications", "", ""); code != 401 {
		t.Fatalf("no sign-in: %d", code)
	}
	code, app := call(t, "POST", base+"/api/applications", tok, "")
	if code != 201 || app["status"] != "draft" {
		t.Fatalf("create: %d %v", code, app)
	}
	id := app["applicationId"].(string)
	if code, m := call(t, "POST", base+"/api/applications/"+id+"/submit", tok, ""); code != 422 || !strings.Contains(m["error"].(map[string]any)["message"].(string), "personal") {
		t.Fatalf("incomplete submit: %d %v", code, m)
	}
	for _, sec := range required {
		if code, m := call(t, "PUT", base+"/api/applications/"+id+"/sections/"+sec, tok, `{"value":"x"}`); code != 200 {
			t.Fatalf("save %s: %d %v", sec, code, m)
		}
	}
	req, _ := http.NewRequest("POST", base+"/api/applications/"+id+"/documents?type=income-certificate", bytes.NewReader(make([]byte, 50_000)))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("upload: %v %v", err, resp)
	}
	resp.Body.Close()
	code, sub := call(t, "POST", base+"/api/applications/"+id+"/submit", tok, "")
	if code != 200 || sub["acknowledgementNumber"] == nil {
		t.Fatalf("submit: %d %v", code, sub)
	}
	if code, _ := call(t, "POST", base+"/api/applications/"+id+"/submit", tok, ""); code != 409 {
		t.Fatalf("second submit: %d", code)
	}
	if code, m := call(t, "GET", base+"/api/acknowledgements/"+sub["acknowledgementNumber"].(string), "", ""); code != 200 || m["status"] != "received" {
		t.Fatalf("track: %d %v", code, m)
	}
	if code, _ := call(t, "GET", base+"/api/applications/"+id, signIn(t, base, "9000000001"), ""); code != 404 {
		t.Fatalf("someone else's application: %d", code)
	}
}

func TestLockBottleneck(t *testing.T) {
	for _, tc := range []struct {
		fixes string
		one   bool
	}{{"", true}, {"lock", false}} {
		s, base := start(t, tc.fixes)
		s.write = 3 * time.Millisecond
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tok := signIn(t, base, fmt.Sprintf("90000%05d", i))
				_, app := call(t, "POST", base+"/api/applications", tok, "")
				call(t, "PUT", base+"/api/applications/"+app["applicationId"].(string)+"/sections/personal", tok, `{"name":"x"}`)
			}()
		}
		wg.Wait()
		if p := s.savesInLock.Peak(); (p == 1) != tc.one {
			t.Errorf("fixes %q: %d saves at once", tc.fixes, p)
		}
	}
}
