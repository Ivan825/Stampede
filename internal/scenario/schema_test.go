package scenario

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// TestSchemaAcceptsExamples keeps schema/scenario.schema.json in step with
// the parser: every example the parser accepts must validate.
func TestSchemaAcceptsExamples(t *testing.T) {
	c := jsonschema.NewCompiler()
	sch, err := c.Compile(filepath.Join("..", "..", "schema", "scenario.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("testdata/*.yaml")
	if len(files) == 0 {
		t.Fatal("no examples")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(b); err != nil {
			t.Fatalf("%s does not parse: %v", f, err)
		}
		var doc any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(normalizeYAML(doc)); err != nil {
			t.Errorf("%s fails the schema: %v", f, err)
		}
	}
}

func TestSchemaRejectsTwoActions(t *testing.T) {
	c := jsonschema.NewCompiler()
	sch, err := c.Compile(filepath.Join("..", "..", "schema", "scenario.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	_ = yaml.Unmarshal([]byte(`
metadata: {name: x}
journeys: [{name: a, steps: [{get: /a, post: /b}]}]
load: {vus: 1, duration: 1s}`), &doc)
	if err := sch.Validate(normalizeYAML(doc)); err == nil {
		t.Error("a step with two actions should fail the schema")
	}
}
