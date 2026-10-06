package ai

import (
	"fmt"
	"sort"
	"strings"
)

// systemPrompt explains the task, the scenario format and the rules.
// It is constant for a given schema so providers can cache it.
func systemPrompt() string {
	return `You write load-test scenarios for Stampede, an open-source load tester. A scenario describes how real users behave; Stampede first runs each journey once with a single user (a dry run), then runs many users at once.

Your reply is one JSON object: a complete Stampede scenario that validates against the JSON Schema you are given. No prose, no markdown.

How to model users
- Write several journeys, one per kind of visit (for example browse, search, buy, check orders). Give each an integer weight proportional to how often it happens; use the access-log mix when one is given.
- Put think times between steps where a person would pause: {"think": "1s..3s"}.
- Use branches for alternatives inside a journey: {"branch": [{"weight": 3, "name": "...", "steps": [...]}, ...]}.
- Include edge journeys with small weights where the API supports them: abandoned carts (add to cart, never check out), failed logins (wrong password), invalid input, and retries of a step that can conflict. Give each failing-on-purpose step a check for the status it really returns, for example {"check": {"status": 401}}, so the step counts as passing.
- Use only endpoints, methods and fields from the API description. Never invent endpoints. Use relative paths such as "/api/products"; Stampede joins them to the target. Do not set target.baseURL.
- Never call third-party services (payment, SMS, email, CAPTCHA), even if the API mentions them.
- Use the test accounts and example values the user or the API description provides. Do not invent credentials for real people.

Step format (compact): each step is an object with exactly one action
- request: {"get": "/path"} or "post", "put", "patch", "delete", "head", "options", plus optional "headers", "query", "json" (a JSON body), "form", "body" (raw string), "check", "extract", "timeout", "name", "if".
- {"think": "2s"} or {"think": "1s..4s"}
- {"branch": [ {"weight": n, "name": "...", "steps": [...]}, ... ]}
- {"loop": 3, "steps": [...]}, {"while": "expr", "max": 5, "steps": [...]}, {"group": "name", "steps": [...]}

Variables and checks
- Values come from earlier responses through "extract": {"token": "$.token", "productId": "$.products[0].id"}. Extractors: "$.json.path", "header:Name", "cookie:name", "regex:(...)", "css:selector", "status", "body".
- Use variables with ${...}: "/api/products/${productId}", {"Authorization": "Bearer ${token}"}. A variable must be extracted in an earlier step of the same journey before it is used; this is checked.
- Expressions are CEL. Helpers: rand(a, b), randString(n), randEmail(), uuid(), pick(list), now(), nowMs(). Roots: env, secret, data, vars, vu, iter.
- "check": {"status": 200} (or [200, 201] or "2xx"), "bodyContains", "json": {"$.path": value or "exists"}, "maxLatency", "expr".
- Without a check, any status of 400 or more fails the step. A failed step ends the journey, so every step's check must match what the API really returns (201 for creates, 204 for empty replies).
- Cookies set by the server are kept automatically within a journey.

Other parts
- metadata.name: lowercase letters, digits, '.', '_' or '-'. Add a one-line metadata.description.
- load: unless the user asks for something else, use {"mode": "vus", "vus": 5, "duration": "1m"}.
- targets (optional): pass/fail goals such as "http.p95 < 500ms" and "errors < 1%".
- data (optional): test data feeders. Prefer inline lists, e.g. {"users": {"list": [{"email": "...", "password": "..."}]}}, used as ${data.users.email}.`
}

// draftPrompt is the first user message.
func draftPrompt(und *Understanding, existing string) string {
	var b strings.Builder
	b.WriteString("Write a Stampede scenario for this system.\n\n")
	b.WriteString(und.Context)
	if existing != "" {
		b.WriteString("## The current scenario (improve on it; keep journeys that still make sense)\n\n")
		b.WriteString(Truncate(existing, 20_000))
		b.WriteString("\n\n")
	}
	b.WriteString("Reply with the complete scenario as one JSON object.")
	return b.String()
}

// repairPrompt sends problems and dry-run evidence back to the model.
func repairPrompt(static []Problem, failures map[string][]Trace, passed []string) string {
	var b strings.Builder
	b.WriteString("Your scenario has problems. Fix them and reply with the complete corrected scenario as one JSON object.\n\n")
	if len(static) > 0 {
		b.WriteString("## Problems found by the static check\n\n")
		for _, p := range static {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	}
	if len(failures) > 0 {
		b.WriteString("## Dry-run failures (one user against the real target; secrets are redacted)\n\n")
		names := make([]string, 0, len(failures))
		for n := range failures {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			for _, t := range failures[n] {
				if t.OK {
					continue
				}
				fmt.Fprintf(&b, "### journey %s, pass %d", n, t.Pass)
				if len(t.Branches) > 0 {
					fmt.Fprintf(&b, " (branches: %s)", strings.Join(t.Branches, ", "))
				}
				b.WriteString("\n")
				b.WriteString(renderTrace(t, 1200))
				b.WriteString("\n")
			}
		}
		b.WriteString("Fix the requests, checks or extractors so they match how the API really behaves. If a journey cannot work against this API, remove it.\n")
	}
	if len(passed) > 0 {
		fmt.Fprintf(&b, "\nThese journeys passed; keep them unchanged: %s.\n", strings.Join(passed, ", "))
	}
	return b.String()
}

// renderTrace prints a trace compactly for a model or a terminal.
func renderTrace(t Trace, bodyLimit int) string {
	var b strings.Builder
	for i, s := range t.Steps {
		mark := "ok"
		if !s.OK {
			mark = "FAILED"
		}
		if s.Note != "" && s.Method == "" {
			fmt.Fprintf(&b, "%d. %s: %s\n", i+1, s.Step, s.Note)
			continue
		}
		fmt.Fprintf(&b, "%d. [%s] %s %s", i+1, mark, s.Method, s.URL)
		if s.Status != 0 {
			fmt.Fprintf(&b, " → %d (%.0f ms)", s.Status, s.DurationMs)
		}
		b.WriteString("\n")
		if s.OK {
			if len(s.Extracted) > 0 {
				keys := make([]string, 0, len(s.Extracted))
				for k := range s.Extracted {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, k+"="+s.Extracted[k])
				}
				fmt.Fprintf(&b, "   extracted: %s\n", strings.Join(parts, ", "))
			}
			continue
		}
		if s.RequestBody != "" {
			fmt.Fprintf(&b, "   request body: %s\n", Truncate(s.RequestBody, bodyLimit/2))
		}
		failedCheck := false
		for _, c := range s.Checks {
			if !c.OK {
				failedCheck = true
				fmt.Fprintf(&b, "   check %s failed: %s\n", c.Name, c.Detail)
			}
		}
		if s.Error != "" && !failedCheck {
			fmt.Fprintf(&b, "   error: %s\n", s.Error)
		}
		if s.ResponseBody != "" {
			fmt.Fprintf(&b, "   response body: %s\n", Truncate(s.ResponseBody, bodyLimit))
		}
	}
	if t.Error != "" && len(t.Steps) == 0 {
		fmt.Fprintf(&b, "error: %s\n", t.Error)
	}
	return b.String()
}

// RenderTrace is renderTrace for callers outside the package.
func RenderTrace(t Trace) string { return renderTrace(t, 600) }
