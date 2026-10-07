package client

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Ivan825/Stampede/internal/api/gen"
)

// pick resolves ref among items: an exact id or key first (keys compare
// without case), then a unique id prefix of at least four characters.
func pick[T any](items []T, what, ref string, id func(T) string, keys func(T) []string) (T, error) {
	var zero T
	if ref == "" {
		return zero, fmt.Errorf("name the %s", what)
	}
	for _, it := range items {
		if id(it) == ref {
			return it, nil
		}
		for _, k := range keys(it) {
			if k != "" && strings.EqualFold(k, ref) {
				return it, nil
			}
		}
	}
	var match []T
	if len(ref) >= 4 {
		for _, it := range items {
			if strings.HasPrefix(id(it), strings.ToLower(ref)) {
				match = append(match, it)
			}
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return zero, fmt.Errorf("%s %q not found", what, ref)
	default:
		return zero, fmt.Errorf("%q matches %d %ss; give more characters", ref, len(match), what)
	}
}

// FindUser resolves a member of the organisation by id or email.
func (c *Client) FindUser(ctx context.Context, ref string) (gen.User, error) {
	var us []gen.User
	if err := c.Do(ctx, "GET", "/users", nil, &us); err != nil {
		return gen.User{}, err
	}
	return pick(us, "user", ref, func(u gen.User) string { return u.Id.String() }, func(u gen.User) []string { return []string{u.Email} })
}

// FindToken resolves one of the caller's API tokens by id, name or prefix.
func (c *Client) FindToken(ctx context.Context, ref string) (gen.Token, error) {
	var ts []gen.Token
	if err := c.Do(ctx, "GET", "/tokens", nil, &ts); err != nil {
		return gen.Token{}, err
	}
	return pick(ts, "token", ref, func(t gen.Token) string { return t.Id.String() }, func(t gen.Token) []string { return []string{t.Name, t.Prefix} })
}

// FindScenarioIn resolves a scenario by id, name or a unique id prefix.
func (c *Client) FindScenarioIn(ctx context.Context, project, ref string) (gen.Scenario, error) {
	var ss []gen.Scenario
	if err := c.Do(ctx, "GET", "/projects/"+project+"/scenarios", nil, &ss); err != nil {
		return gen.Scenario{}, err
	}
	if ref == "" && len(ss) == 1 {
		return ss[0], nil
	}
	return pick(ss, "scenario", ref, func(s gen.Scenario) string { return s.Id.String() }, func(s gen.Scenario) []string { return []string{s.Name} })
}

// FindAIProvider resolves an AI provider by id or name.
func (c *Client) FindAIProvider(ctx context.Context, ref string) (gen.AIProvider, error) {
	var ps []gen.AIProvider
	if err := c.Do(ctx, "GET", "/ai/providers", nil, &ps); err != nil {
		return gen.AIProvider{}, err
	}
	return pick(ps, "AI provider", ref, func(p gen.AIProvider) string { return p.Id.String() }, func(p gen.AIProvider) []string { return []string{p.Name} })
}

// FindAIJob resolves a project's AI job by id or a unique id prefix.
func (c *Client) FindAIJob(ctx context.Context, project, ref string) (gen.AIJobSummary, error) {
	var js []gen.AIJobSummary
	if err := c.Do(ctx, "GET", "/projects/"+project+"/ai/jobs?limit=200", nil, &js); err != nil {
		return gen.AIJobSummary{}, err
	}
	return pick(js, "AI job", ref, func(j gen.AIJobSummary) string { return j.Id.String() }, func(gen.AIJobSummary) []string { return nil })
}

// FindDriftResult resolves a project's drift check by id or a unique id prefix.
func (c *Client) FindDriftResult(ctx context.Context, project, ref string) (gen.DriftResult, error) {
	var ds []gen.DriftResult
	if err := c.Do(ctx, "GET", "/projects/"+project+"/drift-results?limit=200", nil, &ds); err != nil {
		return gen.DriftResult{}, err
	}
	return pick(ds, "drift result", ref, func(d gen.DriftResult) string { return d.Id.String() }, func(gen.DriftResult) []string { return nil })
}

// FindIntegration resolves an integration by id or name.
func (c *Client) FindIntegration(ctx context.Context, ref string) (gen.Integration, error) {
	var is []gen.Integration
	if err := c.Do(ctx, "GET", "/integrations", nil, &is); err != nil {
		return gen.Integration{}, err
	}
	return pick(is, "integration", ref, func(i gen.Integration) string { return i.Id.String() }, func(i gen.Integration) []string { return []string{i.Name} })
}

// FindChannel resolves a notification channel by id or name.
func (c *Client) FindChannel(ctx context.Context, ref string) (gen.NotificationChannel, error) {
	var cs []gen.NotificationChannel
	if err := c.Do(ctx, "GET", "/notifications/channels", nil, &cs); err != nil {
		return gen.NotificationChannel{}, err
	}
	return pick(cs, "channel", ref, func(ch gen.NotificationChannel) string { return ch.Id.String() }, func(ch gen.NotificationChannel) []string { return []string{ch.Name} })
}

// Query encodes non-empty query parameters as "?a=b&c=d".
func Query(kv ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}
