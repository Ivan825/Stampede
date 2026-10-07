package client

import (
	"strings"
	"testing"
)

type item struct{ id, name string }

func TestPick(t *testing.T) {
	items := []item{{"0a1b2c3d-1", "Alpha"}, {"0a1b9999-2", "beta"}, {"ffff0000-3", "gamma"}}
	id := func(i item) string { return i.id }
	keys := func(i item) []string { return []string{i.name} }
	for ref, want := range map[string]string{"ffff0000-3": "gamma", "ALPHA": "Alpha", "ffff": "gamma", "0a1b2": "Alpha"} {
		got, err := pick(items, "thing", ref, id, keys)
		if err != nil || got.name != want {
			t.Errorf("%s: %v %v", ref, got, err)
		}
	}
	for ref, msg := range map[string]string{"0a1b": "matches 2 things", "zzz": `thing "zzz" not found`, "": "name the thing", "ff": `thing "ff" not found`} {
		if _, err := pick(items, "thing", ref, id, keys); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: %v", ref, err)
		}
	}
}

func TestQuery(t *testing.T) {
	if q := Query("limit", "5", "before", "", "scenarioId", "a b"); q != "?limit=5&scenarioId=a+b" {
		t.Errorf("query %q", q)
	}
	if q := Query("x", ""); q != "" {
		t.Errorf("empty query %q", q)
	}
}
