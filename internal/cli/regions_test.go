package cli

import (
	"strings"
	"testing"
)

func TestRegionsFrom(t *testing.T) {
	got, err := regionsFrom([]string{"mumbai=50%", "frankfurt=30", "virginia = 20%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["mumbai"] != 50 || got["frankfurt"] != 30 || got["virginia"] != 20 {
		t.Errorf("regions = %v", got)
	}
	if got, err := regionsFrom(nil); got != nil || err != nil {
		t.Errorf("no flags: %v %v", got, err)
	}
	for _, tc := range []struct {
		flags []string
		want  string
	}{
		{[]string{"mumbai"}, "use REGION=PERCENT"},
		{[]string{"mumbai=abc"}, "invalid percentage"},
		{[]string{"mumbai=50%", "mumbai=50%"}, "given twice"},
		{[]string{"mumbai=50%", "frankfurt=10%"}, "add up to 100%"},
	} {
		if _, err := regionsFrom(tc.flags); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want %q", tc.flags, err, tc.want)
		}
	}
}

func TestRunClusterRegions(t *testing.T) {
	if _, err := runCLI(t, "run", "x.yaml", "--region", "eu=100%"); err == nil || !strings.Contains(err.Error(), "--region applies only with --cluster") {
		t.Errorf("--region without --cluster: %v", err)
	}
	// The test server runs load in-process, so a split is refused with the
	// server's explanation, and nothing starts.
	path, base := shopProject(t)
	_, err := runCLI(t, "run", path, "--cluster", "--project", "shop", "--base-url", base, "--region", "eu=60%", "--region", "us=40%", "-q")
	if err == nil || !strings.Contains(err.Error(), "needs distributed workers") {
		t.Errorf("run --cluster --region: %v", err)
	}
	_, err = runCLI(t, "start", "--project", "shop", "--file", path, "--region", "eu=100%", "--detach")
	if err == nil || !strings.Contains(err.Error(), "needs distributed workers") {
		t.Errorf("start --region: %v", err)
	}
	if _, err := runCLI(t, "start", "--project", "shop", "--file", path, "--region", "eu=60%"); err == nil || !strings.Contains(err.Error(), "add up to 100%") {
		t.Errorf("start with a bad split: %v", err)
	}
}
