package pluginsdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	pluginv1 "github.com/Ivan825/Stampede/gen/stampede/plugin/v1"
)

// TargetKeyword marks the top-level config property that holds the
// address a step connects to.
const TargetKeyword = "x-stampede-target"

var (
	// NameRe is what a plugin name must look like.
	NameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	// StepNameRe is what a step name must look like.
	StepNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,62}$`)
)

// CompileSchema compiles a step's config schema. Configs are JSON
// objects, so the schema must accept only objects.
func CompileSchema(step string, schema []byte) (*jsonschema.Schema, error) {
	if len(bytes.TrimSpace(schema)) == 0 {
		return nil, fmt.Errorf("step %s has no config schema", step)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("step %s: config schema is not JSON: %w", step, err)
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("step %s: config schema must be a JSON object", step)
	}
	if t, _ := m["type"].(string); t != "object" {
		return nil, fmt.Errorf(`step %s: config schema must have "type": "object"`, step)
	}
	if _, err := TargetFields(schema); err != nil {
		return nil, fmt.Errorf("step %s: %w", step, err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	url := "stampede-plugin:///" + step + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("step %s: %w", step, err)
	}
	s, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("step %s: invalid config schema: %w", step, err)
	}
	return s, nil
}

// TargetFields lists the top-level properties a schema marks with
// "x-stampede-target": true. A marked property must be a string or an
// array of strings; the keyword is not allowed below the top level.
func TargetFields(schema []byte) ([]string, error) {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil, err
	}
	var out []string
	for name, raw := range s.Properties {
		var p map[string]any
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		mark, ok := p[TargetKeyword]
		if !ok {
			if bytes.Contains(raw, []byte(TargetKeyword)) {
				return nil, fmt.Errorf("%s is only allowed on top-level properties (found inside %q)", TargetKeyword, name)
			}
			continue
		}
		if mark != true {
			return nil, fmt.Errorf("property %q: %s must be true", name, TargetKeyword)
		}
		switch p["type"] {
		case "string":
		case "array":
			items, _ := p["items"].(map[string]any)
			if items == nil || items["type"] != "string" {
				return nil, fmt.Errorf("property %q: a %s array must hold strings", name, TargetKeyword)
			}
		default:
			return nil, fmt.Errorf("property %q: %s needs \"type\": \"string\" or an array of strings", name, TargetKeyword)
		}
		out = append(out, name)
	}
	return out, nil
}

// ValidateDescription checks what a plugin describes about itself: its
// name and version, and that every step has a unique valid name and a
// config schema that compiles.
func ValidateDescription(d *pluginv1.DescribeResponse) error {
	var errs []error
	if !NameRe.MatchString(d.GetName()) {
		errs = append(errs, fmt.Errorf("plugin name %q must be lowercase letters, digits and hyphens, starting with a letter", d.GetName()))
	}
	if strings.TrimSpace(d.GetVersion()) == "" {
		errs = append(errs, errors.New("plugin version is empty"))
	}
	if len(d.GetSteps()) == 0 {
		errs = append(errs, errors.New("plugin describes no steps"))
	}
	seen := map[string]bool{}
	for _, st := range d.GetSteps() {
		if !StepNameRe.MatchString(st.GetName()) {
			errs = append(errs, fmt.Errorf("step name %q must be letters, digits, underscores and hyphens, starting with a letter", st.GetName()))
		}
		if seen[st.GetName()] {
			errs = append(errs, fmt.Errorf("step %q is described twice", st.GetName()))
		}
		seen[st.GetName()] = true
		if _, err := CompileSchema(st.GetName(), st.GetConfigSchema()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ValidateConfig checks a config, given as JSON, against a compiled
// schema.
func ValidateConfig(s *jsonschema.Schema, config []byte) error {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(config))
	if err != nil {
		return fmt.Errorf("config is not JSON: %w", err)
	}
	return s.Validate(v)
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
