package server

import (
	"context"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
)

// Organisation and project caps, the dry-run gate and per-project roles.

func (h *handlers) GetOrgCaps(ctx context.Context, _ gen.GetOrgCapsRequestObject) (gen.GetOrgCapsResponseObject, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, err
	}
	c, err := h.st.GetOrgCaps(ctx, p.OrgID)
	if store.IsNotFound(err) {
		return gen.GetOrgCaps200JSONResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetOrgCaps200JSONResponse(capsOf(c.MaxRate, c.MaxVus, c.MaxDurationS)), nil
}

func (h *handlers) PutOrgCaps(ctx context.Context, req gen.PutOrgCapsRequestObject) (gen.PutOrgCapsResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	rate, vus, dur, err := capsInput(req.Body)
	if err != nil {
		return nil, err
	}
	uid := p.UserID
	if err := h.st.PutOrgCaps(ctx, db.PutOrgCapsParams{OrgID: p.OrgID, MaxRate: rate, MaxVus: vus, MaxDurationS: dur, UpdatedBy: &uid}); err != nil {
		return nil, err
	}
	out := capsOf(rate, vus, dur)
	h.audit(ctx, "org.caps", p.OrgName, map[string]any{"caps": out})
	return gen.PutOrgCaps200JSONResponse(out), nil
}

// projectAdmin loads a project for changing its settings or roles: the
// caller must be an admin in the organisation, or have the admin role in
// this project through an override. Organisation admins keep this even
// when an override lowers them in the project, so nobody is locked out.
func (h *handlers) projectAdmin(ctx context.Context, id uuid.UUID) (*auth.Principal, db.Project, error) {
	p, err := need(ctx, auth.PermView)
	if err != nil {
		return nil, db.Project{}, err
	}
	if p.Can(auth.PermManageUsers) {
		pr, err := h.st.GetProject(ctx, db.GetProjectParams{ID: id, OrgID: p.OrgID})
		if err != nil {
			return nil, db.Project{}, notFoundOr(err, "project")
		}
		return p, pr, nil
	}
	return h.project(ctx, id, auth.PermManageUsers)
}

// projectSettings returns a project's settings, the defaults when none
// were saved.
func (s *Server) projectSettings(ctx context.Context, project db.Project) (db.ProjectSetting, error) {
	ps, err := s.st.GetProjectSettings(ctx, project.ID)
	if store.IsNotFound(err) {
		return db.ProjectSetting{ProjectID: project.ID}, nil
	}
	return ps, err
}

func projectSettingsOf(ps db.ProjectSetting) gen.ProjectSettings {
	return gen.ProjectSettings{Caps: capsOf(ps.MaxRate, ps.MaxVus, ps.MaxDurationS), RequireDryRun: ps.RequireDryRun}
}

func (h *handlers) GetProjectSettings(ctx context.Context, req gen.GetProjectSettingsRequestObject) (gen.GetProjectSettingsResponseObject, error) {
	_, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	ps, err := h.projectSettings(ctx, pr)
	if err != nil {
		return nil, err
	}
	return gen.GetProjectSettings200JSONResponse(projectSettingsOf(ps)), nil
}

func (h *handlers) PutProjectSettings(ctx context.Context, req gen.PutProjectSettingsRequestObject) (gen.PutProjectSettingsResponseObject, error) {
	p, pr, err := h.projectAdmin(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	rate, vus, dur, err := capsInput(&req.Body.Caps)
	if err != nil {
		return nil, err
	}
	uid := p.UserID
	err = h.st.PutProjectSettings(ctx, db.PutProjectSettingsParams{
		ProjectID: pr.ID, MaxRate: rate, MaxVus: vus, MaxDurationS: dur, RequireDryRun: req.Body.RequireDryRun, UpdatedBy: &uid,
	})
	if err != nil {
		return nil, err
	}
	ps, err := h.projectSettings(ctx, pr)
	if err != nil {
		return nil, err
	}
	out := projectSettingsOf(ps)
	h.audit(ctx, "project.settings", pr.Name, map[string]any{"caps": out.Caps, "requireDryRun": out.RequireDryRun})
	return gen.PutProjectSettings200JSONResponse(out), nil
}

func projectRoleOf(r db.ListProjectRolesRow) gen.ProjectRole {
	out := gen.ProjectRole{UserId: r.UserID, Email: r.Email, Role: gen.Role(r.Role), CreatedAt: &r.CreatedAt}
	if r.Name != "" {
		n := r.Name
		out.Name = &n
	}
	if r.OrgRole != nil {
		o := gen.Role(*r.OrgRole)
		out.OrgRole = &o
	}
	return out
}

func (h *handlers) ListProjectRoles(ctx context.Context, req gen.ListProjectRolesRequestObject) (gen.ListProjectRolesResponseObject, error) {
	_, pr, err := h.project(ctx, req.ProjectId, auth.PermView)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListProjectRoles(ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	out := gen.ListProjectRoles200JSONResponse{}
	for _, r := range rows {
		if r.OrgRole == nil {
			continue // no longer a member; the override has no effect
		}
		out = append(out, projectRoleOf(r))
	}
	return out, nil
}

func (h *handlers) PutProjectRole(ctx context.Context, req gen.PutProjectRoleRequestObject) (gen.PutProjectRoleResponseObject, error) {
	p, pr, err := h.projectAdmin(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	role := auth.Role(req.Body.Role)
	switch {
	case !role.Valid():
		return nil, errInvalid("unknown role " + string(role))
	case role == auth.RoleOwner:
		return nil, errInvalid("owner is an organisation role; it cannot be given for one project")
	case !p.Can(role):
		return nil, errForbidden("you cannot give a role higher than your own (" + string(p.Role) + ") in this project")
	}
	m, err := h.st.GetMember(ctx, db.GetMemberParams{OrgID: p.OrgID, ID: req.UserId})
	if err != nil {
		return nil, notFoundOr(err, "member")
	}
	if m.Role == string(auth.RoleOwner) {
		return nil, errInvalid(m.Email + " is an owner, and owners have the owner role in every project")
	}
	uid := p.UserID
	if err := h.st.SetProjectRole(ctx, db.SetProjectRoleParams{ProjectID: pr.ID, UserID: m.ID, Role: string(role), CreatedBy: &uid}); err != nil {
		return nil, err
	}
	h.audit(ctx, "project.role.set", pr.Name+"/"+m.Email, map[string]any{"role": role, "orgRole": m.Role})
	rows, err := h.st.ListProjectRoles(ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.UserID == m.ID {
			return gen.PutProjectRole200JSONResponse(projectRoleOf(r)), nil
		}
	}
	return nil, errNotFound("project role")
}

func (h *handlers) DeleteProjectRole(ctx context.Context, req gen.DeleteProjectRoleRequestObject) (gen.DeleteProjectRoleResponseObject, error) {
	_, pr, err := h.projectAdmin(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	n, err := h.st.DeleteProjectRole(ctx, db.DeleteProjectRoleParams{ProjectID: pr.ID, UserID: req.UserId})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errNotFound("project role")
	}
	h.audit(ctx, "project.role.delete", pr.Name, map[string]any{"user": req.UserId})
	return gen.DeleteProjectRole204Response{}, nil
}
