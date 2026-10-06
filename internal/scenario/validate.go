package scenario

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$`)

// Validate checks the scenario's structure, then compiles every template
// and expression so problems surface before a run starts.
func (s *Scenario) Validate() error {
	var errs []string
	add := func(path, format string, args ...any) {
		errs = append(errs, path+": "+fmt.Sprintf(format, args...))
	}

	if s.APIVersion != APIVersion {
		add("apiVersion", "unsupported version %q (this build reads %s)", s.APIVersion, APIVersion)
	}
	if s.Kind != KindScenario {
		add("kind", "must be %s", KindScenario)
	}
	if !nameRe.MatchString(s.Metadata.Name) {
		add("metadata.name", "required: lowercase letters, digits, '.', '_' or '-' (got %q)", s.Metadata.Name)
	}

	switch s.Target.Verify {
	case "", "dns-txt", "well-known":
	default:
		add("target.verify", "must be dns-txt or well-known")
	}
	switch s.Target.HTTP.Connections {
	case "per-vu", "shared":
	default:
		add("target.http.connections", "must be per-vu or shared")
	}
	if s.Target.BaseURL != "" && !strings.Contains(s.Target.BaseURL, "${") {
		u, err := url.Parse(s.Target.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add("target.baseURL", "must be an absolute http(s) URL, got %q", s.Target.BaseURL)
		}
	}

	for name, f := range s.Data {
		p := "data." + name
		if !identRe.MatchString(name) {
			add(p, "feeder names must be identifiers")
		}
		sources := 0
		for _, set := range []bool{f.CSV != "", f.JSON != "", len(f.List) > 0, len(f.Range) > 0} {
			if set {
				sources++
			}
		}
		if sources != 1 {
			add(p, "set exactly one of csv, json, list or range")
		}
		if len(f.Range) > 0 && (len(f.Range) != 2 || f.Range[1] < f.Range[0]) {
			add(p+".range", "must be [from, to] with to >= from")
		}
		switch f.Mode {
		case FeedSequential, FeedUnique, FeedRandom, FeedPerVU:
		default:
			add(p+".mode", "must be sequential, unique, random or per-vu")
		}
		switch f.OnExhausted {
		case "stop", "wrap":
		default:
			add(p+".onExhausted", "must be stop or wrap")
		}
	}

	if len(s.Journeys) == 0 {
		add("journeys", "at least one journey is required")
	}
	seen := map[string]bool{}
	hasRelative := false
	for i, j := range s.Journeys {
		p := fmt.Sprintf("journeys[%d]", i)
		if j.Name == "" {
			add(p+".name", "required")
		} else if seen[j.Name] {
			add(p+".name", "duplicate journey name %q", j.Name)
		} else if strings.ContainsAny(j.Name, "/") {
			add(p+".name", "must not contain '/'")
		}
		seen[j.Name] = true
		if j.Weight < 0 {
			add(p+".weight", "must not be negative")
		}
		if len(j.Steps) == 0 {
			add(p+".steps", "a journey needs at least one step")
		}
		walkSteps(j.Steps, func(st Step) {
			if u, ok := stepURL(st); ok && !IsAbsoluteURL(u) && !strings.HasPrefix(u, "${") {
				hasRelative = true
			}
		})
	}
	if hasRelative && s.Target.BaseURL == "" {
		add("target.baseURL", "required because some requests use relative paths")
	}

	if _, err := s.Load.Plan(); err != nil {
		add("load", "%v", err)
	}

	if len(errs) > 0 {
		return &ValidationError{Problems: errs}
	}
	_, err := Compile(s)
	return err
}

// IsAbsoluteURL reports whether a step URL names its own scheme and host,
// rather than a path joined to target.baseURL.
func IsAbsoluteURL(s string) bool {
	for _, p := range []string{"http://", "https://", "ws://", "wss://"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// stepURL returns the URL of a step that makes an HTTP exchange.
func stepURL(st Step) (string, bool) {
	switch st.Kind {
	case StepRequest:
		return st.Request.URL, true
	case StepGraphQL:
		return st.GraphQL.URL, true
	case StepSSE:
		return st.SSE.URL, true
	}
	return "", false
}

func walkSteps(steps []Step, fn func(Step)) {
	for _, st := range steps {
		fn(st)
		switch st.Kind {
		case StepBranch:
			for _, b := range st.Branch {
				walkSteps(b.Steps, fn)
			}
		case StepLoop, StepWhile:
			walkSteps(st.Loop.Steps, fn)
		case StepGroup:
			walkSteps(st.Group.Steps, fn)
		}
	}
}
