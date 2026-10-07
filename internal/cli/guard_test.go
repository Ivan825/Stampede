package cli

import (
	"strings"
	"testing"
)

// TestValidateWarnsAboutThirdParties: validate reports calls to guarded
// providers as warnings and still passes the file.
func TestValidateWarnsAboutThirdParties(t *testing.T) {
	path := writeScenario(t, `metadata: {name: pay}
target: {baseURL: "https://shop.example.com"}
journeys:
  - name: checkout
    steps:
      - name: charge
        post: https://api.stripe.com/v1/charges
load: {iterations: 1}
`)
	out, err := runCLI(t, "validate", path)
	if err != nil {
		t.Fatalf("validate should only warn: %v\n%s", err, out)
	}
	if !strings.Contains(out, "! "+path+": journey checkout, step charge calls api.stripe.com, a payment provider") || !strings.Contains(out, "✓ "+path) {
		t.Errorf("output: %s", out)
	}
}
