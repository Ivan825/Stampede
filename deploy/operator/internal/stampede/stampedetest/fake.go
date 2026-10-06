// Package stampedetest is an in-memory fake of the Stampede REST API, enough
// for the operator's tests.
package stampedetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"sigs.k8s.io/yaml"

	"github.com/Ivan825/Stampede/deploy/operator/internal/stampede"
)

// Server is a fake Stampede server.
type Server struct {
	*httptest.Server
	Token string

	mu        sync.Mutex
	seq       int
	projects  []stampede.Project
	targets   map[string][]stampede.Target
	scenarios map[string][]stampede.Scenario
	runs      map[string]*stampede.Run
	runOrder  []string
	stopped   map[string]bool
	workers   int
	creates   int
	last      stampede.RunCreate
}

// RunCreates counts POST .../runs calls.
func (f *Server) RunCreates() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates
}

// LastRunCreate is the body of the latest run create.
func (f *Server) LastRunCreate() stampede.RunCreate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

// New starts a fake server that accepts token.
func New(token string) *Server {
	f := &Server{
		Token:     token,
		targets:   map[string][]stampede.Target{},
		scenarios: map[string][]stampede.Scenario{},
		runs:      map[string]*stampede.Run{},
		stopped:   map[string]bool{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// SetWorkers sets how many connected workers /workers reports.
func (f *Server) SetWorkers(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workers = n
}

// FinishRun moves a run to a terminal status with a verdict.
func (f *Server) FinishRun(id, status, verdict string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[id]
	r.Status = status
	if verdict != "" {
		r.Verdict = &verdict
	}
	r.Summary = &stampede.RunSummary{Requests: 1200, ErrorRate: 0.001, RPS: 20, P95: 0.012, P99: 0.031}
}

// Run returns a copy of a run.
func (f *Server) Run(id string) (stampede.Run, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[id]
	if !ok {
		return stampede.Run{}, false
	}
	return *r, true
}

// Stopped reports whether a stop was requested for the run.
func (f *Server) Stopped(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped[id]
}

// Scenarios returns the scenarios stored in a project.
func (f *Server) Scenarios(projectID string) []stampede.Scenario {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]stampede.Scenario(nil), f.scenarios[projectID]...)
}

func (f *Server) id(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s-%04d", prefix, f.seq)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.Token {
		apiErr(w, http.StatusUnauthorized, "unauthorized", "bad token")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var body map[string]any
	if r.Method == http.MethodPost && r.ContentLength != 0 {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	str := func(k string) string { s, _ := body[k].(string); return s }

	switch {
	case r.Method == http.MethodGet && path == "/workers":
		ws := make([]stampede.Worker, f.workers)
		for i := range ws {
			ws[i] = stampede.Worker{ID: fmt.Sprintf("w-%d", i), Name: fmt.Sprintf("worker-%d", i), Status: "idle"}
		}
		writeJSON(w, http.StatusOK, ws)
	case path == "/projects" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, f.projects)
	case path == "/projects" && r.Method == http.MethodPost:
		p := stampede.Project{ID: f.id("prj"), Name: str("name"), Slug: strings.ReplaceAll(strings.ToLower(str("name")), " ", "-")}
		f.projects = append(f.projects, p)
		writeJSON(w, http.StatusCreated, p)
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "targets":
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, f.targets[parts[1]])
			return
		}
		t := stampede.Target{ID: f.id("tgt"), Name: str("name"), BaseURL: str("baseURL"), Private: true}
		f.targets[parts[1]] = append(f.targets[parts[1]], t)
		writeJSON(w, http.StatusCreated, t)
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "scenarios":
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, f.scenarios[parts[1]])
			return
		}
		var doc struct {
			Metadata struct{ Name string } `json:"metadata"`
		}
		if err := yaml.Unmarshal([]byte(str("yaml")), &doc); err != nil || doc.Metadata.Name == "" {
			apiErr(w, http.StatusUnprocessableEntity, "invalid", "the scenario has problems")
			return
		}
		id := f.id("scn")
		s := stampede.Scenario{ID: id, Name: doc.Metadata.Name, LatestVersion: stampede.ScenarioVersion{ScenarioID: id, Version: 1, YAML: str("yaml")}}
		f.scenarios[parts[1]] = append(f.scenarios[parts[1]], s)
		writeJSON(w, http.StatusCreated, s)
	case len(parts) == 3 && parts[0] == "scenarios" && parts[2] == "versions" && r.Method == http.MethodPost:
		for p, list := range f.scenarios {
			for i := range list {
				if list[i].ID == parts[1] {
					v := stampede.ScenarioVersion{ScenarioID: parts[1], Version: list[i].LatestVersion.Version + 1, YAML: str("yaml")}
					f.scenarios[p][i].LatestVersion = v
					writeJSON(w, http.StatusCreated, v)
					return
				}
			}
		}
		apiErr(w, http.StatusNotFound, "not_found", "no such scenario")
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "runs":
		if r.Method == http.MethodGet {
			out := []stampede.Run{}
			for i := len(f.runOrder) - 1; i >= 0; i-- {
				if run := f.runs[f.runOrder[i]]; run.ProjectID == parts[1] {
					out = append(out, *run)
				}
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		raw, _ := json.Marshal(body)
		var in stampede.RunCreate
		_ = json.Unmarshal(raw, &in)
		f.creates++
		f.last = in
		run := &stampede.Run{ID: f.id("run"), ProjectID: parts[1], ScenarioID: in.ScenarioID, ScenarioVersion: in.Version, Status: "running", Workers: in.Workers, Note: in.Note}
		f.runs[run.ID] = run
		f.runOrder = append(f.runOrder, run.ID)
		writeJSON(w, http.StatusCreated, run)
	case len(parts) == 2 && parts[0] == "runs" && r.Method == http.MethodGet:
		run, ok := f.runs[parts[1]]
		if !ok {
			apiErr(w, http.StatusNotFound, "not_found", "no such run")
			return
		}
		writeJSON(w, http.StatusOK, run)
	case len(parts) == 3 && parts[0] == "runs" && parts[2] == "stop" && r.Method == http.MethodPost:
		run, ok := f.runs[parts[1]]
		if !ok {
			apiErr(w, http.StatusNotFound, "not_found", "no such run")
			return
		}
		f.stopped[parts[1]] = true
		run.Status = "stopping"
		w.WriteHeader(http.StatusAccepted)
	default:
		apiErr(w, http.StatusNotFound, "not_found", r.Method+" "+path)
	}
}
