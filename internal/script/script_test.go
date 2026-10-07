package script

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	p, err := Compile("t", `
const items = vars.cart.items;
let total = 0;
for (const it of items) total += it.price * it.qty;
vars.total = total;
vars.label = env.REGION + "-" + vu + "-" + iteration + "-" + data.users.email;
console.log("total", total);
if (total > 1000) return;
vars.small = true;
`)
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"cart": map[string]any{"items": []any{
		map[string]any{"price": 2.5, "qty": int64(2)},
		map[string]any{"price": 10.0, "qty": int64(1)},
	}}}
	var logged []string
	var r Runner
	out, err := r.Run(context.Background(), p, Input{
		Vars: in, Env: map[string]string{"REGION": "eu"}, Data: map[string]any{"users": map[string]any{"email": "a@example.test"}},
		VU: 3, Iteration: 9, Log: func(s string) { logged = append(logged, s) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["total"] != int64(15) && out["total"] != float64(15) {
		t.Errorf("total %#v", out["total"])
	}
	if out["label"] != "eu-3-9-a@example.test" || out["small"] != true {
		t.Errorf("out %#v", out)
	}
	if _, ok := in["total"]; ok {
		t.Error("input vars were modified")
	}
	if len(logged) != 1 || logged[0] != "total 15" {
		t.Errorf("logged %q", logged)
	}
}

func TestFailuresAndLimits(t *testing.T) {
	var r Runner
	run := func(code string) error {
		p, err := Compile("t", code)
		if err != nil {
			return err
		}
		_, err = r.Run(context.Background(), p, Input{})
		return err
	}
	var f *Failure
	if err := run(`fail("no seats left")`); !errors.As(err, &f) || f.Message != "no seats left" {
		t.Errorf("fail: %v", err)
	}
	if err := run(`throw new Error("boom")`); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("throw: %v", err)
	}
	if err := run(`while (true) {}`); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Errorf("loop: %v", err)
	}
	// The runtime is usable after an interrupt.
	if err := run(`vars.x = 1`); err != nil {
		t.Errorf("after interrupt: %v", err)
	}
	if err := run(`undefinedName.x`); err == nil {
		t.Error("reference error not reported")
	}
	if _, err := Compile("t", `let = ;`); err == nil {
		t.Error("syntax error not reported")
	}
	if err := run(`require("fs")`); err == nil {
		t.Error("require is available")
	}
}
