package ai

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// parseDraft turns a model's JSON reply into a YAML node tree (keeping
// the model's key order) and a decoded scenario. The node tree is what is
// written out, so the proposal reads like a hand-written file.
func parseDraft(js string) (*yaml.Node, *scenario.Scenario, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(js), &doc); err != nil {
		return nil, nil, fmt.Errorf("the reply is not valid JSON: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, errors.New("the reply must be one JSON object (the scenario)")
	}
	root := doc.Content[0]
	// Accept {"scenario": {...}} as well as the bare scenario.
	if v := mapGet(root, "scenario"); v != nil && v.Kind == yaml.MappingNode && mapGet(root, "journeys") == nil {
		root = v
		doc.Content[0] = v
	}
	plain(root)
	if js := mapGet(root, "journeys"); js != nil && js.Kind == yaml.SequenceNode {
		for _, j := range js.Content {
			tidySteps(mapGet(j, "steps"))
		}
	}
	if mapGet(root, "kind") == nil {
		mapPrepend(root, "kind", scenario.KindScenario)
	}
	if mapGet(root, "apiVersion") == nil {
		mapPrepend(root, "apiVersion", scenario.APIVersion)
	}
	src, err := encodeYAML(&doc)
	if err != nil {
		return nil, nil, err
	}
	s, err := scenario.Decode(src)
	if err != nil {
		return &doc, nil, err
	}
	return &doc, s, nil
}

// plain clears flow and quoting styles inherited from JSON so the encoder
// writes ordinary block YAML and quotes only where needed.
func plain(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		plain(c)
	}
}

// stepLead are keys written first in a step, in this order, so each step
// reads "name, action, details" like hand-written files.
var stepLead = []string{"name", "if", "get", "post", "put", "patch", "delete", "head", "options", "think", "branch", "loop", "while", "max", "group"}

// tidySteps reorders step keys, recursing into nested steps.
func tidySteps(steps *yaml.Node) {
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return
	}
	for _, st := range steps.Content {
		if st.Kind != yaml.MappingNode {
			continue
		}
		var lead, rest []*yaml.Node
		for _, k := range stepLead {
			for i := 0; i+1 < len(st.Content); i += 2 {
				if st.Content[i].Value == k {
					lead = append(lead, st.Content[i], st.Content[i+1])
				}
			}
		}
		for i := 0; i+1 < len(st.Content); i += 2 {
			if !contains(stepLead, st.Content[i].Value) {
				rest = append(rest, st.Content[i], st.Content[i+1])
			}
		}
		st.Content = append(lead, rest...)
		tidySteps(mapGet(st, "steps"))
		if br := mapGet(st, "branch"); br != nil && br.Kind == yaml.SequenceNode {
			for _, b := range br.Content {
				tidySteps(mapGet(b, "steps"))
			}
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func mapPrepend(m *yaml.Node, key, value string) {
	m.Content = append([]*yaml.Node{scalar(key), scalar(value)}, m.Content...)
}

func mapSet(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, scalar(key), value)
}

func mapDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// setBaseURL sets target.baseURL, or removes it when base is empty (the
// server supplies it from the run's target). The key is placed after
// metadata when the model left target out entirely.
func setBaseURL(doc *yaml.Node, base string) {
	root := doc.Content[0]
	t := mapGet(root, "target")
	if t == nil || t.Kind != yaml.MappingNode {
		if base == "" {
			return
		}
		t = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mapDelete(root, "target")
		at := len(root.Content)
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "metadata" {
				at = i + 2
			}
		}
		root.Content = append(root.Content[:at], append([]*yaml.Node{scalar("target"), t}, root.Content[at:]...)...)
	}
	if base == "" {
		mapDelete(t, "baseURL")
		if len(t.Content) == 0 {
			mapDelete(root, "target")
		}
		return
	}
	mapSet(t, "baseURL", scalar(base))
}

// annotate adds a header comment and marks flagged journeys.
func annotate(doc *yaml.Node, header string, flagged map[string]string) {
	doc.HeadComment = commentLines(header)
	journeys := mapGet(doc.Content[0], "journeys")
	if journeys == nil || journeys.Kind != yaml.SequenceNode {
		return
	}
	for _, j := range journeys.Content {
		name := mapGet(j, "name")
		if name == nil {
			continue
		}
		if why, ok := flagged[name.Value]; ok {
			j.HeadComment = commentLines("FLAGGED FOR REVIEW: this journey did not pass its dry run.\n" + why)
		} else {
			j.HeadComment = ""
		}
	}
}

func commentLines(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "# " + strings.TrimSpace(l)
	}
	return strings.Join(lines, "\n")
}

func encodeYAML(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnifiedDiff compares two texts line by line and returns a unified diff
// with three lines of context, or "" when they are equal.
func UnifiedDiff(a, b, nameA, nameB string) string {
	if a == b {
		return ""
	}
	x := splitLines(a)
	y := splitLines(b)
	if len(x)*len(y) > 16_000_000 {
		return fmt.Sprintf("--- %s\n+++ %s\n(files too large to diff: %d and %d lines)\n", nameA, nameB, len(x), len(y))
	}
	// Longest common subsequence table, filled from the end.
	n, m := len(x), len(y)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte // ' ', '-', '+'
		text string
		ai   int // line number in a (1-based) for ' ' and '-'
		bi   int // line number in b for ' ' and '+'
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			ops = append(ops, op{' ', x[i], i + 1, j + 1})
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, op{'+', y[j], i + 1, j + 1})
			j++
		default:
			ops = append(ops, op{'-', x[i], i + 1, j + 1})
			i++
		}
	}
	const ctx = 3
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", nameA, nameB)
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := max(k-ctx, 0)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			// Stop when the next change is more than 2*ctx lines away.
			run := 0
			for end+run < len(ops) && ops[end+run].kind == ' ' {
				run++
			}
			if end+run >= len(ops) || run > 2*ctx {
				end = min(end+ctx, len(ops))
				break
			}
			end += run
		}
		aStart, bStart, aLen, bLen := 0, 0, 0, 0
		for _, o := range ops[start:end] {
			if o.kind != '+' {
				if aLen == 0 {
					aStart = o.ai
				}
				aLen++
			}
			if o.kind != '-' {
				if bLen == 0 {
					bStart = o.bi
				}
				bLen++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aLen, bStart, bLen)
		for _, o := range ops[start:end] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		k = end
	}
	return out.String()
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
