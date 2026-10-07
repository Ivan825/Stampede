package scenario

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Ivan825/Stampede/internal/feed"
	"github.com/Ivan825/Stampede/internal/protocol/netem"
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
	switch r := s.Target.HTTP.TLSResumption; {
	case r == "per-vu" && s.Target.HTTP.Connections == "shared":
		add("target.http.tlsResumption", "per-vu needs per-vu connections; with shared connections use shared or off")
	case r != "" && r != "per-vu" && r != "shared" && r != "off":
		add("target.http.tlsResumption", "must be per-vu, shared or off")
	}
	if s.Target.BaseURL != "" && !strings.Contains(s.Target.BaseURL, "${") {
		u, err := url.Parse(s.Target.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add("target.baseURL", "must be an absolute http(s) URL, got %q", s.Target.BaseURL)
		}
	}

	if n := s.Target.Network; n != nil {
		if _, err := n.Resolve(); err != nil {
			add("target.network", "%v", err)
		}
	}

	if s.Faults != nil {
		s.validateFaults(add)
	}

	for name, f := range s.Data {
		p := "data." + name
		if !identRe.MatchString(name) {
			add(p, "feeder names must be identifiers")
		}
		sources := 0
		for _, set := range []bool{f.CSV != "", f.JSON != "", len(f.List) > 0, len(f.Range) > 0, len(f.Generate) > 0, f.SQL != nil} {
			if set {
				sources++
			}
		}
		if sources != 1 {
			add(p, "set exactly one of csv, json, list, range, generate or sql")
		}
		for field, kind := range f.Generate {
			if _, err := feed.Compile(kind); err != nil {
				add(p+".generate."+field, "%s", err)
			}
		}
		if q := f.SQL; q != nil {
			if _, ok := feed.Drivers[q.Driver]; !ok {
				add(p+".sql.driver", "must be postgres or mysql")
			}
			if strings.TrimSpace(q.DSN) == "" {
				add(p+".sql.dsn", "required")
			}
			if strings.TrimSpace(q.Query) == "" {
				add(p+".sql.query", "required")
			}
			if q.Limit < 0 {
				add(p+".sql.limit", "must not be negative")
			}
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

	replay := s.Load.Mode == ModeReplay
	if replay {
		switch r := s.Load.Replay; {
		case r == nil || strings.TrimSpace(r.File) == "":
			add("load.replay.file", "required: an access log or HAR file to replay")
		default:
			if r.Speed < 0 {
				add("load.replay.speed", "must be positive")
			}
			if r.Limit < 0 {
				add("load.replay.limit", "must not be negative")
			}
			switch r.Format {
			case "", "auto", "log", "har":
			default:
				add("load.replay.format", "must be auto, log or har")
			}
		}
		if s.Target.BaseURL == "" {
			add("target.baseURL", "required: recorded paths are sent to it")
		}
	} else if s.Load.Replay != nil {
		add("load.replay", "set load.mode to replay to replay a recording")
	}
	if len(s.Journeys) == 0 && !replay {
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
			if st.Kind == StepGRPC && st.GRPC.Target == "" {
				hasRelative = true
			}
		})
	}
	if hasRelative && s.Target.BaseURL == "" {
		add("target.baseURL", "required because some requests use relative paths (or gRPC steps have no target)")
	}

	// A replay plan comes from the recording, which LoadReplay reads
	// after paths are resolved.
	if !replay || s.Load.Replay.Recording() != nil {
		if _, err := s.Load.Plan(); err != nil {
			add("load", "%v", err)
		}
	}
	if o := s.Observe; o != nil {
		for _, p := range o.problems() {
			add(p[0], "%s", p[1])
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Problems: errs}
	}
	_, err := compile(s)
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
	case StepWS:
		return st.WS.URL, true
	case StepBrowser:
		return st.Browser.URL, true
	case StepGoto:
		return st.Action.Target, true
	}
	return "", false
}

// EachStep calls fn for every step of every journey, nested ones included.
func (s *Scenario) EachStep(fn func(Step)) {
	for _, j := range s.Journeys {
		walkSteps(j.Steps, fn)
	}
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
		case StepWS:
			walkSteps(st.WS.Steps, fn)
		case StepBrowser:
			walkSteps(st.Browser.Steps, fn)
		}
	}
}

func (s *Scenario) validateFaults(add func(path, format string, args ...any)) {
	fs := s.Faults
	switch ag := fs.Agent; {
	case ag.Integration != "" && (ag.URL != "" || ag.Token != ""):
		add("faults.agent", "set either integration (on a server) or url and token (with stampede run), not both")
	case ag.Integration != "":
	case strings.TrimSpace(ag.URL) == "":
		add("faults.agent.url", "required: the agent's control API, e.g. ${env.AGENT_URL} (or name an integration on a server)")
	default:
		if !strings.Contains(ag.URL, "${") {
			if u, err := url.Parse(ag.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				add("faults.agent.url", "must be an absolute http(s) URL, got %q", ag.URL)
			}
		}
		if strings.TrimSpace(ag.Token) == "" {
			add("faults.agent.token", "required with url, e.g. ${secret.AGENT_TOKEN}")
		}
	}
	if len(fs.Timeline) == 0 {
		add("faults.timeline", "add at least one fault")
	}
	for i, f := range fs.Timeline {
		p := fmt.Sprintf("faults.timeline[%d]", i)
		if f.At < 0 {
			add(p+".at", "must not be negative")
		}
		if f.For <= 0 {
			add(p+".for", "required: how long the fault lasts")
		}
		kinds := 0
		for _, set := range []bool{f.Proxy != "", f.Container != "", f.Deployment != ""} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			add(p, "set exactly one of proxy, container or deployment")
			continue
		}
		proxyFields := f.Latency != 0 || f.Jitter != 0 || f.Bandwidth != "" || f.Reset || f.Refuse || f.Blackhole
		switch {
		case f.Proxy != "":
			if !proxyFields {
				add(p, "a proxy fault needs latency, jitter, bandwidth, reset, refuse or blackhole")
			}
			if f.Latency < 0 || f.Jitter < 0 {
				add(p, "latency and jitter must not be negative")
			}
			if f.Bandwidth != "" {
				if _, err := netem.ParseBandwidth(f.Bandwidth); err != nil {
					add(p+".bandwidth", "%v", err)
				}
			}
			if f.Action != "" || f.Replicas != nil {
				add(p, "action and replicas apply to containers and deployments, not proxies")
			}
		case f.Container != "":
			switch f.Action {
			case "pause", "stop", "kill", "restart":
			default:
				add(p+".action", "must be pause, stop, kill or restart")
			}
			if proxyFields || f.Replicas != nil {
				add(p, "a container fault takes only action")
			}
		default:
			if ns, name, ok := strings.Cut(f.Deployment, "/"); !ok || ns == "" || name == "" {
				add(p+".deployment", "use namespace/name")
			}
			if f.Replicas == nil || *f.Replicas < 0 {
				add(p+".replicas", "required and must not be negative")
			}
			if proxyFields || f.Action != "" {
				add(p, "a deployment fault takes only replicas")
			}
		}
	}
}
