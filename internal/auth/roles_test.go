package auth

import "testing"

func TestInProject(t *testing.T) {
	tests := []struct {
		name             string
		org, token, over Role
		want             Role
	}{
		{"no override", RoleViewer, "", "", RoleViewer},
		{"raised", RoleViewer, "", RoleEditor, RoleEditor},
		{"lowered", RoleEditor, "", RoleViewer, RoleViewer},
		{"owners stay owners", RoleOwner, "", RoleViewer, RoleOwner},
		{"never made owner", RoleViewer, "", RoleOwner, RoleViewer},
		{"token caps a raise", RoleViewer, RoleRunner, RoleAdmin, RoleRunner},
		{"token below override", RoleAdmin, RoleViewer, RoleEditor, RoleViewer},
		{"unknown override ignored", RoleRunner, "", Role("root"), RoleRunner},
	}
	for _, tc := range tests {
		role := tc.org
		if tc.token != "" {
			role = Lower(tc.org, tc.token)
		}
		p := &Principal{Role: role, OrgRole: tc.org, TokenRole: tc.token}
		got := p.InProject(tc.over)
		if got.Role != tc.want {
			t.Errorf("%s: role %s, want %s", tc.name, got.Role, tc.want)
		}
		if p.Role != role {
			t.Errorf("%s: InProject changed the original principal", tc.name)
		}
	}
	// Principals built without OrgRole fall back to Role.
	if got := (&Principal{Role: RoleRunner}).InProject(RoleEditor).Role; got != RoleEditor {
		t.Errorf("fallback: %s", got)
	}
}
