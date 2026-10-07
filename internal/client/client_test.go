package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A response decoded into the value that held the request keeps nothing
// the response leaves out.
func TestDoDecodesIntoAZeroValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"b","env":{"Y":"2"}}`))
	}))
	defer srv.Close()
	c := &Client{Server: srv.URL, Token: "t", HTTP: srv.Client()}
	v := struct {
		Name string            `json:"name"`
		Note *string           `json:"note,omitempty"`
		Env  map[string]string `json:"env"`
	}{Name: "a", Env: map[string]string{"X": "1"}}
	note := "old"
	v.Note = &note
	if err := c.Do(context.Background(), "PATCH", "/x", v, &v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "b" || v.Note != nil || len(v.Env) != 1 || v.Env["Y"] != "2" {
		t.Errorf("decoded %+v", v)
	}
}
