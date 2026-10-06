package ai

import (
	"os"
	"strings"
	"testing"
)

func TestUnderstandShopLabSpec(t *testing.T) {
	b, err := os.ReadFile("../../examples/shoplab/openapi.yaml")
	if err != nil {
		t.Skip(err)
	}
	u, err := Understand(Inputs{OpenAPI: b}, NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Endpoints) < 15 || !u.HasSpec {
		t.Errorf("endpoints: %d", len(u.Endpoints))
	}
	want := map[string]bool{
		"POST /api/login → $.token → GET /api/me":                       false,
		"POST /api/login → $.token → POST /api/checkout":                false,
		"GET /api/products → $.products[0].id → GET /api/products/{id}": false,
	}
	for _, d := range u.Dependencies {
		k := d.Producer + " → " + d.Value + " → " + d.Consumer
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("missing dependency %s in %+v", k, u.Dependencies)
		}
	}
	if !strings.Contains(u.Context, "user0001@shoplab.test") || !strings.Contains(u.Context, "### POST /api/checkout") {
		t.Error("the digest should describe the API, including its test accounts")
	}
}

func TestUnderstandHARAndAccessLog(t *testing.T) {
	har := `{"log":{"entries":[
	 {"startedDateTime":"2026-01-01T00:00:01Z","request":{"method":"POST","url":"http://shop.test/api/login","headers":[],"postData":{"mimeType":"application/json","text":"{\"email\":\"jane@corp.example\",\"password\":\"hunter22\"}"}},
	  "response":{"status":200,"headers":[{"name":"Set-Cookie","value":"sid=abcdef123456; Path=/"}],"content":{"mimeType":"application/json","text":"{\"token\":\"tkn-9f8e7d6c5b4a\"}"}}},
	 {"startedDateTime":"2026-01-01T00:00:02Z","request":{"method":"GET","url":"http://shop.test/api/orders/123?page=1","headers":[{"name":"Authorization","value":"Bearer tkn-9f8e7d6c5b4a"},{"name":"Cookie","value":"sid=abcdef123456"}]},
	  "response":{"status":200,"headers":[],"content":{"mimeType":"application/json","text":"{\"id\":123}"}}},
	 {"startedDateTime":"2026-01-01T00:00:03Z","request":{"method":"GET","url":"http://shop.test/app.js","headers":[]},"response":{"status":200,"headers":[],"content":{"mimeType":"application/javascript","text":""}}},
	 {"startedDateTime":"2026-01-01T00:00:04Z","request":{"method":"POST","url":"https://api.stripe.com/v1/tokens","headers":[]},"response":{"status":200,"headers":[],"content":{"mimeType":"application/json","text":"{}"}}}
	]}}`
	log := `10.0.0.1 - - [06/Oct/2026:10:00:00 +0000] "GET /api/products?page=2 HTTP/1.1" 200 512 "-" "Mozilla"
10.0.0.1 - - [06/Oct/2026:10:00:05 +0000] "GET /api/products/42 HTTP/1.1" 200 512 "-" "Mozilla"
10.0.0.2 - - [06/Oct/2026:10:00:06 +0000] "POST /api/login HTTP/1.1" 401 64 "-" "Mozilla"
10.0.0.2 - - [06/Oct/2026:10:00:09 +0000] "GET /static/app.css HTTP/1.1" 200 64 "-" "Mozilla"
10.0.0.3 - - [06/Oct/2026:10:00:10 +0000] "GET /api/products?q=jane@corp.example HTTP/1.1" 200 512 "-" "Mozilla"
`
	u, err := Understand(Inputs{HAR: []byte(har), AccessLog: []byte(log)}, NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, e := range u.Endpoints {
		keys = append(keys, e.Key())
	}
	all := strings.Join(keys, ",")
	for _, want := range []string{"POST /api/login", "GET /api/orders/{id}", "GET /api/products", "GET /api/products/{id}"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing endpoint %s in %s", want, all)
		}
	}
	if strings.Contains(all, "app.js") || strings.Contains(all, "stripe") || strings.Contains(all, "app.css") {
		t.Errorf("static assets and third parties must be ignored: %s", all)
	}
	foundToken, foundCookie := false, false
	for _, d := range u.Dependencies {
		if d.Producer == "POST /api/login" && d.Value == "$.token" && d.Consumer == "GET /api/orders/{id}" && d.Via == "Authorization header" {
			foundToken = true
		}
		if d.Value == "cookie sid" {
			foundCookie = true
		}
	}
	if !foundToken || !foundCookie {
		t.Errorf("dependencies: %+v", u.Dependencies)
	}
	if u.Mix[0].Endpoint != "GET /api/products" || u.Mix[0].Count != 2 {
		t.Errorf("mix: %+v", u.Mix)
	}
	if len(u.Visits) == 0 {
		t.Error("no visit patterns")
	}
	for _, leak := range []string{"hunter22", "jane@corp.example", "tkn-9f8e7d6c5b4a", "abcdef123456", "10.0.0.1"} {
		if strings.Contains(u.Context, leak) {
			t.Errorf("context leaks %q:\n%s", leak, u.Context)
		}
	}
}
