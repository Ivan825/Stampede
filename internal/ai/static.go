package ai

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// Problem is something wrong with a proposed scenario.
type Problem struct {
	// Journey is the journey the problem belongs to; empty means the whole
	// scenario.
	Journey string `json:"journey,omitempty"`
	Message string `json:"message"`
	// Fatal problems make the scenario unusable: it does not validate or
	// it would send traffic to a blocked third party. Other problems flag
	// a journey for review.
	Fatal bool `json:"fatal,omitempty"`
}

func (p Problem) String() string {
	if p.Journey != "" {
		return "journey " + p.Journey + ": " + p.Message
	}
	return p.Message
}

var (
	journeyIndexRe = regexp.MustCompile(`journeys\[(\d+)\]`)
	journeyNameRe  = regexp.MustCompile(`journeys\[([^\]]+)\]`)
)

// staticCheck validates the scenario (structure, expressions and variable
// flow), then checks every request against the known endpoints and the
// third-party guard. targetHost is the host relative paths resolve to.
func staticCheck(s *scenario.Scenario, und *Understanding, targetHost string, allowHosts []string) []Problem {
	var out []Problem
	if err := s.Validate(); err != nil {
		var ve *scenario.ValidationError
		if errors.As(err, &ve) {
			for _, msg := range ve.Problems {
				out = append(out, Problem{Journey: journeyOf(s, msg), Message: msg, Fatal: true})
			}
		} else {
			out = append(out, Problem{Message: err.Error(), Fatal: true})
		}
	}
	allowed := map[string]bool{strings.ToLower(targetHost): true}
	for _, h := range allowHosts {
		allowed[strings.ToLower(h)] = true
	}
	out = append(out, grpcProblems(s, und)...)
	for _, j := range s.Journeys {
		seen := map[string]bool{}
		walkRequests(j.Steps, func(r *scenario.Request) {
			for _, p := range checkRequest(r, und, allowed) {
				if !seen[p.Message] {
					seen[p.Message] = true
					p.Journey = j.Name
					out = append(out, p)
				}
			}
		})
	}
	return out
}

func journeyOf(s *scenario.Scenario, msg string) string {
	if m := journeyIndexRe.FindStringSubmatch(msg); m != nil {
		if i, err := strconv.Atoi(m[1]); err == nil && i < len(s.Journeys) {
			return s.Journeys[i].Name
		}
	}
	if m := journeyNameRe.FindStringSubmatch(msg); m != nil {
		for _, j := range s.Journeys {
			if j.Name == m[1] {
				return j.Name
			}
		}
	}
	return ""
}

// walkRequests visits request steps, including those nested in branches,
// loops and groups. Step kinds it does not know are skipped.
func walkRequests(steps []scenario.Step, fn func(*scenario.Request)) {
	for _, st := range steps {
		switch {
		case st.Kind == scenario.StepRequest && st.Request != nil:
			fn(st.Request)
		case st.Kind == scenario.StepBranch:
			for _, b := range st.Branch {
				walkRequests(b.Steps, fn)
			}
		case (st.Kind == scenario.StepLoop || st.Kind == scenario.StepWhile) && st.Loop != nil:
			walkRequests(st.Loop.Steps, fn)
		case st.Kind == scenario.StepGroup && st.Group != nil:
			walkRequests(st.Group.Steps, fn)
		}
	}
}

func checkRequest(r *scenario.Request, und *Understanding, allowed map[string]bool) []Problem {
	raw := strings.TrimSpace(r.URL)
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(maskTemplates(raw))
		if err != nil || strings.Contains(u.Host, "\x00") {
			return nil
		}
		host := strings.ToLower(u.Hostname())
		if cat := ThirdPartyCategory(host); cat != "" {
			return []Problem{{Message: fmt.Sprintf("%s %s calls %s, a %s provider; load tests must never call payment, SMS, email or CAPTCHA services (use a test-mode host or remove the step)", r.Method, raw, host, cat), Fatal: true}}
		}
		if !allowed[host] {
			return []Problem{{Message: fmt.Sprintf("%s %s calls %s, which is not the target; use a relative path on the target", r.Method, raw, host)}}
		}
		return endpointProblem(r.Method, u.EscapedPath(), und)
	}
	if strings.HasPrefix(raw, "${") {
		return nil // fully templated; cannot be checked statically
	}
	return endpointProblem(r.Method, raw, und)
}

func endpointProblem(method, rawPath string, und *Understanding) []Problem {
	if und == nil || !und.HasSpec || len(und.Endpoints) == 0 {
		return nil
	}
	if matchEndpoint(und, method, rawPath) {
		return nil
	}
	near := nearestEndpoints(und, rawPath)
	msg := fmt.Sprintf("%s %s is not an endpoint of the API", method, rawPath)
	if len(near) > 0 {
		msg += " (known endpoints with a similar path: " + strings.Join(near, ", ") + ")"
	}
	return []Problem{{Message: msg}}
}

// maskTemplates replaces every ${...} with a NUL byte so the rest of the
// URL can be parsed; braces inside expressions are balanced.
func maskTemplates(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			depth := 0
			j := i + 1
			for ; j < len(s); j++ {
				if s[j] == '{' {
					depth++
				} else if s[j] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			b.WriteByte(0)
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func pathSegments(p string) []string {
	p = maskTemplates(p)
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func matchEndpoint(und *Understanding, method, rawPath string) bool {
	segs := pathSegments(rawPath)
	var candidates [][]string
	candidates = append(candidates, segs)
	if bp := pathSegments(und.BasePath); len(bp) > 0 && len(segs) >= len(bp) {
		strip := true
		for i := range bp {
			if segs[i] != bp[i] {
				strip = false
			}
		}
		if strip {
			candidates = append(candidates, segs[len(bp):])
		}
	}
	for _, e := range und.Endpoints {
		if !strings.EqualFold(e.Method, method) {
			continue
		}
		es := pathSegments(e.Path)
		for _, c := range candidates {
			if segmentsMatch(es, c) {
				return true
			}
		}
	}
	return false
}

func segmentsMatch(spec, step []string) bool {
	if len(spec) != len(step) {
		return false
	}
	for i := range spec {
		switch {
		case strings.HasPrefix(spec[i], "{") && step[i] != "":
		case strings.Contains(step[i], "\x00"):
		case spec[i] != step[i]:
			return false
		}
	}
	return true
}

func nearestEndpoints(und *Understanding, rawPath string) []string {
	segs := pathSegments(rawPath)
	type scored struct {
		key   string
		score int
	}
	var all []scored
	for _, e := range und.Endpoints {
		es := pathSegments(e.Path)
		n := 0
		for i := 0; i < len(es) && i < len(segs) && (es[i] == segs[i] || strings.HasPrefix(es[i], "{")); i++ {
			n++
		}
		if n > 0 {
			all = append(all, scored{e.Key(), n})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	var out []string
	for i := 0; i < len(all) && i < 4; i++ {
		out = append(out, all[i].key)
	}
	return out
}
