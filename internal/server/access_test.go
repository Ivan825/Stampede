package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// memberWithID creates a user with an organisation role and returns a client
// signed in as them and their id.
func memberWithID(t *testing.T, owner *client, base, email, role string) (*client, string) {
	t.Helper()
	var u map[string]any
	if code := owner.do("POST", "/users", map[string]string{"email": email, "name": role, "role": role, "password": "member password 1"}, &u); code != 201 {
		t.Fatalf("create %s: %d %v", email, code, u)
	}
	c := newClient(t, base)
	if code := c.do("POST", "/auth/login", map[string]string{"email": email, "password": "member password 1"}, nil); code != 200 {
		t.Fatalf("login %s: %d", email, code)
	}
	return c, u["id"].(string)
}

// TestProjectRoleOverrides shows a viewer in the organisation acting as an
// editor in one project, an editor acting as a viewer in another, tokens
// staying capped, and organisation admins keeping control of roles.
func TestProjectRoleOverrides(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	base := startServer(t)
	owner := newClient(t, base)
	setup(t, owner)

	pids := map[string]string{}
	tids := map[string]string{}
	for _, name := range []string{"alpha", "beta"} {
		var proj, tgt map[string]any
		owner.do("POST", "/projects", map[string]string{"name": name}, &proj)
		pids[name] = proj["id"].(string)
		owner.do("POST", "/projects/"+pids[name]+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)
		tids[name] = tgt["id"].(string)
	}
	viewer, viewerID := memberWithID(t, owner, base, "viewer@acme.test", "viewer")
	editor, editorID := memberWithID(t, owner, base, "editor@acme.test", "editor")

	// Only admins set overrides.
	if code := viewer.do("PUT", "/projects/"+pids["alpha"]+"/roles/"+viewerID, map[string]string{"role": "editor"}, nil); code != 403 {
		t.Errorf("viewer raising themselves: %d", code)
	}
	var pr map[string]any
	if code := owner.do("PUT", "/projects/"+pids["alpha"]+"/roles/"+viewerID, map[string]string{"role": "editor"}, &pr); code != 200 || pr["role"] != "editor" || pr["orgRole"] != "viewer" {
		t.Fatalf("set viewer→editor: %d %v", code, pr)
	}
	if code := owner.do("PUT", "/projects/"+pids["alpha"]+"/roles/"+editorID, map[string]string{"role": "viewer"}, nil); code != 200 {
		t.Fatalf("set editor→viewer: %d", code)
	}

	scenario := func(name string) map[string]string {
		return map[string]string{"yaml": "metadata: {name: " + name + "}\njourneys: [{name: a, steps: [{get: /}]}]\nload: {iterations: 1}"}
	}
	var e errBody
	// The org viewer edits in alpha but not in beta.
	var sc map[string]any
	if code := viewer.do("POST", "/projects/"+pids["alpha"]+"/scenarios", scenario("by-viewer"), &sc); code != 201 {
		t.Errorf("viewer as editor in alpha: %d", code)
	}
	if code := viewer.do("POST", "/projects/"+pids["beta"]+"/scenarios", scenario("by-viewer"), &e); code != 403 {
		t.Errorf("viewer in beta: %d", code)
	}
	// ...and may start runs there (editor includes runner).
	if code := viewer.do("POST", "/projects/"+pids["alpha"]+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tids["alpha"]}, nil); code != 201 {
		t.Errorf("viewer as editor starts a run in alpha: %d", code)
	}

	// The org editor is only a viewer in alpha.
	e = errBody{}
	if code := editor.do("POST", "/projects/"+pids["alpha"]+"/scenarios", scenario("by-editor"), &e); code != 403 || !strings.Contains(e.Error.Message, "in this project; you are viewer here") {
		t.Errorf("editor as viewer in alpha: %d %+v", code, e)
	}
	if code := editor.do("POST", "/projects/"+pids["alpha"]+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tids["alpha"]}, nil); code != 403 {
		t.Errorf("editor as viewer starts a run in alpha: %d", code)
	}
	if code := editor.do("DELETE", "/scenarios/"+sc["id"].(string), nil, nil); code != 403 {
		t.Errorf("editor as viewer deletes a scenario in alpha: %d", code)
	}
	if code := editor.do("POST", "/projects/"+pids["beta"]+"/scenarios", scenario("by-editor"), nil); code != 201 {
		t.Errorf("editor in beta: %d", code)
	}
	// Reading is unaffected.
	if code := editor.do("GET", "/scenarios/"+sc["id"].(string), nil, nil); code != 200 {
		t.Errorf("editor reads alpha: %d", code)
	}

	// Projects report the caller's role in each.
	var projects []map[string]any
	viewer.do("GET", "/projects", nil, &projects)
	roles := map[string]any{}
	for _, p := range projects {
		roles[p["name"].(string)] = p["role"]
	}
	if roles["alpha"] != "editor" || roles["beta"] != "viewer" {
		t.Errorf("viewer's project roles: %v", roles)
	}

	// A token never grants more than its own role, override or not.
	var tok map[string]any
	if code := viewer.do("POST", "/tokens", map[string]any{"name": "ci", "role": "viewer"}, &tok); code != 201 {
		t.Fatalf("token: %d", code)
	}
	bot := newClient(t, base)
	bot.token, bot.csrf = tok["secret"].(string), false
	if code := bot.do("POST", "/projects/"+pids["alpha"]+"/scenarios", scenario("by-token"), nil); code != 403 {
		t.Errorf("viewer token in alpha: %d", code)
	}

	// Owners cannot be overridden, nor can anyone be made owner of a project.
	var me map[string]any
	owner.do("GET", "/me", nil, &me)
	if code := owner.do("PUT", "/projects/"+pids["alpha"]+"/roles/"+me["id"].(string), map[string]string{"role": "viewer"}, nil); code != 422 {
		t.Errorf("override for an owner: %d", code)
	}
	if code := owner.do("PUT", "/projects/"+pids["alpha"]+"/roles/"+viewerID, map[string]string{"role": "owner"}, nil); code != 422 {
		t.Errorf("project owner: %d", code)
	}

	var list []map[string]any
	if code := editor.do("GET", "/projects/"+pids["alpha"]+"/roles", nil, &list); code != 200 || len(list) != 2 {
		t.Errorf("list overrides: %d %v", code, list)
	}

	// An org admin lowered to viewer in a project still manages its roles,
	// but cannot edit there.
	admin, adminID := memberWithID(t, owner, base, "admin@acme.test", "admin")
	owner.do("PUT", "/projects/"+pids["alpha"]+"/roles/"+adminID, map[string]string{"role": "viewer"}, nil)
	if code := admin.do("POST", "/projects/"+pids["alpha"]+"/scenarios", scenario("by-admin"), nil); code != 403 {
		t.Errorf("admin as viewer edits alpha: %d", code)
	}
	if code := admin.do("DELETE", "/projects/"+pids["alpha"]+"/roles/"+viewerID, nil, nil); code != 204 {
		t.Errorf("admin removes an override: %d", code)
	}
	// Without the override the viewer is a viewer again.
	if code := viewer.do("POST", "/projects/"+pids["alpha"]+"/scenarios", scenario("again"), nil); code != 403 {
		t.Errorf("viewer after the override is removed: %d", code)
	}
	if code := admin.do("DELETE", "/projects/"+pids["alpha"]+"/roles/"+viewerID, nil, nil); code != 404 {
		t.Errorf("remove a missing override: %d", code)
	}
}

// TestOrgAndProjectCaps checks organisation and project caps are set by
// admins and enforced at run start along with the target's.
func TestOrgAndProjectCaps(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	base := startServer(t)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt map[string]any
	c.do("POST", "/projects", map[string]string{"name": "capped"}, &proj)
	pid := proj["id"].(string)
	c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)

	var caps map[string]any
	if code := c.do("GET", "/organisation/caps", nil, &caps); code != 200 || len(caps) != 0 {
		t.Errorf("no org caps: %d %v", code, caps)
	}
	var settings map[string]any
	if code := c.do("GET", "/projects/"+pid+"/settings", nil, &settings); code != 200 || settings["requireDryRun"] != false {
		t.Errorf("default settings: %d %v", code, settings)
	}
	runner, _ := memberWithID(t, c, base, "runner@acme.test", "runner")
	if code := runner.do("PUT", "/organisation/caps", map[string]any{"maxVUs": 100}, nil); code != 403 {
		t.Errorf("runner sets org caps: %d", code)
	}
	if code := runner.do("PUT", "/projects/"+pid+"/settings", map[string]any{"caps": map[string]any{}, "requireDryRun": false}, nil); code != 403 {
		t.Errorf("runner sets project settings: %d", code)
	}
	if code := c.do("PUT", "/organisation/caps", map[string]any{"maxVUs": -1}, nil); code != 422 {
		t.Errorf("negative cap: %d", code)
	}
	if code := c.do("PUT", "/organisation/caps", map[string]any{"maxVUs": 10}, &caps); code != 200 || caps["maxVUs"] != float64(10) {
		t.Fatalf("set org caps: %d %v", code, caps)
	}
	if code := c.do("PUT", "/projects/"+pid+"/settings", map[string]any{"caps": map[string]any{"maxDurationSeconds": 5}, "requireDryRun": false}, &settings); code != 200 {
		t.Fatalf("set project caps: %d %v", code, settings)
	}

	try := func(name, load string) (int, errBody) {
		t.Helper()
		var sc map[string]any
		if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": "metadata: {name: " + name + "}\njourneys: [{name: a, steps: [{get: /}]}]\nload: " + load}, &sc); code != 201 {
			t.Fatalf("scenario %s: %d", name, code)
		}
		var e errBody
		return c.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &e), e
	}
	if code, e := try("too-many-users", "{vus: 20, duration: 2s}"); code != 403 || !strings.Contains(e.Error.Message, "over the organisation's caps: 20 virtual users exceeds the cap of 10") {
		t.Errorf("org cap: %d %+v", code, e)
	}
	if code, e := try("too-long", "{vus: 5, duration: 10s}"); code != 403 || !strings.Contains(e.Error.Message, "over project capped's caps: duration 10s exceeds the cap of 5s") {
		t.Errorf("project cap: %d %+v", code, e)
	}
	if code, e := try("fits", "{vus: 5, duration: 2s}"); code != 201 {
		t.Errorf("within every cap: %d %+v", code, e)
	}
	// Removing the org caps lifts them.
	c.do("PUT", "/organisation/caps", map[string]any{}, nil)
	if code, e := try("many-users", "{vus: 20, duration: 2s}"); code != 201 {
		t.Errorf("after removing org caps: %d %+v", code, e)
	}
}
