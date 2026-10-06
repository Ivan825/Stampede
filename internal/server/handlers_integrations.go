package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/agent"
	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/keyring"
	"github.com/Ivan825/Stampede/internal/observe"
	"github.com/Ivan825/Stampede/internal/runner"
	"github.com/Ivan825/Stampede/internal/scenario"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// Integration kinds, as stored.
const (
	integrationPrometheus = "prometheus"
	integrationTraces     = "traces"
	integrationAgent      = "agent"
)

func integrationAAD(org, id uuid.UUID) []byte {
	return []byte("stampede-integration:" + org.String() + ":" + id.String())
}

func integrationOf(r db.Integration) gen.Integration {
	return gen.Integration{
		Id: r.ID, Name: r.Name, Kind: gen.IntegrationKind(r.Kind), Url: r.Url,
		HasToken: len(r.Ciphertext) > 0, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// ListIntegrations is open to every member: scenario authors need the
// names. Tokens are never returned.
func (h *handlers) ListIntegrations(ctx context.Context, _ gen.ListIntegrationsRequestObject) (gen.ListIntegrationsResponseObject, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListIntegrations(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	out := gen.ListIntegrations200JSONResponse{}
	for _, r := range rows {
		out = append(out, integrationOf(r))
	}
	return out, nil
}

func (h *handlers) CreateIntegration(ctx context.Context, req gen.CreateIntegrationRequestObject) (gen.CreateIntegrationResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	b := req.Body
	name := strings.TrimSpace(b.Name)
	if !scenario.IntegrationNameRe.MatchString(name) {
		return nil, errInvalid("name must start with a letter or digit and use letters, digits, '_', '.' or '-' (at most 100)")
	}
	raw := strings.TrimSpace(b.Url)
	token := ""
	if b.BearerToken != nil {
		token = strings.TrimSpace(*b.BearerToken)
	}
	switch string(b.Kind) {
	case integrationPrometheus:
		raw = strings.TrimRight(raw, "/")
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, errInvalid("url must be the Prometheus base URL, for example http://prometheus:9090")
		}
		if u.User != nil {
			return nil, errInvalid("put credentials in bearerToken, not in the URL")
		}
	case integrationTraces:
		if msg := scenario.CheckTraceURL(raw); msg != "" {
			return nil, errInvalid("url " + msg)
		}
		if token != "" {
			return nil, errInvalid("a traces integration only builds links; it takes no bearerToken")
		}
	case integrationAgent:
		raw = strings.TrimRight(raw, "/")
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return nil, errInvalid("url must be the agent's control API, for example http://agent.shop.svc:7070")
		}
		if token == "" {
			return nil, errInvalid("an agent integration needs bearerToken: the agent's --token")
		}
	default:
		return nil, errInvalid("kind must be prometheus, traces or agent")
	}

	id := uuid.New()
	var sealed keyring.Sealed
	if token != "" {
		kr, err := h.keyring()
		if err != nil {
			return nil, errConflict("integration tokens are stored encrypted: start the server with STAMPEDE_MASTER_KEY set")
		}
		if sealed, err = kr.Seal([]byte(token), integrationAAD(p.OrgID, id)); err != nil {
			return nil, err
		}
	}
	var keyID *string
	if sealed.KeyID != "" {
		k := sealed.KeyID
		keyID = &k
	}
	uid := p.UserID
	err = h.st.CreateIntegration(ctx, db.CreateIntegrationParams{
		ID: id, OrgID: p.OrgID, Name: name, Kind: string(b.Kind), Url: raw,
		Ciphertext: sealed.Ciphertext, WrappedKey: sealed.WrappedKey, KeyID: keyID, CreatedBy: &uid,
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict(fmt.Sprintf("an integration named %q already exists; delete it first to replace it", name))
		}
		return nil, err
	}
	h.audit(ctx, "integration.create", name, map[string]any{"kind": b.Kind, "url": raw, "token": token != ""})
	row, err := h.st.GetIntegration(ctx, db.GetIntegrationParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	return gen.CreateIntegration201JSONResponse(integrationOf(row)), nil
}

func (h *handlers) DeleteIntegration(ctx context.Context, req gen.DeleteIntegrationRequestObject) (gen.DeleteIntegrationResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	row, err := h.st.GetIntegration(ctx, db.GetIntegrationParams{ID: req.IntegrationId, OrgID: p.OrgID})
	if err != nil {
		return nil, notFoundOr(err, "integration")
	}
	if _, err := h.st.DeleteIntegration(ctx, db.DeleteIntegrationParams{ID: row.ID, OrgID: p.OrgID}); err != nil {
		return nil, err
	}
	h.audit(ctx, "integration.delete", row.Name, nil)
	return gen.DeleteIntegration204Response{}, nil
}

// observeFor resolves a server run's observe block against the
// organisation's integrations. A server never fetches a URL written in a
// scenario, so Prometheus must be named as an integration; a trace link
// template may be given directly because nothing is fetched from it.
func (h *handlers) observeFor(ctx context.Context, org uuid.UUID, o *scenario.Observe) (*observe.Config, error) {
	c := &observe.Config{}
	if o == nil {
		return c, nil
	}
	lookup := func(path, name, kind string) (db.Integration, error) {
		row, err := h.st.GetIntegrationByName(ctx, db.GetIntegrationByNameParams{OrgID: org, Name: name})
		if store.IsNotFound(err) {
			return row, errInvalid(fmt.Sprintf("%s: no integration named %q; an admin adds integrations under Settings → Integrations", path, name))
		}
		if err != nil {
			return row, err
		}
		if row.Kind != kind {
			return row, errInvalid(fmt.Sprintf("%s: integration %q is a %s integration, not %s", path, name, row.Kind, kind))
		}
		return row, nil
	}
	if p := o.Prometheus; p != nil {
		if p.URL != "" || p.BearerToken != "" {
			return nil, errInvalid("observe.prometheus: on the server, name an integration (observe.prometheus.integration) instead of a URL; the server only contacts URLs an admin configured")
		}
		row, err := lookup("observe.prometheus.integration", p.Integration, integrationPrometheus)
		if err != nil {
			return nil, err
		}
		prom := &observe.Prometheus{URL: row.Url}
		if len(row.Ciphertext) > 0 {
			kr, err := h.keyring()
			if err != nil {
				return nil, err
			}
			sealed := keyring.Sealed{Ciphertext: row.Ciphertext, WrappedKey: row.WrappedKey}
			if row.KeyID != nil {
				sealed.KeyID = *row.KeyID
			}
			tok, err := kr.Open(sealed, integrationAAD(org, row.ID))
			if err != nil {
				return nil, fmt.Errorf("decrypt integration %q token: %w", row.Name, err)
			}
			prom.BearerToken = string(tok)
		}
		c.Prometheus, c.Queries = prom, observe.QueriesOf(p)
	}
	if t := o.Traces; t != nil {
		c.TraceURL = t.URL
		if t.Integration != "" {
			row, err := lookup("observe.traces.integration", t.Integration, integrationTraces)
			if err != nil {
				return nil, err
			}
			c.TraceURL = row.Url
		}
	}
	return c, nil
}

// faultsFor resolves a server run's faults block. The agent must be an
// agent integration: the server never contacts a URL written in a
// scenario. The plan is checked against the agent before the run is
// created, so a missing proxy or permission fails the request, not the run.
func (h *handlers) faultsFor(ctx context.Context, org uuid.UUID, fs *scenario.Faults) (*runner.FaultPlan, error) {
	if fs == nil {
		return nil, nil
	}
	if fs.Agent.URL != "" || fs.Agent.Token != "" || fs.Agent.Integration == "" {
		return nil, errInvalid("faults.agent: on the server, name an agent integration (faults.agent.integration) instead of a URL and token; the server only contacts URLs an admin configured")
	}
	row, err := h.st.GetIntegrationByName(ctx, db.GetIntegrationByNameParams{OrgID: org, Name: fs.Agent.Integration})
	if store.IsNotFound(err) {
		return nil, errInvalid(fmt.Sprintf("faults.agent.integration: no integration named %q; an admin adds integrations under Settings → Integrations", fs.Agent.Integration))
	}
	if err != nil {
		return nil, err
	}
	if row.Kind != integrationAgent {
		return nil, errInvalid(fmt.Sprintf("faults.agent.integration: integration %q is a %s integration, not agent", row.Name, row.Kind))
	}
	kr, err := h.keyring()
	if err != nil {
		return nil, err
	}
	sealed := keyring.Sealed{Ciphertext: row.Ciphertext, WrappedKey: row.WrappedKey}
	if row.KeyID != nil {
		sealed.KeyID = *row.KeyID
	}
	tok, err := kr.Open(sealed, integrationAAD(org, row.ID))
	if err != nil {
		return nil, fmt.Errorf("decrypt integration %q token: %w", row.Name, err)
	}
	plan := &runner.FaultPlan{Client: &agent.Client{BaseURL: row.Url, Token: string(tok)}, Steps: fs.Timeline}
	if err := plan.Check(ctx); err != nil {
		return nil, errInvalid(err.Error())
	}
	return plan, nil
}
