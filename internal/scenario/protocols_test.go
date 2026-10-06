package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cel.dev/cel-go/interpreter"
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
		"send outside ws": {`
      - send: hello`, "send only works inside a ws block"},
		"expect outside ws": {`
      - group: g
        steps: [{expect: hi}]`, "expect only works inside a ws block"},
		"nested ws": {`
      - ws: /a
        steps:
          - group: inner
            steps: [{ws: /b, steps: [{send: x}]}]`, "ws blocks cannot be nested"},
		"ws without steps": {`
      - ws: /chat`, "ws needs steps"},
		"empty send": {`
      - ws: /chat
        steps: [{send: ""}]`, "send needs a message"},
		"expect bad regex": {`
      - ws: /chat
        steps: [{expect: "("}]`, "invalid regex"},
		"expect header extractor": {`
      - ws: /chat
        steps: [{expect: hi, extract: {h: "header:X-Id"}}]`, "header extractors do not apply here"},
		"expect unknown key": {`
      - ws: /chat
        steps:
          - expect: {match: hi, timout: 1s}`, `line 10: unknown key "timout" in expect`},
		"ws request key": {`
      - ws: /chat
        json: {}
        steps: [{send: hi}]`, `"json" does not apply to ws steps`},
		"grpc bad method": {`
      - grpc: shop.v1.Catalog.GetProduct`, "must look like package.Service/Method"},
		"grpc bad status": {`
      - grpc: shop.v1.Catalog/Get
        check: {status: TEAPOT}`, `unknown gRPC status "TEAPOT"`},
		"grpc status number": {`
      - grpc: shop.v1.Catalog/Get
        check: {status: [OK, 404]}`, `unknown gRPC status "404"`},
		"grpc http check key": {`
      - grpc: shop.v1.Catalog/Get
        check: {bodyContains: x}`, `unknown key "bodyContains" in check`},
		"grpc bad target": {`
      - grpc: shop.v1.Catalog/Get
        target: http://localhost:9090`, "must look like grpc://host:port"},
		"grpc target uses a step variable": {`
      - get: /x
        extract: {host: "$.host"}
      - grpc: shop.v1.Catalog/Get
        target: "grpc://${host}:9090"`, `undeclared reference to 'host'`},
		"grpc protoset and proto": {`
      - grpc: shop.v1.Catalog/Get
        protoset: a.protoset
        proto: a.proto`, "set protoset or proto, not both"},
		"grpc message not a mapping": {`
      - grpc: shop.v1.Catalog/Get
        message: "{}"`, "must be a mapping"},
		"grpc cookie extractor": {`
      - grpc: shop.v1.Catalog/Get
        extract: {c: "cookie:sid"}`, "cookie extractors do not apply here"},
		"grpc json key": {`
      - grpc: shop.v1.Catalog/Get
        json: {}`, `"json" does not apply to grpc steps`},
		"allowErrors on http": {`
      - get: /x
        check: {allowErrors: true}`, "only applies to graphql steps"},
		"plugin bad name": {`
      - plugin: mqtt`, "must look like <plugin>.<step>"},
		"plugin upper-case name": {`
      - plugin: MQTT.publish`, "must look like <plugin>.<step>"},
		"plugin unknown key": {`
      - plugin: mqtt.publish
        topic: a`, `line 9: unknown key "topic" in plugin step`},
		"plugin with not a mapping": {`
      - plugin: mqtt.publish
        with: [a]`, "with takes the step's settings as a mapping"},
		"plugin http key": {`
      - plugin: mqtt.publish
        headers: {a: b}`, `"headers" does not apply to plugin steps`},
		"plugin status check": {`
      - plugin: mqtt.publish
        check: {status: 200}`, "does not apply to plugin steps"},
		"plugin header extractor": {`
      - plugin: mqtt.publish
        extract: {h: "header:x"}`, "header extractors do not apply here"},
		"plugin undeclared variable": {`
      - plugin: mqtt.publish
        with: {topic: "t/${nope}"}`, `is "nope" extracted in an earlier step`},
		"with on http": {`
      - get: /x
        with: {a: 1}`, `"with" does not apply to get steps`},
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
      - get: /users/${userId}
      - ws: /chat/${userId}
        steps:
          - expect: {json: {"$.type": welcome}}
            extract: {room: "$.room"}
          - send: {join: "${room}"}
          - loop: 2
            steps:
              - send: "ping ${room}"
              - expect: {match: "^pong", timeout: 2s}
      - get: /rooms/${room}`)
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, st := range p.Steps {
		names = append(names, st.Name)
	}
	want := []string{"graphql /graphql", "GET /users/${userId}", "WS /chat/${userId}", "expect", "send", "send", "expect ^pong", "GET /rooms/${room}"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("recorded steps %q, want %q", names, want)
	}
}

func TestPluginCompile(t *testing.T) {
	s, err := Parse([]byte(protocolScenario(`
      - plugin: mqtt.publish
        with:
          topic: "devices/${vu}"
          qos: 1
          tags: [a, "${iter}"]
          nested: {"a/b": "${vu}"}
        extract: {id: "$.packetId"}
      - plugin: kafka.produce
      - get: /x/${id}`)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	st := p.Steps[0]
	if st.Name != "mqtt.publish" || st.Plugin.Plugin != "mqtt" || st.Plugin.Step != "publish" {
		t.Fatalf("compiled %q %+v", st.Name, st.Plugin)
	}
	if got := strings.Join(st.Plugin.Templated, " "); got != "/nested/a~1b /tags/1 /topic" {
		t.Errorf("templated %q", got)
	}
	v, err := st.Plugin.With.Value(&testActivation{"vu": int64(3), "iter": int64(2)})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["topic"] != "devices/3" || m["qos"] != 1 {
		t.Errorf("rendered %v", m)
	}
	if p.Steps[1].Plugin.Config == nil {
		t.Error("a plugin step without with gets an empty config")
	}
	if got := strings.Join(p.PluginNames(), ","); got != "kafka,mqtt" {
		t.Errorf("plugins %s", got)
	}
}

type testActivation map[string]any

func (a *testActivation) ResolveName(n string) (any, bool) {
	v, ok := (*a)[n]
	return v, ok
}

func (a *testActivation) Parent() interpreter.Activation { return nil }

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

func TestGRPCPathsResolveAgainstTheScenarioFile(t *testing.T) {
	dir := t.TempDir()
	src := protocolScenario(`
      - grpc: a.B/C
        protoset: protos/shop.protoset
      - grpc: a.B/D
        proto: shop.proto
      - ws: /chat
        steps:
          - grpc: a.B/E
            proto: [shop.proto]
            importPaths: [protos, /abs]`)
	path := filepath.Join(dir, "s.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	steps := s.Journeys[0].Steps
	if got := steps[0].GRPC.Protoset; got != filepath.Join(dir, "protos/shop.protoset") {
		t.Errorf("protoset %q", got)
	}
	if got := steps[1].GRPC.ImportPaths; len(got) != 1 || got[0] != dir {
		t.Errorf("a .proto without import paths is found next to the scenario: %q", got)
	}
	if got := steps[2].WS.Steps[0].GRPC.ImportPaths; len(got) != 2 || got[0] != filepath.Join(dir, "protos") || got[1] != "/abs" {
		t.Errorf("import paths %q", got)
	}
}

// TestProtocolDocsExamples parses every step example in docs/protocols.md
// so the documentation cannot drift from the parser.
func TestProtocolDocsExamples(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "protocols.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(b), "```yaml\n")[1:]
	n := 0
	for _, blk := range blocks {
		blk, _, _ = strings.Cut(blk, "```")
		if !strings.HasPrefix(blk, "- ") {
			continue
		}
		n++
		lines := strings.Split(strings.TrimRight(blk, "\n"), "\n")
		for i, l := range lines {
			lines[i] = "      " + l
		}
		// Examples use variables an earlier step would have extracted.
		src := strings.Replace(protocolScenario(strings.Join(lines, "\n")), "journeys:",
			"vars: {productId: p1, token: t, orderId: 1}\njourneys:", 1)
		if _, err := Parse([]byte(src)); err != nil {
			t.Errorf("example %d does not parse: %v\n%s", n, err, blk)
		}
	}
	if n < 4 {
		t.Errorf("found %d step examples, want one per protocol", n)
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
