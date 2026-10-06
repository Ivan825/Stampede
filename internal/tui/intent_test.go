package tui

import "testing"

func TestParseSlash(t *testing.T) {
	c, ok := ParseSlash("/run spike shop-mix --rate 200/s --duration=5m")
	if !ok || c.Name != "run" || len(c.Args) != 2 || c.Args[0] != "spike" || c.Flags["rate"] != "200/s" || c.Flags["duration"] != "5m" {
		t.Fatalf("%+v", c)
	}
	if _, ok := ParseSlash("run spike"); ok {
		t.Error("needs a slash")
	}
}

func TestInterpret(t *testing.T) {
	sc := []string{"shoplab-mix", "checkout-flow", "order-history"}
	cases := []struct {
		in    string
		want  string
		match bool
	}{
		{"find the breaking point for checkout", "/run breakpoint --scenario checkout-flow", true},
		{"run a spike test on order-history at 300 rps", "/run spike --scenario order-history --rate 300/s", true},
		{"soak test for 4 hours", "/run soak --duration 4h0m0s", true},
		{"smoke test shoplab-mix", "/run smoke --scenario shoplab-mix", true},
		{"500 users for 10 minutes", "/run baseline --vus 500 --duration 10m0s", true},
		{"what's the weather", "", false},
	}
	for _, c := range cases {
		got, ok := Interpret(c.in, sc)
		if ok != c.match || (ok && got.String() != c.want) {
			t.Errorf("%q: got %q (%v), want %q", c.in, got.String(), ok, c.want)
		}
	}
}
