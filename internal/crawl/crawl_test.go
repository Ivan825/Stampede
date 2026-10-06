package crawl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Ivan825/Stampede/internal/engine"
)

func TestCrawl(t *testing.T) {
	if _, err := engine.FindChrome(); err != nil {
		t.Skip(err)
	}
	var external, logouts atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { external.Add(1) }))
	defer other.Close()
	mux := http.NewServeMux()
	page := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!doctype html>"+body)
		}
	}
	mux.HandleFunc("GET /{$}", page(fmt.Sprintf(`<title>Home</title><a href="/products">Products</a> <a href="/login">Log in</a>
<a href="/logout">Log out</a> <a href="%s/elsewhere">Partner</a> <a href="/style.css">css</a>`, other.URL)))
	mux.HandleFunc("GET /products", page(`<title>Products</title><ul id="l"></ul><a href="/products/7">Lamp</a>
<script>fetch('/api/products?page=1').then(r => r.json()).then(d => { document.getElementById('l').textContent = d.items.length; });</script>`))
	mux.HandleFunc("GET /products/7", page(`<title>Lamp</title><a href="/">Home</a>`))
	mux.HandleFunc("GET /login", page(`<title>Log in</title><form method="post" action="/api/login"><input name="email" type="email"><input name="password" type="password"><button>Go</button></form>`))
	mux.HandleFunc("GET /logout", func(w http.ResponseWriter, _ *http.Request) { logouts.Add(1) })
	mux.HandleFunc("GET /api/products", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[{"id":7,"name":"Lamp"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := Crawl(context.Background(), Options{Start: srv.URL + "/", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, p := range res.Pages {
		urls = append(urls, strings.TrimPrefix(p.URL, srv.URL))
	}
	if strings.Join(urls, " ") != "/ /products /login /products/7" {
		t.Errorf("pages = %v", urls)
	}
	if logouts.Load() != 0 || external.Load() != 0 {
		t.Errorf("logout visited %d times, external host %d times", logouts.Load(), external.Load())
	}
	if len(res.Forms) != 1 || res.Forms[0].Method != "POST" || !strings.HasSuffix(res.Forms[0].Action, "/api/login") ||
		strings.Join(res.Forms[0].Fields, ",") != "email (email),password (password)" {
		t.Errorf("forms = %+v", res.Forms)
	}
	if !strings.Contains(res.Summary(), "POST "+srv.URL+"/api/login with fields email (email), password (password)") {
		t.Errorf("summary = %q", res.Summary())
	}
	var har struct {
		Log struct {
			Entries []struct {
				Request struct {
					Method, URL string
				} `json:"request"`
				Response struct {
					Status  int `json:"status"`
					Content struct {
						MimeType, Text string
					} `json:"content"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(res.HAR, &har); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range har.Log.Entries {
		if strings.HasSuffix(e.Request.URL, "/api/products?page=1") {
			found = e.Response.Status == 200 && strings.Contains(e.Response.Content.Text, `"Lamp"`)
		}
		if strings.Contains(e.Request.URL, "style.css") {
			t.Errorf("static asset recorded: %s", e.Request.URL)
		}
	}
	if !found {
		t.Errorf("the API call made by the page must be recorded with its response: %+v", har.Log.Entries)
	}
}
