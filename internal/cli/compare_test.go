package cli

import "testing"

func TestRepeatPath(t *testing.T) {
	cases := []struct {
		in     string
		n, tot int
		want   string
	}{
		{"r.json", 2, 3, "r-2.json"},
		{"out/v1.report.json", 1, 3, "out/v1.report-1.json"},
		{"dir.v/report", 3, 3, "dir.v/report-3"},
		{"r.json", 1, 1, "r.json"},
		{"-", 2, 3, "-"},
	}
	for _, c := range cases {
		if got := repeatPath(c.in, c.n, c.tot); got != c.want {
			t.Errorf("%s: got %s want %s", c.in, got, c.want)
		}
	}
}
