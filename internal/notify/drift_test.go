package notify

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDriftPayloads(t *testing.T) {
	ev := Event{Type: EventDriftDetected, Message: "shop: 1 journeys broke", Drift: &Drift{
		ID: "d1", Schedule: "nightly", Scenario: "shop", Target: "https://staging.example.com", Broken: []string{"orders"},
		URL: "https://stampede.example.com/api/v1/drift-results/d1",
	}}
	b, err := Payload(KindSlack, ev)
	if err != nil {
		t.Fatal(err)
	}
	var slack map[string]string
	_ = json.Unmarshal(b, &slack)
	for _, want := range []string{"*Stampede · Drift detected*", "`shop` against https://staging.example.com", "Broken: `orders`", "|Open the drift result>"} {
		if !strings.Contains(slack["text"], want) {
			t.Errorf("slack text lacks %q:\n%s", want, slack["text"])
		}
	}
	b, _ = Payload(KindWebhook, ev)
	var hook map[string]any
	_ = json.Unmarshal(b, &hook)
	if d, _ := hook["drift"].(map[string]any); hook["type"] != "drift.detected" || d["schedule"] != "nightly" || hook["run"] != nil {
		t.Errorf("webhook body: %s", b)
	}
}
