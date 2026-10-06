package ai

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// Coverage compares the requests of a scenario with the endpoints of an
// API: which endpoints some journey exercises, which none does, and which
// requests match no endpoint at all. It needs no model.
type Coverage struct {
	Endpoints []EndpointCoverage `json:"endpoints"`
	// Unmatched are requests that use no endpoint of the API.
	Unmatched []RequestRef `json:"unmatched,omitempty"`
	// Templated counts requests whose whole URL is an expression, which
	// cannot be matched statically.
	Templated int `json:"templated,omitempty"`
	Covered   int `json:"covered"`
	Total     int `json:"total"`
}

// EndpointCoverage is one endpoint and the journeys that call it.
type EndpointCoverage struct {
	Endpoint
	Journeys []string `json:"journeys,omitempty"`
}

// RequestRef is a request step in a journey.
type RequestRef struct {
	Journey string `json:"journey"`
	Method  string `json:"method"`
	URL     string `json:"url"`
}

// Percent is the share of endpoints covered.
func (c *Coverage) Percent() float64 {
	if c.Total == 0 {
		return 0
	}
	return float64(c.Covered) / float64(c.Total) * 100
}

// CoverageOf maps a scenario's requests onto the endpoints of und.
func CoverageOf(s *scenario.Scenario, und *Understanding) *Coverage {
	c := &Coverage{}
	hits := make([]map[string]bool, len(und.Endpoints))
	for _, j := range s.Journeys {
		walkRequests(j.Steps, func(r *scenario.Request) {
			raw := strings.TrimSpace(r.URL)
			if strings.HasPrefix(raw, "${") {
				c.Templated++
				return
			}
			if i := strings.Index(raw, "://"); i >= 0 {
				// Absolute URL: keep the path.
				rest := raw[i+3:]
				if k := strings.IndexByte(rest, '/'); k >= 0 {
					raw = rest[k:]
				} else {
					raw = "/"
				}
			}
			matched := false
			for i, e := range und.Endpoints {
				if endpointMatches(und, e, r.Method, raw) {
					if hits[i] == nil {
						hits[i] = map[string]bool{}
					}
					hits[i][j.Name] = true
					matched = true
				}
			}
			if !matched {
				c.Unmatched = append(c.Unmatched, RequestRef{Journey: j.Name, Method: r.Method, URL: r.URL})
			}
		})
	}
	for i, e := range und.Endpoints {
		ec := EndpointCoverage{Endpoint: e}
		for name := range hits[i] {
			ec.Journeys = append(ec.Journeys, name)
		}
		sort.Strings(ec.Journeys)
		if len(ec.Journeys) > 0 {
			c.Covered++
		}
		c.Endpoints = append(c.Endpoints, ec)
	}
	c.Total = len(und.Endpoints)
	return c
}

// endpointMatches reports whether a request matches one endpoint, with or
// without the spec's base path.
func endpointMatches(und *Understanding, e Endpoint, method, rawPath string) bool {
	if !strings.EqualFold(e.Method, method) {
		return false
	}
	one := &Understanding{Endpoints: []Endpoint{e}, BasePath: und.BasePath}
	return matchEndpoint(one, method, rawPath)
}

var paramRe = regexp.MustCompile(`\{[^}/]*\}`)

// SpecDiff lists endpoints added and removed between two versions of an
// API, and the journeys that call a removed one.
type SpecDiff struct {
	Added   []Endpoint            `json:"added,omitempty"`
	Removed []Endpoint            `json:"removed,omitempty"`
	Broken  map[string][]Endpoint `json:"broken,omitempty"` // journey -> removed endpoints it calls
}

// DiffSpecs compares two understandings by method and path, and finds the
// journeys of s that call an endpoint the new one no longer has.
func DiffSpecs(s *scenario.Scenario, old, cur *Understanding) *SpecDiff {
	d := &SpecDiff{Broken: map[string][]Endpoint{}}
	// Renaming a path parameter does not change the endpoint.
	norm := func(e Endpoint) string {
		return strings.ToUpper(e.Method) + " " + paramRe.ReplaceAllString(NormalizePath(e.Path), "{}")
	}
	have := map[string]bool{}
	for _, e := range cur.Endpoints {
		have[norm(e)] = true
	}
	had := map[string]bool{}
	for _, e := range old.Endpoints {
		had[norm(e)] = true
		if !have[norm(e)] {
			d.Removed = append(d.Removed, e)
		}
	}
	for _, e := range cur.Endpoints {
		if !had[norm(e)] {
			d.Added = append(d.Added, e)
		}
	}
	if len(d.Removed) > 0 {
		gone := &Understanding{Endpoints: d.Removed, BasePath: old.BasePath, HasSpec: true}
		for _, ec := range CoverageOf(s, gone).Endpoints {
			for _, j := range ec.Journeys {
				d.Broken[j] = append(d.Broken[j], ec.Endpoint)
			}
		}
	}
	return d
}

// JourneyCheck is the dry-run result of one journey.
type JourneyCheck struct {
	Journey string  `json:"journey"`
	OK      bool    `json:"ok"`
	Traces  []Trace `json:"traces"`
}

// DryRunScenario runs every journey of s once against target with one
// user, under the same host policy and third-party guard as generation.
func DryRunScenario(ctx context.Context, s *scenario.Scenario, target string, allowHosts []string, env, secrets map[string]string, dataDir string) ([]JourneyCheck, error) {
	prog, err := scenario.Compile(s)
	if err != nil {
		return nil, err
	}
	tu, err := url.Parse(target)
	if err != nil || tu.Host == "" {
		return nil, fmt.Errorf("target %q is not an absolute URL", target)
	}
	policy := safety.NewHostPolicy(tu.Hostname(), allowHosts)
	red := NewRedactor()
	for _, v := range secrets {
		red.AddSecret(v)
	}
	dr := &DryRunner{
		Program: prog, BaseURL: strings.TrimRight(target, "/"), Env: env, Secrets: secrets, DataDir: dataDir, Redactor: red,
		Allow: func(u *url.URL) string {
			if cat := ThirdPartyCategory(u.Hostname()); cat != "" {
				return fmt.Sprintf("%s is a blocked %s provider", u.Hostname(), cat)
			}
			if !policy.Allow(u) {
				return fmt.Sprintf("%s is not the target or an allowed host", u.Hostname())
			}
			return ""
		},
	}
	var out []JourneyCheck
	for _, j := range prog.Journeys {
		traces := dr.RunJourney(ctx, j)
		ok := len(traces) > 0
		for _, tr := range traces {
			ok = ok && tr.OK
		}
		out = append(out, JourneyCheck{Journey: j.Name, OK: ok, Traces: traces})
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
	}
	return out, nil
}
