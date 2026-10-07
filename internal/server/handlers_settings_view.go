package server

import (
	"context"
	"time"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// GetSSOSettings shows how single sign-on is configured. The client ID and
// secret are left out.
func (h *handlers) GetSSOSettings(ctx context.Context, _ gen.GetSSOSettingsRequestObject) (gen.GetSSOSettingsResponseObject, error) {
	if _, err := need(ctx, auth.PermManageUsers); err != nil {
		return nil, err
	}
	out := gen.SSOSettings{PasswordLogin: true, AllowedDomains: []string{}, Scopes: []string{}}
	if o := h.oidc; o != nil {
		c := o.cfg
		out.Enabled = true
		out.Name, out.Issuer, out.RedirectURL = ptr(c.Name), ptr(c.Issuer), ptr(c.RedirectURL)
		out.AllowedDomains = append(out.AllowedDomains, c.AllowedDomains...)
		if c.DefaultRole != "" {
			out.DefaultRole = ptr(string(c.DefaultRole))
		}
		// The scopes requested, as the sign-in flow builds them.
		out.Scopes = append(out.Scopes, "openid")
		if len(c.Scopes) == 0 {
			out.Scopes = append(out.Scopes, "email", "profile")
		}
		out.Scopes = append(out.Scopes, c.Scopes...)
	}
	return gen.GetSSOSettings200JSONResponse(out), nil
}

func limitCapsOf(c safety.Caps) gen.LimitCaps {
	var out gen.LimitCaps
	if c.MaxRate > 0 {
		out.MaxRate = ptr(c.MaxRate)
	}
	if c.MaxVUs > 0 {
		out.MaxVUs = ptr(c.MaxVUs)
	}
	if c.MaxDuration > 0 {
		out.MaxDurationSeconds = ptr(int(c.MaxDuration / time.Second))
	}
	return out
}

func targetCaps(t db.Target) safety.Caps {
	var c safety.Caps
	if t.MaxRate != nil {
		c.MaxRate = *t.MaxRate
	}
	if t.MaxVus != nil {
		c.MaxVUs = int(*t.MaxVus)
	}
	if t.MaxDurationS != nil {
		c.MaxDuration = time.Duration(*t.MaxDurationS) * time.Second
	}
	return c
}

// tightest combines caps, keeping the lowest set value of each; zero
// means no cap.
func tightest(cs ...safety.Caps) safety.Caps {
	var out safety.Caps
	lower := func(cur, v float64) float64 {
		if v > 0 && (cur == 0 || v < cur) {
			return v
		}
		return cur
	}
	for _, c := range cs {
		out.MaxRate = lower(out.MaxRate, c.MaxRate)
		out.MaxVUs = int(lower(float64(out.MaxVUs), float64(c.MaxVUs)))
		out.MaxDuration = time.Duration(lower(float64(out.MaxDuration), float64(c.MaxDuration)))
	}
	return out
}

// GetLimitSettings shows the caps every run is checked against, in the
// order checkCaps applies them: the server's, the organisation's, each
// project's, the unverified public caps, and each target's own caps,
// with each target's effective caps combining all that apply to it.
func (h *handlers) GetLimitSettings(ctx context.Context, _ gen.GetLimitSettingsRequestObject) (gen.GetLimitSettingsResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	out := gen.LimitSettings{
		Server:           limitCapsOf(h.cfg.HardCaps),
		UnverifiedPublic: limitCapsOf(safety.UnverifiedPublicCaps),
		Targets:          []gen.TargetLimits{},
	}
	if a := h.cfg.AbortFloor; a != nil {
		floor := &struct {
			ErrorRate  *float64 `json:"errorRate,omitempty"`
			ForSeconds float64  `json:"forSeconds"`
			P95Seconds *float64 `json:"p95Seconds,omitempty"`
		}{ForSeconds: a.For.D().Seconds()}
		if a.Errors != nil {
			floor.ErrorRate = ptr(float64(*a.Errors))
		}
		if a.P95 > 0 {
			floor.P95Seconds = ptr(a.P95.D().Seconds())
		}
		out.AbortFloor = floor
	}
	var orgCaps safety.Caps
	oc, err := h.st.GetOrgCaps(ctx, p.OrgID)
	switch {
	case err == nil:
		orgCaps = safetyCaps(oc.MaxRate, oc.MaxVus, oc.MaxDurationS)
	case !store.IsNotFound(err):
		return nil, err
	}
	out.Organisation = limitCapsOf(orgCaps)
	projects, err := h.st.ListProjects(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	out.Projects = make([]gen.ProjectLimits, 0, len(projects))
	for _, pr := range projects {
		ps, err := h.projectSettings(ctx, pr)
		if err != nil {
			return nil, err
		}
		projCaps := safetyCaps(ps.MaxRate, ps.MaxVus, ps.MaxDurationS)
		out.Projects = append(out.Projects, gen.ProjectLimits{
			Id: pr.ID, Name: pr.Name, Caps: limitCapsOf(projCaps), RequireDryRun: ps.RequireDryRun,
		})
		targets, err := h.st.ListTargets(ctx, pr.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range targets {
			own := targetCaps(t)
			verified := t.Private || t.VerifiedAt != nil
			eff := tightest(h.cfg.HardCaps, orgCaps, projCaps, own)
			if !verified {
				eff = tightest(eff, safety.UnverifiedPublicCaps)
			}
			out.Targets = append(out.Targets, gen.TargetLimits{
				Id: t.ID, Name: t.Name, ProjectId: pr.ID, ProjectName: pr.Name, BaseURL: t.BaseUrl,
				Private: t.Private, Verified: verified, Caps: limitCapsOf(own), Effective: limitCapsOf(eff),
			})
		}
	}
	return gen.GetLimitSettings200JSONResponse(out), nil
}
