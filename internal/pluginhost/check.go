package pluginhost

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// CheckProgram checks every plugin step of prog against the plugins in
// set: the plugin offers the step and the step's config fits its schema.
// A string containing ${} is only rendered at run time, so problems with
// its value (its type, format or range) are left for the plugin to report
// then; everything else (missing or unknown settings, literal values) is
// checked now.
func CheckProgram(prog *scenario.Program, set Set) error {
	var problems []string
	for _, st := range prog.Steps {
		cp := st.Plugin
		if cp == nil {
			continue
		}
		where := fmt.Sprintf("journeys[%s] step %q", st.Journey, st.Name)
		p := set[cp.Plugin]
		if p == nil {
			problems = append(problems, fmt.Sprintf("%s: plugin %s is not loaded", where, cp.Plugin))
			continue
		}
		stt := p.Steps[cp.Step]
		if stt == nil {
			problems = append(problems, fmt.Sprintf("%s: plugin %s has no step %q (it has %s)", where, cp.Plugin, cp.Step, strings.Join(p.StepNames(), ", ")))
			continue
		}
		for _, msg := range configProblems(stt.Compiled, cp.Config, cp.Templated) {
			problems = append(problems, fmt.Sprintf("%s: with%s", where, msg))
		}
	}
	if len(problems) > 0 {
		return &scenario.ValidationError{Problems: problems}
	}
	return nil
}

// configProblems validates a config as written, ignoring failures that
// are only about a templated string's value.
func configProblems(s *jsonschema.Schema, config map[string]any, templated []string) []string {
	err := s.Validate(toJSONSchemaValue(config))
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		if err != nil {
			return []string{": " + err.Error()}
		}
		return nil
	}
	kept := filterTemplated(ve, templated)
	if kept == nil {
		return nil
	}
	pr := message.NewPrinter(language.English)
	var out []string
	var walk func(*jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := ""
			if len(e.InstanceLocation) > 0 {
				loc = "." + strings.Join(e.InstanceLocation, ".")
			}
			msg := loc + ": " + e.ErrorKind.LocalizedString(pr)
			if !slices.Contains(out, msg) {
				out = append(out, msg)
			}
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(kept)
	return out
}

// filterTemplated drops the parts of a validation error caused only by
// templated strings, returning nil when nothing is left.
func filterTemplated(e *jsonschema.ValidationError, templated []string) *jsonschema.ValidationError {
	if len(e.Causes) == 0 {
		ptr := pointer(e.InstanceLocation)
		for _, t := range templated {
			if ptr == t || strings.HasPrefix(ptr, t+"/") {
				return nil
			}
		}
		return e
	}
	var kept []*jsonschema.ValidationError
	dropped := false
	for _, c := range e.Causes {
		if k := filterTemplated(c, templated); k != nil {
			kept = append(kept, k)
		} else {
			dropped = true
		}
	}
	switch e.ErrorKind.(type) {
	case *kind.AnyOf, *kind.OneOf:
		// One alternative failed only on a templated value: it may well
		// match at run time.
		if dropped {
			return nil
		}
	}
	if len(kept) == 0 {
		return nil
	}
	out := *e
	out.Causes = kept
	return &out
}

func pointer(loc []string) string {
	var b strings.Builder
	for _, p := range loc {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(p, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

// toJSONSchemaValue converts YAML-decoded values into the types the
// validator expects (numbers as float64 or json.Number-compatible ints).
func toJSONSchemaValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = toJSONSchemaValue(e)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, e := range x {
			a[i] = toJSONSchemaValue(e)
		}
		return a
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	default:
		return v
	}
}

// TargetHosts returns the hosts a config's target value names. It
// understands URLs (mqtt://host:1883, postgres://u@h1,h2/db), host:port
// lists, PostgreSQL key=value DSNs (host=db port=5432) and MySQL DSNs
// (user:pw@tcp(db:3306)/app). Unix sockets and SQLite file: URIs count as
// localhost. An address it cannot read is an error, so the policy fails
// closed.
func TargetHosts(v any) ([]string, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string:
		return addrHosts(x)
	case []any:
		var out []string
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("target address %v is not a string", e)
			}
			hs, err := addrHosts(s)
			if err != nil {
				return nil, err
			}
			out = append(out, hs...)
		}
		return out, nil
	}
	return nil, fmt.Errorf("target address %v is not a string", v)
}

var (
	mysqlNetRe = regexp.MustCompile(`@(tcp6?|tcp4|udp|unix)\(([^)]*)\)`)
	kvHostRe   = regexp.MustCompile(`(?:^|\s)(?:host|hostaddr)\s*=\s*'?([^\s']+)`)
)

func addrHosts(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return nil, nil
	case s == ":memory:", strings.HasPrefix(s, "file:") && !strings.HasPrefix(s, "file://"):
		// An SQLite database: a local file or memory.
		return []string{"localhost"}, nil
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("cannot read the host in %q: %w", redact(s), err)
		}
		if u.Host == "" {
			if u.Scheme == "unix" {
				return []string{"localhost"}, nil
			}
			if h := u.Query().Get("host"); h != "" {
				return splitHosts(h)
			}
			return []string{"localhost"}, nil
		}
		return splitHosts(u.Host)
	case mysqlNetRe.MatchString(s):
		m := mysqlNetRe.FindStringSubmatch(s)
		if m[1] == "unix" || m[2] == "" {
			return []string{"localhost"}, nil
		}
		return splitHosts(m[2])
	case kvHostRe.MatchString(s):
		return splitHosts(kvHostRe.FindStringSubmatch(s)[1])
	case strings.Contains(s, "@/"):
		// A MySQL DSN with no address uses localhost.
		return []string{"localhost"}, nil
	case strings.ContainsAny(s, " =@/"):
		return nil, fmt.Errorf("cannot read the host in %q", redact(s))
	}
	return splitHosts(s)
}

// splitHosts splits a comma-separated host list; unix socket paths count
// as localhost.
func splitHosts(s string) ([]string, error) {
	var out []string
	for _, h := range strings.Split(s, ",") {
		h = strings.TrimSpace(h)
		switch {
		case h == "":
		case strings.HasPrefix(h, "/"):
			out = append(out, "localhost")
		default:
			if host, _, err := net.SplitHostPort(h); err == nil {
				h = host
			}
			out = append(out, strings.Trim(h, "[]"))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no host in %q", s)
	}
	return out, nil
}

// redact hides a password in an address before it is shown.
var pwRe = regexp.MustCompile(`(://[^:/@]*:|password\s*=\s*)[^@\s]*`)

func redact(s string) string { return pwRe.ReplaceAllString(s, "${1}***") }
