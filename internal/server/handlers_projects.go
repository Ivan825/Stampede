package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/keyring"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		s = "project"
	}
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	return s
}

func projectOf(p db.Project, role auth.Role) gen.Project {
	d := p.Description
	out := gen.Project{Id: p.ID, Name: p.Name, Slug: p.Slug, Description: &d, CreatedAt: p.CreatedAt}
	if role != "" {
		r := gen.Role(role)
		out.Role = &r
	}
	return out
}

// project loads a project in the caller's organisation and checks the
// caller's role in it (see needIn).
func (h *handlers) project(ctx context.Context, id uuid.UUID, min auth.Role) (*auth.Principal, db.Project, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, db.Project{}, err
	}
	pr, err := h.st.GetProject(ctx, db.GetProjectParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, db.Project{}, notFoundOr(err, "project")
	}
	pp, err := h.needIn(ctx, pr.ID, min)
	if err != nil {
		return nil, db.Project{}, err
	}
	return pp, pr, nil
}

func (h *handlers) ListProjects(ctx context.Context, _ gen.ListProjectsRequestObject) (gen.ListProjectsResponseObject, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListProjects(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	ovs, err := h.st.ListUserProjectRoles(ctx, db.ListUserProjectRolesParams{OrgID: p.OrgID, UserID: p.UserID})
	if err != nil {
		return nil, err
	}
	overrides := map[uuid.UUID]auth.Role{}
	for _, o := range ovs {
		overrides[o.ProjectID] = auth.Role(o.Role)
	}
	out := gen.ListProjects200JSONResponse{}
	for _, r := range rows {
		out = append(out, projectOf(r, p.InProject(overrides[r.ID]).Role))
	}
	return out, nil
}

func (h *handlers) CreateProject(ctx context.Context, req gen.CreateProjectRequestObject) (gen.CreateProjectResponseObject, error) {
	p, err := need(ctx, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Body.Name)
	if name == "" {
		return nil, errInvalid("name is required")
	}
	desc := ""
	if req.Body.Description != nil {
		desc = *req.Body.Description
	}
	id := uuid.New()
	if err := h.st.CreateProject(ctx, db.CreateProjectParams{ID: id, OrgID: p.OrgID, Name: name, Slug: slugify(name), Description: desc}); err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict("a project with a similar name already exists")
		}
		return nil, err
	}
	h.audit(ctx, "project.create", name, nil)
	pr, err := h.st.GetProject(ctx, db.GetProjectParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	return gen.CreateProject201JSONResponse(projectOf(pr, p.Role)), nil
}

func (h *handlers) GetProject(ctx context.Context, req gen.GetProjectRequestObject) (gen.GetProjectResponseObject, error) {
	p, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	return gen.GetProject200JSONResponse(projectOf(pr, p.Role)), nil
}

func (h *handlers) UpdateProject(ctx context.Context, req gen.UpdateProjectRequestObject) (gen.UpdateProjectResponseObject, error) {
	p, pr, err := h.project(ctx, req.ProjectId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Body.Name)
	if name == "" {
		return nil, errInvalid("name is required")
	}
	desc := pr.Description
	if req.Body.Description != nil {
		desc = *req.Body.Description
	}
	if err := h.st.UpdateProject(ctx, db.UpdateProjectParams{ID: pr.ID, OrgID: p.OrgID, Name: name, Slug: slugify(name), Description: desc}); err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict("a project with a similar name already exists")
		}
		return nil, err
	}
	pr, err = h.st.GetProject(ctx, db.GetProjectParams{ID: pr.ID, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	return gen.UpdateProject200JSONResponse(projectOf(pr, p.Role)), nil
}

func (h *handlers) DeleteProject(ctx context.Context, req gen.DeleteProjectRequestObject) (gen.DeleteProjectResponseObject, error) {
	// Deleting a project needs an organisation admin; a project-level
	// override does not grant it.
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	pr, err := h.st.GetProject(ctx, db.GetProjectParams{ID: req.ProjectId, OrgID: p.OrgID})
	if err != nil {
		return nil, notFoundOr(err, "project")
	}
	if h.runs.projectHasActiveRun(pr.ID) {
		return nil, errConflict("stop the project's active runs first")
	}
	if _, err := h.st.DeleteProject(ctx, db.DeleteProjectParams{ID: pr.ID, OrgID: p.OrgID}); err != nil {
		return nil, err
	}
	h.audit(ctx, "project.delete", pr.Name, nil)
	return gen.DeleteProject204Response{}, nil
}

// Targets.

func targetOf(t db.Target) gen.Target {
	caps := capsOf(t.MaxRate, t.MaxVus, t.MaxDurationS)
	allow := t.AllowHosts
	return gen.Target{
		Id: t.ID, ProjectId: t.ProjectID, Name: t.Name, BaseURL: t.BaseUrl, Private: t.Private,
		Verified: t.Private || t.VerifiedAt != nil, VerifiedAt: t.VerifiedAt, VerificationMethod: t.VerificationMethod,
		VerificationToken: t.VerificationToken, AllowHosts: &allow, Caps: caps, CreatedAt: t.CreatedAt,
	}
}

type targetInput struct {
	host       string
	private    bool
	allow      []string
	maxRate    *float64
	maxVUs     *int32
	maxDurSecs *int32
}

func (h *handlers) checkTargetInput(ctx context.Context, in gen.TargetCreate) (targetInput, error) {
	var out targetInput
	if strings.TrimSpace(in.Name) == "" {
		return out, errInvalid("name is required")
	}
	u, err := url.Parse(strings.TrimSpace(in.BaseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return out, errInvalid("baseURL must be an absolute http(s) URL such as https://staging.example.com")
	}
	out.host = strings.ToLower(u.Hostname())
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	priv, err := safety.IsPrivateHost(lctx, out.host)
	if err != nil {
		return out, errInvalid(fmt.Sprintf("cannot resolve %s: %v", out.host, err))
	}
	out.private = priv
	if in.AllowHosts != nil {
		for _, a := range *in.AllowHosts {
			if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
				out.allow = append(out.allow, a)
			}
		}
	}
	if out.allow == nil {
		out.allow = []string{}
	}
	out.maxRate, out.maxVUs, out.maxDurSecs, err = capsInput(in.Caps)
	return out, err
}

// capsInput checks caps from a request and converts them for storage.
func capsInput(c *gen.Caps) (maxRate *float64, maxVUs, maxDurSecs *int32, err error) {
	if c == nil {
		return nil, nil, nil, nil
	}
	if c.MaxRate != nil && *c.MaxRate <= 0 || c.MaxVUs != nil && *c.MaxVUs <= 0 || c.MaxDurationSeconds != nil && *c.MaxDurationSeconds <= 0 {
		return nil, nil, nil, errInvalid("caps must be positive")
	}
	maxRate = c.MaxRate
	if c.MaxVUs != nil {
		v := int32(min(*c.MaxVUs, 10_000_000)) //nolint:gosec // bounded
		maxVUs = &v
	}
	if c.MaxDurationSeconds != nil {
		v := int32(min(*c.MaxDurationSeconds, 30*24*3600)) //nolint:gosec // bounded
		maxDurSecs = &v
	}
	return maxRate, maxVUs, maxDurSecs, nil
}

// capsOf converts stored caps for the API.
func capsOf(maxRate *float64, maxVUs, maxDurSecs *int32) gen.Caps {
	c := gen.Caps{MaxRate: maxRate}
	if maxVUs != nil {
		v := int(*maxVUs)
		c.MaxVUs = &v
	}
	if maxDurSecs != nil {
		v := int(*maxDurSecs)
		c.MaxDurationSeconds = &v
	}
	return c
}

// safetyCaps converts stored caps for safety.CheckPlan.
func safetyCaps(maxRate *float64, maxVUs, maxDurSecs *int32) safety.Caps {
	c := safety.Caps{}
	if maxRate != nil {
		c.MaxRate = *maxRate
	}
	if maxVUs != nil {
		c.MaxVUs = int(*maxVUs)
	}
	if maxDurSecs != nil {
		c.MaxDuration = time.Duration(*maxDurSecs) * time.Second
	}
	return c
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (h *handlers) ListTargets(ctx context.Context, req gen.ListTargetsRequestObject) (gen.ListTargetsResponseObject, error) {
	_, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListTargets(ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	out := gen.ListTargets200JSONResponse{}
	for _, t := range rows {
		out = append(out, targetOf(t))
	}
	return out, nil
}

func (h *handlers) CreateTarget(ctx context.Context, req gen.CreateTargetRequestObject) (gen.CreateTargetResponseObject, error) {
	p, pr, err := h.project(ctx, req.ProjectId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	in, err := h.checkTargetInput(ctx, *req.Body)
	if err != nil {
		return nil, err
	}
	id := uuid.New()
	err = h.st.CreateTarget(ctx, db.CreateTargetParams{
		ID: id, ProjectID: pr.ID, Name: strings.TrimSpace(req.Body.Name), BaseUrl: strings.TrimRight(strings.TrimSpace(req.Body.BaseURL), "/"),
		Host: in.host, Private: in.private, VerificationToken: randomHex(16), AllowHosts: in.allow,
		MaxRate: in.maxRate, MaxVus: in.maxVUs, MaxDurationS: in.maxDurSecs,
	})
	if err != nil {
		return nil, err
	}
	h.audit(ctx, "target.create", req.Body.BaseURL, map[string]any{"private": in.private})
	t, err := h.st.GetTarget(ctx, db.GetTargetParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	return gen.CreateTarget201JSONResponse(targetOf(t)), nil
}

func (h *handlers) target(ctx context.Context, id uuid.UUID, min auth.Role) (*auth.Principal, db.Target, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, db.Target{}, err
	}
	t, err := h.st.GetTarget(ctx, db.GetTargetParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		return nil, db.Target{}, notFoundOr(err, "target")
	}
	pp, err := h.needIn(ctx, t.ProjectID, min)
	if err != nil {
		return nil, db.Target{}, err
	}
	return pp, t, nil
}

func (h *handlers) GetTarget(ctx context.Context, req gen.GetTargetRequestObject) (gen.GetTargetResponseObject, error) {
	_, t, err := h.target(ctx, req.TargetId, auth.PermView)
	if err != nil {
		return nil, err
	}
	return gen.GetTarget200JSONResponse(targetOf(t)), nil
}

func (h *handlers) UpdateTarget(ctx context.Context, req gen.UpdateTargetRequestObject) (gen.UpdateTargetResponseObject, error) {
	p, t, err := h.target(ctx, req.TargetId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	in, err := h.checkTargetInput(ctx, *req.Body)
	if err != nil {
		return nil, err
	}
	// Changing the host clears verification (handled in the query).
	err = h.st.UpdateTarget(ctx, db.UpdateTargetParams{
		ID: t.ID, Name: strings.TrimSpace(req.Body.Name), BaseUrl: strings.TrimRight(strings.TrimSpace(req.Body.BaseURL), "/"),
		Host: in.host, Private: in.private, AllowHosts: in.allow, MaxRate: in.maxRate, MaxVus: in.maxVUs, MaxDurationS: in.maxDurSecs,
	})
	if err != nil {
		return nil, err
	}
	h.audit(ctx, "target.update", req.Body.BaseURL, nil)
	t, err = h.st.GetTarget(ctx, db.GetTargetParams{ID: t.ID, OrgID: p.OrgID})
	if err != nil {
		return nil, err
	}
	return gen.UpdateTarget200JSONResponse(targetOf(t)), nil
}

func (h *handlers) DeleteTarget(ctx context.Context, req gen.DeleteTargetRequestObject) (gen.DeleteTargetResponseObject, error) {
	p, t, err := h.target(ctx, req.TargetId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	if h.runs.targetHasActiveRun(t.ID) {
		return nil, errConflict("a run against this target is active")
	}
	if _, err := h.st.DeleteTarget(ctx, db.DeleteTargetParams{ID: t.ID, OrgID: p.OrgID}); err != nil {
		return nil, err
	}
	h.audit(ctx, "target.delete", t.BaseUrl, nil)
	return gen.DeleteTarget204Response{}, nil
}

func (h *handlers) VerifyTarget(ctx context.Context, req gen.VerifyTargetRequestObject) (gen.VerifyTargetResponseObject, error) {
	p, t, err := h.target(ctx, req.TargetId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	if !t.Private && t.VerifiedAt == nil {
		u, _ := url.Parse(t.BaseUrl)
		vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		method, verr := safety.Verify(vctx, u, t.VerificationToken)
		cancel()
		if verr == nil {
			if err := h.st.SetTargetVerified(ctx, db.SetTargetVerifiedParams{ID: t.ID, VerificationMethod: &method}); err != nil {
				return nil, err
			}
			h.audit(ctx, "target.verify", t.BaseUrl, map[string]any{"method": method})
		}
		t, err = h.st.GetTarget(ctx, db.GetTargetParams{ID: t.ID, OrgID: p.OrgID})
		if err != nil {
			return nil, err
		}
	}
	return gen.VerifyTarget200JSONResponse(targetOf(t)), nil
}

// Secrets.

var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func secretAAD(project uuid.UUID, name string) []byte {
	return []byte("stampede-secret:" + project.String() + ":" + name)
}

func (h *handlers) keyring() (*keyring.Keyring, error) {
	if h.cfg.Keyring == nil {
		return nil, errConflict("secrets are disabled: start the server with STAMPEDE_MASTER_KEY set")
	}
	return h.cfg.Keyring, nil
}

func (h *handlers) ListSecrets(ctx context.Context, req gen.ListSecretsRequestObject) (gen.ListSecretsResponseObject, error) {
	_, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListSecretNames(ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	out := gen.ListSecrets200JSONResponse{}
	for _, r := range rows {
		out = append(out, gen.Secret{Name: r.Name, UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}

func (h *handlers) PutSecret(ctx context.Context, req gen.PutSecretRequestObject) (gen.PutSecretResponseObject, error) {
	_, pr, err := h.project(ctx, req.ProjectId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	kr, err := h.keyring()
	if err != nil {
		return nil, err
	}
	if !secretNameRe.MatchString(req.Body.Name) {
		return nil, errInvalid("secret names must be identifiers (letters, digits, underscore)")
	}
	sealed, err := kr.Seal([]byte(req.Body.Value), secretAAD(pr.ID, req.Body.Name))
	if err != nil {
		return nil, err
	}
	at, err := h.st.PutSecret(ctx, db.PutSecretParams{ProjectID: pr.ID, Name: req.Body.Name, Ciphertext: sealed.Ciphertext, WrappedKey: sealed.WrappedKey, KeyID: sealed.KeyID})
	if err != nil {
		return nil, err
	}
	h.audit(ctx, "secret.put", pr.Name+"/"+req.Body.Name, nil)
	return gen.PutSecret200JSONResponse{Name: req.Body.Name, UpdatedAt: at}, nil
}

func (h *handlers) DeleteSecret(ctx context.Context, req gen.DeleteSecretRequestObject) (gen.DeleteSecretResponseObject, error) {
	_, pr, err := h.project(ctx, req.ProjectId, auth.PermEditScenarios)
	if err != nil {
		return nil, err
	}
	n, err := h.st.DeleteSecret(ctx, db.DeleteSecretParams{ProjectID: pr.ID, Name: req.Name})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errNotFound("secret")
	}
	h.audit(ctx, "secret.delete", pr.Name+"/"+req.Name, nil)
	return gen.DeleteSecret204Response{}, nil
}

// projectSecrets decrypts every secret of a project for a run.
func (s *Server) projectSecrets(ctx context.Context, project uuid.UUID) (map[string]string, error) {
	rows, err := s.st.ListSecrets(ctx, project)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if len(rows) == 0 {
		return out, nil
	}
	if s.cfg.Keyring == nil {
		return nil, errConflict("the project has secrets but the server has no master key")
	}
	for _, r := range rows {
		pt, err := s.cfg.Keyring.Open(keyring.Sealed{Ciphertext: r.Ciphertext, WrappedKey: r.WrappedKey, KeyID: r.KeyID}, secretAAD(project, r.Name))
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", r.Name, err)
		}
		out[r.Name] = string(pt)
	}
	return out, nil
}
