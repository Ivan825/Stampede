package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/auth"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
	"github.com/Ivan825/Stampede/internal/version"
)

// lockSetup serialises first-run setup.
const lockSetup = 7461

// audit records an action. Failures are logged, never returned: the audit
// log must not make an otherwise successful action fail.
func (s *Server) audit(ctx context.Context, action, subject string, details map[string]any) {
	p := auth.FromContext(ctx)
	if p == nil {
		return
	}
	s.auditAs(ctx, p, action, subject, details)
}

func (s *Server) auditAs(ctx context.Context, p *auth.Principal, action, subject string, details map[string]any) {
	b, _ := json.Marshal(details)
	if details == nil {
		b = []byte("{}")
	}
	uid := p.UserID
	err := s.st.InsertAudit(context.WithoutCancel(ctx), db.InsertAuditParams{
		OrgID: p.OrgID, Actor: p.Actor(), ActorID: &uid, Action: action, Subject: subject, Details: b, Ip: clientIP(ctx),
	})
	if err != nil {
		s.log.Error("audit write failed", "action", action, "error", err)
	}
}

func (h *handlers) GetVersion(ctx context.Context, _ gen.GetVersionRequestObject) (gen.GetVersionResponseObject, error) {
	n, err := h.st.CountUsers(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetVersion200JSONResponse{Version: version.Version, Commit: version.Commit, SetupRequired: n == 0}, nil
}

func (h *handlers) Setup(ctx context.Context, req gen.SetupRequestObject) (gen.SetupResponseObject, error) {
	b := req.Body
	if strings.TrimSpace(b.Organisation) == "" || strings.TrimSpace(b.Name) == "" || !strings.Contains(string(b.Email), "@") {
		return nil, errInvalid("organisation, name and a valid email are required")
	}
	hash, err := auth.HashPassword(b.Password)
	if err != nil {
		return nil, errInvalid(err.Error())
	}
	org, user := uuid.New(), uuid.New()
	// The lock serialises concurrent setup attempts so only one creates the owner.
	err = h.st.InTxLocked(ctx, lockSetup, func(q *db.Queries) error {
		n, err := q.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return errConflict("setup has already been completed; sign in instead")
		}
		if err := q.CreateOrg(ctx, db.CreateOrgParams{ID: org, Name: strings.TrimSpace(b.Organisation)}); err != nil {
			return err
		}
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: user, Email: strings.TrimSpace(string(b.Email)), Name: strings.TrimSpace(b.Name), PasswordHash: hash}); err != nil {
			return err
		}
		return q.CreateMembership(ctx, db.CreateMembershipParams{OrgID: org, UserID: user, Role: string(auth.RoleOwner)})
	})
	if err != nil {
		return nil, err
	}
	p := &auth.Principal{UserID: user, OrgID: org, Email: string(b.Email), Name: b.Name, OrgName: b.Organisation, Role: auth.RoleOwner, Via: "session"}
	sess, err := h.startSession(ctx, p)
	if err != nil {
		return nil, err
	}
	h.auditAs(ctx, p, "setup", b.Organisation, nil)
	return gen.Setup201JSONResponse(sess), nil
}

func (h *handlers) startSession(ctx context.Context, p *auth.Principal) (gen.Session, error) {
	w, r := httpFrom(ctx)
	token, hash := auth.NewToken(auth.SessionPrefix)
	exp := h.cfg.Now().Add(h.cfg.SessionTTL)
	ua := ""
	if r != nil {
		ua = r.UserAgent()
	}
	if err := h.st.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: hash, UserID: p.UserID, OrgID: p.OrgID, ExpiresAt: exp, Ip: clientIP(ctx), UserAgent: ua,
	}); err != nil {
		return gen.Session{}, err
	}
	if w != nil {
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is configurable for plain-HTTP local installs
			Name: SessionCookie, Value: token, Path: "/", Expires: exp,
			HttpOnly: true, Secure: h.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
		})
	}
	_ = h.st.TouchUserLogin(ctx, p.UserID)
	return gen.Session{ExpiresAt: exp, User: meOf(p)}, nil
}

func meOf(p *auth.Principal) gen.Me {
	return gen.Me{Id: p.UserID, Email: p.Email, Name: p.Name, OrgId: p.OrgID, OrgName: p.OrgName, Role: gen.Role(p.Role)}
}

func (h *handlers) Login(ctx context.Context, req gen.LoginRequestObject) (gen.LoginResponseObject, error) {
	email := strings.ToLower(strings.TrimSpace(string(req.Body.Email)))
	ipKey, emailKey := "ip:"+clientIP(ctx), "email:"+email
	if !h.ipLimiter.Allowed(ipKey) || !h.limiter.Allowed(emailKey) {
		return nil, errTooMany("too many failed sign-in attempts; try again in 15 minutes")
	}
	fail := func() (gen.LoginResponseObject, error) {
		h.ipLimiter.Fail(ipKey)
		h.limiter.Fail(emailKey)
		return nil, errUnauthorized("email or password is incorrect")
	}
	u, err := h.st.GetUserByEmail(ctx, email)
	if err != nil {
		auth.CheckDummy(req.Body.Password)
		return fail()
	}
	if !auth.CheckPassword(u.PasswordHash, req.Body.Password) {
		return fail()
	}
	m, err := h.st.GetMembershipForUser(ctx, u.ID)
	if err != nil {
		return fail()
	}
	h.limiter.Reset(emailKey)
	p := &auth.Principal{UserID: u.ID, OrgID: m.OrgID, Email: u.Email, Name: u.Name, OrgName: m.OrgName, Role: auth.Role(m.Role), Via: "session"}
	sess, err := h.startSession(ctx, p)
	if err != nil {
		return nil, err
	}
	h.auditAs(ctx, p, "auth.login", u.Email, nil)
	return gen.Login200JSONResponse(sess), nil
}

func (h *handlers) Logout(ctx context.Context, _ gen.LogoutRequestObject) (gen.LogoutResponseObject, error) {
	p := auth.FromContext(ctx)
	if p != nil && p.SessionHash != nil {
		if err := h.st.DeleteSession(ctx, p.SessionHash); err != nil {
			return nil, err
		}
	}
	if w, _ := httpFrom(ctx); w != nil {
		http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", //nolint:gosec // see startSession
			Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
	}
	return gen.Logout204Response{}, nil
}

func (h *handlers) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	p, err := need(ctx, auth.RoleViewer)
	if err != nil {
		return nil, err
	}
	return gen.GetMe200JSONResponse(meOf(p)), nil
}

func (h *handlers) ChangePassword(ctx context.Context, req gen.ChangePasswordRequestObject) (gen.ChangePasswordResponseObject, error) {
	p, err := need(ctx, auth.RoleViewer)
	if err != nil {
		return nil, err
	}
	u, err := h.st.GetUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if !auth.CheckPassword(u.PasswordHash, req.Body.Current) {
		return nil, errUnauthorized("current password is incorrect")
	}
	hash, err := auth.HashPassword(req.Body.New)
	if err != nil {
		return nil, errInvalid(err.Error())
	}
	if err := h.st.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{ID: p.UserID, PasswordHash: hash}); err != nil {
		return nil, err
	}
	// Sign out every other session; keep the current one.
	if err := h.st.DeleteUserSessions(ctx, p.UserID); err != nil {
		return nil, err
	}
	if p.SessionHash != nil {
		if _, err := h.startSession(ctx, p); err != nil {
			return nil, err
		}
	}
	h.audit(ctx, "auth.password_change", p.Email, nil)
	return gen.ChangePassword204Response{}, nil
}

func userOf(id uuid.UUID, email, name string, created time.Time, lastLogin *time.Time, role string) gen.User {
	return gen.User{Id: id, Email: email, Name: name, CreatedAt: created, LastLoginAt: lastLogin, Role: gen.Role(role)}
}

func (h *handlers) ListUsers(ctx context.Context, _ gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	p, err := need(ctx, auth.RoleViewer)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListMembers(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}
	out := gen.ListUsers200JSONResponse{}
	for _, r := range rows {
		out = append(out, userOf(r.ID, r.Email, r.Name, r.CreatedAt, r.LastLoginAt, r.Role))
	}
	return out, nil
}

func (h *handlers) CreateUser(ctx context.Context, req gen.CreateUserRequestObject) (gen.CreateUserResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	b := req.Body
	role := auth.Role(b.Role)
	if !role.Valid() {
		return nil, errInvalid("unknown role")
	}
	if role == auth.RoleOwner && !p.Can(auth.PermManageOwners) {
		return nil, errForbidden("only owners can create owners")
	}
	hash, err := auth.HashPassword(b.Password)
	if err != nil {
		return nil, errInvalid(err.Error())
	}
	id := uuid.New()
	err = h.st.InTx(ctx, func(q *db.Queries) error {
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: id, Email: strings.TrimSpace(string(b.Email)), Name: strings.TrimSpace(b.Name), PasswordHash: hash}); err != nil {
			return err
		}
		return q.CreateMembership(ctx, db.CreateMembershipParams{OrgID: p.OrgID, UserID: id, Role: string(role)})
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errConflict("a user with that email already exists")
		}
		return nil, err
	}
	h.audit(ctx, "user.create", string(b.Email), map[string]any{"role": role})
	m, err := h.st.GetMember(ctx, db.GetMemberParams{OrgID: p.OrgID, ID: id})
	if err != nil {
		return nil, err
	}
	return gen.CreateUser201JSONResponse(userOf(m.ID, m.Email, m.Name, m.CreatedAt, m.LastLoginAt, m.Role)), nil
}

func (h *handlers) UpdateUser(ctx context.Context, req gen.UpdateUserRequestObject) (gen.UpdateUserResponseObject, error) {
	p, err := need(ctx, auth.RoleViewer)
	if err != nil {
		return nil, err
	}
	target, err := h.st.GetMember(ctx, db.GetMemberParams{OrgID: p.OrgID, ID: req.UserId})
	if err != nil {
		return nil, notFoundOr(err, "user")
	}
	self := req.UserId == p.UserID
	if !self && !p.Can(auth.PermManageUsers) {
		return nil, errForbidden("only admins can change other users")
	}
	if req.Body.Name != nil {
		if err := h.st.UpdateUserName(ctx, db.UpdateUserNameParams{ID: req.UserId, Name: strings.TrimSpace(*req.Body.Name)}); err != nil {
			return nil, err
		}
	}
	if req.Body.Role != nil && string(*req.Body.Role) != target.Role {
		newRole := auth.Role(*req.Body.Role)
		if !newRole.Valid() {
			return nil, errInvalid("unknown role")
		}
		if !p.Can(auth.PermManageUsers) {
			return nil, errForbidden("only admins can change roles")
		}
		if (newRole == auth.RoleOwner || target.Role == string(auth.RoleOwner)) && !p.Can(auth.PermManageOwners) {
			return nil, errForbidden("only owners can grant or remove the owner role")
		}
		if target.Role == string(auth.RoleOwner) {
			if err := h.keepAnOwner(ctx, p.OrgID); err != nil {
				return nil, err
			}
		}
		if err := h.st.UpdateMembershipRole(ctx, db.UpdateMembershipRoleParams{OrgID: p.OrgID, UserID: req.UserId, Role: string(newRole)}); err != nil {
			return nil, err
		}
		h.audit(ctx, "user.role", target.Email, map[string]any{"from": target.Role, "to": newRole})
	}
	m, err := h.st.GetMember(ctx, db.GetMemberParams{OrgID: p.OrgID, ID: req.UserId})
	if err != nil {
		return nil, err
	}
	return gen.UpdateUser200JSONResponse(userOf(m.ID, m.Email, m.Name, m.CreatedAt, m.LastLoginAt, m.Role)), nil
}

func (h *handlers) keepAnOwner(ctx context.Context, org uuid.UUID) error {
	n, err := h.st.CountOwners(ctx, org)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errConflict("the organisation must keep at least one owner")
	}
	return nil
}

func (h *handlers) DeleteUser(ctx context.Context, req gen.DeleteUserRequestObject) (gen.DeleteUserResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	target, err := h.st.GetMember(ctx, db.GetMemberParams{OrgID: p.OrgID, ID: req.UserId})
	if err != nil {
		return nil, notFoundOr(err, "user")
	}
	if target.Role == string(auth.RoleOwner) {
		if !p.Can(auth.PermManageOwners) {
			return nil, errForbidden("only owners can remove owners")
		}
		if err := h.keepAnOwner(ctx, p.OrgID); err != nil {
			return nil, err
		}
	}
	if err := h.st.DeleteUser(ctx, req.UserId); err != nil {
		return nil, err
	}
	h.audit(ctx, "user.delete", target.Email, nil)
	return gen.DeleteUser204Response{}, nil
}

func tokenOf(t db.ApiToken) gen.Token {
	return gen.Token{Id: t.ID, Name: t.Name, Prefix: t.Prefix, Role: gen.Role(t.Role), CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, ExpiresAt: t.ExpiresAt}
}

func (h *handlers) ListTokens(ctx context.Context, _ gen.ListTokensRequestObject) (gen.ListTokensResponseObject, error) {
	p, err := need(ctx, auth.RoleViewer)
	if err != nil {
		return nil, err
	}
	rows, err := h.st.ListTokens(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := gen.ListTokens200JSONResponse{}
	for _, t := range rows {
		out = append(out, tokenOf(t))
	}
	return out, nil
}

func (h *handlers) CreateToken(ctx context.Context, req gen.CreateTokenRequestObject) (gen.CreateTokenResponseObject, error) {
	p, err := need(ctx, auth.RoleViewer)
	if err != nil {
		return nil, err
	}
	if p.Via != "session" {
		return nil, errForbidden("API tokens cannot create other tokens; sign in to the UI")
	}
	b := req.Body
	role := p.Role
	if b.Role != nil {
		role = auth.Role(*b.Role)
		if !role.Valid() {
			return nil, errInvalid("unknown role")
		}
		if !p.Role.AtLeast(role) {
			return nil, errForbidden("a token cannot have more access than you")
		}
	}
	var exp *time.Time
	if b.ExpiresInDays != nil {
		t := h.cfg.Now().Add(time.Duration(*b.ExpiresInDays) * 24 * time.Hour)
		exp = &t
	}
	token, hash := auth.NewToken(auth.APITokenPrefix)
	id := uuid.New()
	err = h.st.CreateToken(ctx, db.CreateTokenParams{
		ID: id, OrgID: p.OrgID, UserID: p.UserID, Name: strings.TrimSpace(b.Name),
		Prefix: token[:12], TokenHash: hash, Role: string(role), ExpiresAt: exp,
	})
	if err != nil {
		return nil, err
	}
	h.audit(ctx, "token.create", b.Name, map[string]any{"role": role})
	now := h.cfg.Now()
	return gen.CreateToken201JSONResponse{
		Id: id, Name: b.Name, Prefix: token[:12], Role: gen.Role(role), CreatedAt: now, ExpiresAt: exp, Secret: token,
	}, nil
}

func (h *handlers) DeleteToken(ctx context.Context, req gen.DeleteTokenRequestObject) (gen.DeleteTokenResponseObject, error) {
	p, err := need(ctx, auth.RoleViewer)
	if err != nil {
		return nil, err
	}
	n, err := h.st.DeleteToken(ctx, db.DeleteTokenParams{ID: req.TokenId, UserID: p.UserID})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errNotFound("token")
	}
	h.audit(ctx, "token.revoke", req.TokenId.String(), nil)
	return gen.DeleteToken204Response{}, nil
}

func (h *handlers) ListAudit(ctx context.Context, req gen.ListAuditRequestObject) (gen.ListAuditResponseObject, error) {
	p, err := need(ctx, auth.PermManageUsers)
	if err != nil {
		return nil, err
	}
	limit, before := 100, h.cfg.Now().Add(time.Minute)
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	if req.Params.Before != nil {
		before = *req.Params.Before
	}
	rows, err := h.st.ListAudit(ctx, db.ListAuditParams{OrgID: p.OrgID, Limit: int32(min(max(limit, 1), 500)), Before: before}) //nolint:gosec // bounded above
	if err != nil {
		return nil, err
	}
	out := gen.ListAudit200JSONResponse{}
	for _, r := range rows {
		var d map[string]any
		_ = json.Unmarshal(r.Details, &d)
		subj, ip := r.Subject, r.Ip
		out = append(out, gen.AuditEntry{Id: r.ID, At: r.At, Actor: r.Actor, Action: r.Action, Subject: &subj, Details: &d, Ip: &ip})
	}
	return out, nil
}
