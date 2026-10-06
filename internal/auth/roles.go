package auth

import (
	"context"

	"github.com/google/uuid"
)

// Role is an organisation role.
type Role string

// Roles from most to least privileged.
const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleRunner Role = "runner"
	RoleViewer Role = "viewer"
)

var rank = map[Role]int{RoleViewer: 1, RoleRunner: 2, RoleEditor: 3, RoleAdmin: 4, RoleOwner: 5}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { _, ok := rank[r]; return ok }

// AtLeast reports whether r grants everything min does.
func (r Role) AtLeast(min Role) bool { return rank[r] >= rank[min] }

// Lower returns the less privileged of two roles. A token can never grant
// more than its owner currently has.
func Lower(a, b Role) Role {
	if rank[a] <= rank[b] {
		return a
	}
	return b
}

// Permissions map actions to the minimum role. Keeping them in one place
// makes the role model easy to review.
const (
	PermView          = RoleViewer
	PermRun           = RoleRunner // start, stop and kill runs
	PermEditScenarios = RoleEditor // scenarios, targets, secrets
	PermEditSchedules = RoleEditor // create, change and delete schedules
	PermManageUsers   = RoleAdmin  // users, audit log
	PermManageOwners  = RoleOwner  // grant or remove the owner role
)

// Principal is the authenticated caller.
type Principal struct {
	UserID  uuid.UUID
	OrgID   uuid.UUID
	Email   string
	Name    string
	OrgName string
	Role    Role
	// Via is "session" or "token:<name>".
	Via string
	// SessionHash is set for cookie sessions (used by logout).
	SessionHash []byte
}

// Can reports whether the principal holds at least role min.
func (p *Principal) Can(min Role) bool { return p != nil && p.Role.AtLeast(min) }

// Actor describes the principal for the audit log.
func (p *Principal) Actor() string {
	if p == nil {
		return "anonymous"
	}
	if p.Via != "" && p.Via != "session" {
		return p.Email + " (" + p.Via + ")"
	}
	return p.Email
}

type ctxKey struct{}

// WithPrincipal stores the principal in a context.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal or nil.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}
