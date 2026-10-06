package ai

import (
	"strings"
	"testing"
)

func TestStaticCheckEndpointMatching(t *testing.T) {
	u := &Understanding{HasSpec: true, BasePath: "/v2", Endpoints: []Endpoint{
		{Method: "GET", Path: "/items/{id}"}, {Method: "POST", Path: "/items"},
	}}
	for _, c := range []struct {
		method, path string
		ok           bool
	}{
		{"GET", "/items/${itemId}", true},
		{"GET", "/items/12?expand=${x ? 'a' : 'b'}", true},
		{"GET", "/v2/items/5", true},
		{"POST", "/items/", true},
		{"DELETE", "/items/5", false},
		{"GET", "/items", false},
		{"GET", "/things/1", false},
	} {
		if got := matchEndpoint(u, c.method, c.path); got != c.ok {
			t.Errorf("%s %s: got %v", c.method, c.path, got)
		}
	}
}

func TestUnifiedDiff(t *testing.T) {
	a := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	b := "a\nb\nC\nd\ne\nf\ng\nh\ni\nj\nk\n"
	d := UnifiedDiff(a, b, "old", "new")
	for _, want := range []string{"--- old", "+++ new", "-c", "+C", "+k", "@@ -1,6 +1,6 @@"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff lacks %q:\n%s", want, d)
		}
	}
	if UnifiedDiff(a, a, "x", "y") != "" {
		t.Error("equal texts have no diff")
	}
}
