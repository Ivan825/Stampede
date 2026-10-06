package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Ivan825/Stampede/internal/ai/provider"
	"github.com/Ivan825/Stampede/internal/report"
)

// narrativeSchema constrains the model's reply.
var narrativeSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["summary", "claims"],
  "properties": {
    "summary": {"type": "string", "description": "Two or three plain sentences: did the run pass, and what matters most."},
    "claims": {
      "type": "array",
      "maxItems": 8,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["text", "label", "refs"],
        "properties": {
          "text": {"type": "string"},
          "label": {"type": "string", "enum": ["measured", "suspected"]},
          "refs": {"type": "array", "items": {"type": "string"}}
        }
      }
    }
  }
}`)

const narrativeSystem = `You write the summary at the top of a load test report from Stampede.

You are given the report's facts as a JSON list of {id, text}. Use only these facts.

Rules:
- Write a summary of two or three plain sentences: whether the run met its targets and the one or two things that matter most.
- Then write up to eight claims. Each claim is one sentence and cites the ids of the facts it rests on in "refs".
- Label a claim "measured" only when the cited facts state it directly. Every number in a measured claim must appear in a cited fact.
- Label a claim "suspected" when it is a likely cause or an interpretation the facts suggest but do not prove (for example, rising connect time suggesting connection pool exhaustion). Say "likely" or "suggests" in its text.
- Never invent figures, endpoints, components or causes that no fact supports. Every number in the summary must appear in some fact.
- Latency is measured from each request's scheduled send time, so it includes queueing; service time is from the actual send.
- If the verdict is generator-limited, say the load generator, not the target, limited the run.
- Plain, direct language. No headings, no markdown, no exclamation marks.

Reply with one JSON object: {"summary": "...", "claims": [{"text": "...", "label": "measured", "refs": ["overall.latency"]}]}.`

// NarrateOptions configures Narrate.
type NarrateOptions struct {
	Provider  provider.Provider
	MaxTokens int // per reply; default 4000
	// Repairs is how many times a reply with uncited or unsupported claims
	// is sent back; default 1, negative disables.
	Repairs int
}

// Narrate asks a model to summarise a finished report. Every claim it
// keeps cites facts from r.Facts(); claims citing unknown facts, measured
// claims citing none, and measured claims with figures not found in their
// citations are dropped. The model never sees request or response data,
// only the report's aggregate figures, with error texts redacted.
func Narrate(ctx context.Context, r *report.Report, opts NarrateOptions) (*report.Narrative, provider.Usage, error) {
	var usage provider.Usage
	if opts.Provider == nil {
		return nil, usage, errors.New("no AI provider configured")
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 4000
	}
	if opts.Repairs == 0 {
		opts.Repairs = 1
	}
	facts := r.Facts()
	red := NewRedactor()
	for i := range facts {
		if strings.HasPrefix(facts[i].ID, "error.") || strings.HasPrefix(facts[i].ID, "note.") {
			facts[i].Text = red.Text(facts[i].Text)
		}
	}
	type wireFact struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	wf := make([]wireFact, len(facts))
	for i, f := range facts {
		wf[i] = wireFact{f.ID, f.Text}
	}
	js, _ := json.MarshalIndent(wf, "", " ")
	msgs := []provider.Message{{Role: "user", Content: "Facts from the report of scenario " + quoteName(r.Scenario) + ":\n" + string(js)}}

	var best *report.Narrative
	for round := 0; ; round++ {
		resp, err := opts.Provider.Chat(ctx, &provider.Request{
			System: narrativeSystem, Messages: msgs, JSONSchema: narrativeSchema, SchemaName: "narrative", MaxTokens: opts.MaxTokens,
		})
		if resp != nil {
			usage.Add(resp.Usage)
		}
		if err != nil {
			if best != nil && strings.TrimSpace(best.Summary) != "" {
				best.KeepCited(facts)
				return best, usage, nil
			}
			return nil, usage, err
		}
		var n report.Narrative
		if err := json.Unmarshal([]byte(resp.Text), &n); err != nil {
			if best != nil && strings.TrimSpace(best.Summary) != "" {
				best.KeepCited(facts)
				return best, usage, nil
			}
			return nil, usage, fmt.Errorf("the model's reply is not a narrative: %w", err)
		}
		n.Model = resp.Model
		if n.Model == "" {
			n.Model = opts.Provider.Model()
		}
		problems := checkNarrative(&n, facts)
		if best == nil || len(problems) == 0 || len(n.Claims) > len(best.Claims) {
			cp := n
			best = &cp
		}
		if len(problems) == 0 || round >= opts.Repairs || opts.Repairs < 0 {
			if strings.TrimSpace(best.Summary) == "" {
				return nil, usage, errors.New("the model returned an empty summary")
			}
			best.KeepCited(facts)
			return best, usage, nil
		}
		msgs = append(msgs,
			provider.Message{Role: "assistant", Content: resp.Text},
			provider.Message{Role: "user", Content: "Some of that was not supported by the facts and has been removed:\n- " +
				strings.Join(problems, "\n- ") + "\nReply again with the whole JSON object, following the rules."})
	}
}

var numberRe = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

// checkNarrative applies report.Narrative.Check and also requires every
// figure in a measured claim to appear in the facts it cites, and every
// figure in the summary to appear in some fact. Unsupported claims are
// dropped; an unsupported summary is cleared. It returns what was wrong.
func checkNarrative(n *report.Narrative, facts []report.Fact) []string {
	var problems []string
	byID := map[string]string{}
	var all strings.Builder
	for _, f := range facts {
		byID[f.ID] = f.Text
		all.WriteString(f.Text + "\n")
	}
	kept := n.Claims[:0]
	for _, c := range n.Claims {
		var cited strings.Builder
		for _, r := range c.Refs {
			cited.WriteString(byID[r] + "\n")
		}
		switch {
		case c.Label != "measured" && c.Label != "suspected":
			problems = append(problems, fmt.Sprintf("%q: the label must be measured or suspected", c.Text))
			continue
		case c.Label == "measured" && len(c.Refs) == 0:
			problems = append(problems, fmt.Sprintf("%q: a measured claim must cite facts", c.Text))
			continue
		}
		bad := ""
		for _, r := range c.Refs {
			if _, ok := byID[r]; !ok {
				bad = fmt.Sprintf("%q cites %q, which is not a fact id", c.Text, r)
				break
			}
		}
		if bad == "" && c.Label == "measured" {
			if x := unsupported(c.Text, cited.String()); x != "" {
				bad = fmt.Sprintf("%q: %s does not appear in the cited facts", c.Text, x)
			}
		}
		if bad != "" {
			problems = append(problems, bad)
			continue
		}
		kept = append(kept, c)
	}
	n.Claims = kept
	if x := unsupported(n.Summary, all.String()); x != "" {
		problems = append(problems, fmt.Sprintf("the summary says %s, which no fact states", x))
		n.Summary = ""
	}
	// Belt and braces: the report package's own check.
	n.Check(facts)
	return problems
}

// unsupported returns the first figure in text that does not occur in
// source, comparing by value (840ms matches 840.0ms) and ignoring
// single-digit counting numbers.
func unsupported(text, source string) string {
	have := map[string]bool{}
	for _, m := range numberRe.FindAllString(source, -1) {
		have[canonical(m)] = true
	}
	for _, m := range numberRe.FindAllString(text, -1) {
		v := canonical(m)
		if len(v) == 1 { // "one", "2 journeys": counting words, not figures
			continue
		}
		if !have[v] {
			return m
		}
	}
	return ""
}

// canonical writes a number without thousands separators or trailing
// decimal zeros.
func canonical(m string) string {
	m = strings.ReplaceAll(m, ",", "")
	f, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return m
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func quoteName(s string) string {
	if s == "" {
		return "(unnamed)"
	}
	return fmt.Sprintf("%q", s)
}
