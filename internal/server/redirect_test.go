package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedirectHTTPS(t *testing.T) {
	w := httptest.NewRecorder()
	redirectHTTPS(w, httptest.NewRequest(http.MethodGet, "http://stampede.example:80/runs/1?tab=report", nil))
	if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "https://stampede.example/runs/1?tab=report" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
}
