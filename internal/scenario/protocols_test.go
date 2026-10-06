package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// protocolScenario wraps steps in a minimal scenario.
func protocolScenario(steps string) string {
	return `
metadata: {name: t}
target: {baseURL: "http://localhost"}
journeys:
  - name: a
    steps:
` + steps + `
load: {vus: 1, duration: 1s}`
}

func TestProtocolStepErrors(t *testing.T) {
	cases := map[string]struct {
		steps string
		want  string
	}{
		"graphql without query": {`
      - graphql: /graphql`, "graphql needs a query"},
		"graphql bad hash": {`
      - graphql: /graphql
        persisted: {sha256: nothex}`, "must be 64 hex digits"},
		"graphql unknown key": {`
      - graphql: /graphql
        query: "{ x }"
        varables: {}`, `line 10: unknown key "varables" in graphql step`},
		"graphql body key": {`
      - graphql: /graphql
        query: "{ x }"
        json: {}`, `line 10: "json" does not apply to graphql steps`},
		"graphql variables not a mapping": {`
      - graphql: /graphql
        query: "{ x }"
        variables: [1, 2]`, "must be a mapping"},
		"graphql undeclared variable": {`
      - graphql: /graphql
        query: "{ x }"
        variables: {id: "${missing}"}`, `is "missing" extracted in an earlier step`},
		"graphql persisted typo": {`
      - graphql: /graphql
        query: "{ x }"
        persisted: {sha: abc}`, `unknown key "sha" in persisted`},
		"sse bad regex": {`
      - sse: /stream
        until: {match: "("}`, "invalid regex"},
		"sse unknown until key": {`
      - sse: /stream
        until: {event: 3}`, `unknown key "event" in until`},
		"sse bad method": {`
      - sse: /stream
        method: FETCH`, "method must be an HTTP method"},
		"sse negative events": {`
      - sse: /stream
        until: {events: -1}`, "must be positive"},
		"sse extract undeclared in check": {`
      - sse: /stream
        check: {bodyContains: "${later}"}
        extract: {later: "$.x"}`, `"later"`},
		"allowErrors on http": {`
      - get: /x
        check: {allowErrors: true}`, "only applies to graphql steps"},
		"request key on think": {`
      - think: 1s
        check: {status: 200}`, `"check" does not apply to think steps`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(protocolScenario(tc.steps)))
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestProtocolVariablesFlow(t *testing.T) {
	src := protocolScenario(`
      - graphql: /graphql
        query: "query { me { id } }"
        extract: {userId: "$.data.me.id"}
      - get: /users/${userId}`)
	if _, err := Parse([]byte(src)); err != nil {
		t.Fatal(err)
	}
}

func TestGraphQLCompile(t *testing.T) {
	const q = "query { me { id } }"
	s, err := Parse([]byte(protocolScenario(`
      - graphql: /graphql
        query: "` + q + `"
        persisted: true
        operationName: Me`)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	st := p.Steps[0]
	// The hash of a literal query is known at compile time.
	sum := sha256.Sum256([]byte(q))
	if st.Name != "graphql Me" || st.GraphQL.Hash != hex.EncodeToString(sum[:]) {
		t.Errorf("name %q hash %q", st.Name, st.GraphQL.Hash)
	}
	if !st.GraphQL.Persisted || st.Req.Method != "POST" {
		t.Errorf("compiled %+v %+v", st.GraphQL, st.Req)
	}
}

// TestProtocolExamplesRoundTrip renders every protocol example back to
// YAML and parses it again, so the compact form survives a save.
func TestProtocolExamplesRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.yaml")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s, err := Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out, err := s.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		s2, err := Parse(out)
		if err != nil {
			t.Fatalf("%s: re-parse failed: %v\n%s", f, err, out)
		}
		p1, _ := Compile(s)
		p2, _ := Compile(s2)
		if len(p1.Steps) != len(p2.Steps) {
			t.Errorf("%s: %d recorded steps before the round trip, %d after", f, len(p1.Steps), len(p2.Steps))
		}
	}
}
