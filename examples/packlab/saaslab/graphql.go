package saaslab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

// The GraphQL schema SaaSLab answers (not validated beyond field names):
//
//	type Query {
//	  me: User
//	  dashboard: Dashboard
//	  records(first: Int = 20, status: String): [Record]
//	  record(id: ID!): Record
//	  report(name: String!): Report
//	}
//	type Mutation {
//	  createRecord(title: String!, status: String, amount: Float): Record
//	  updateRecord(id: ID!, title: String, status: String, amount: Float): Record
//	}
//	type Dashboard { tenant: String, openRecords: Int, wonAmount: Float, recent(limit: Int = 10): [Record] }
//	type Record { id: ID, title: String, status: String, amount: Float, updated: String, owner: User }
//	type User { id: ID, name: String, email: String }
//	type Report { name: String, records: Int, rows: [ReportRow] }
//	type ReportRow { key: String, count: Float, amount: Float, revenue: Float, records: Float }

type gqlRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
	Extensions    struct {
		PersistedQuery *struct {
			Version int    `json:"version"`
			SHA256  string `json:"sha256Hash"`
		} `json:"persistedQuery"`
	} `json:"extensions"`
}

type gqlError struct {
	Message    string         `json:"message"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// resolution is the per-request state; owners caches batched owner loads.
type resolution struct {
	s      *server
	ss     *session
	vars   map[string]any
	owners map[int]*user
}

type dashboard struct{ tenant *tenant }

func (s *server) graphql(w http.ResponseWriter, r *http.Request, ss *session) {
	var req gqlRequest
	if !labkit.Decode(w, r, &req) {
		return
	}
	fail := func(msg string, ext map[string]any) {
		labkit.JSON(w, 200, map[string]any{"errors": []gqlError{{Message: msg, Extensions: ext}}})
	}
	// Automatic persisted queries.
	if pq := req.Extensions.PersistedQuery; pq != nil {
		s.apqMu.Lock()
		if req.Query == "" {
			req.Query = s.apq[pq.SHA256]
		} else {
			sum := sha256.Sum256([]byte(req.Query))
			if hex.EncodeToString(sum[:]) != pq.SHA256 {
				s.apqMu.Unlock()
				fail("provided sha does not match query", map[string]any{"code": "PERSISTED_QUERY_HASH_MISMATCH"})
				return
			}
			s.apq[pq.SHA256] = req.Query
		}
		s.apqMu.Unlock()
		if req.Query == "" {
			fail("PersistedQueryNotFound", map[string]any{"code": "PERSISTED_QUERY_NOT_FOUND"})
			return
		}
	}
	doc, err := parser.ParseQuery(&ast.Source{Input: req.Query})
	if err != nil {
		fail(err.Error(), map[string]any{"code": "GRAPHQL_PARSE_FAILED"})
		return
	}
	op := doc.Operations.ForName(req.OperationName)
	if op == nil {
		fail("operation not found", nil)
		return
	}
	res := &resolution{s: s, ss: ss, vars: req.Variables, owners: map[int]*user{}}
	data := map[string]any{}
	var errs []gqlError
	for _, sel := range op.SelectionSet {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		v, err := res.root(op.Operation, f)
		if err != nil {
			errs = append(errs, gqlError{Message: err.Error(), Extensions: map[string]any{"path": []string{f.Alias}}})
			data[f.Alias] = nil
			continue
		}
		data[f.Alias], err = res.complete(v, f.SelectionSet)
		if err != nil {
			errs = append(errs, gqlError{Message: err.Error()})
		}
	}
	out := map[string]any{"data": data}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	labkit.JSON(w, 200, out)
}

func (res *resolution) arg(f *ast.Field, name string) (any, bool) {
	a := f.Arguments.ForName(name)
	if a == nil {
		return nil, false
	}
	v, err := a.Value.Value(res.vars)
	if err != nil || v == nil {
		return nil, false
	}
	return v, true
}

func (res *resolution) str(f *ast.Field, name string) *string {
	v, ok := res.arg(f, name)
	if !ok {
		return nil
	}
	s := fmt.Sprint(v)
	return &s
}

func (res *resolution) num(f *ast.Field, name string, def float64) float64 {
	v, ok := res.arg(f, name)
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int64:
		return float64(n)
	case float64:
		return n
	case json.Number:
		x, _ := n.Float64()
		return x
	}
	return def
}

func (res *resolution) root(op ast.Operation, f *ast.Field) (any, error) {
	s, ss := res.s, res.ss
	if op == ast.Mutation {
		in := recordInput{Title: res.str(f, "title"), Status: res.str(f, "status")}
		if _, ok := res.arg(f, "amount"); ok {
			a := res.num(f, "amount", 0)
			in.Amount = &a
		}
		switch f.Name {
		case "createRecord":
			rec, problem := s.create(ss, in)
			if rec == nil {
				return nil, fmt.Errorf("%s", problem)
			}
			return rec, nil
		case "updateRecord":
			id := res.str(f, "id")
			if id == nil {
				return nil, fmt.Errorf("id is required")
			}
			rec, problem := s.edit(ss, *id, in)
			if problem != "" {
				return nil, fmt.Errorf("%s", problem)
			}
			if rec == nil {
				return nil, fmt.Errorf("record %s not found", *id)
			}
			return rec, nil
		}
		return nil, fmt.Errorf("unknown mutation %s", f.Name)
	}
	switch f.Name {
	case "__typename":
		return "Query", nil
	case "me":
		return ss.user, nil
	case "dashboard":
		return dashboard{ss.tenant}, nil
	case "records":
		t := ss.tenant
		st := ""
		if p := res.str(f, "status"); p != nil {
			st = *p
		}
		t.mu.RLock()
		recs := s.page(t, st, 0, int(res.num(f, "first", 20)))
		t.mu.RUnlock()
		return recs, nil
	case "record":
		id := res.str(f, "id")
		if id == nil {
			return nil, fmt.Errorf("id is required")
		}
		if rec := s.find(ss, *id); rec != nil {
			return rec, nil
		}
		return nil, nil
	case "report":
		name := res.str(f, "name")
		if name == nil {
			return nil, fmt.Errorf("name is required")
		}
		out, ok := s.runReport(ss, *name)
		if !ok {
			return nil, fmt.Errorf("unknown report %s", *name)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown field Query.%s", f.Name)
}

// complete turns a resolved value into JSON shaped by the selection set.
func (res *resolution) complete(v any, sel ast.SelectionSet) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []*record:
		if res.s.cfg.Fixes.On("n1") && selects(sel, "owner") {
			res.batchOwners(x)
		}
		out := make([]any, len(x))
		for i, r := range x {
			o, err := res.complete(r, sel)
			if err != nil {
				return nil, err
			}
			out[i] = o
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			o, err := res.complete(e, sel)
			if err != nil {
				return nil, err
			}
			out[i] = o
		}
		return out, nil
	case []map[string]any:
		out := make([]any, len(x))
		for i, e := range x {
			o, err := res.complete(e, sel)
			if err != nil {
				return nil, err
			}
			out[i] = o
		}
		return out, nil
	}
	if len(sel) == 0 {
		return v, nil
	}
	obj := map[string]any{}
	for _, s := range sel {
		f, ok := s.(*ast.Field)
		if !ok {
			continue
		}
		fv, err := res.field(v, f)
		if err != nil {
			return nil, err
		}
		if obj[f.Alias], err = res.complete(fv, f.SelectionSet); err != nil {
			return nil, err
		}
	}
	return obj, nil
}

func selects(sel ast.SelectionSet, name string) bool {
	for _, s := range sel {
		if f, ok := s.(*ast.Field); ok && f.Name == name {
			return true
		}
	}
	return false
}

// batchOwners loads every owner of a page of records in one query.
func (res *resolution) batchOwners(recs []*record) {
	res.s.query()
	for _, r := range recs {
		res.owners[r.Owner] = res.s.usersByID[r.Owner]
	}
}

func (res *resolution) owner(id int) *user {
	if u, ok := res.owners[id]; ok {
		return u
	}
	// Bottleneck (without "n1"): one query per record.
	res.s.query()
	return res.s.usersByID[id]
}

func (res *resolution) field(parent any, f *ast.Field) (any, error) {
	switch p := parent.(type) {
	case *record:
		t := res.s.tenants[p.Tenant]
		t.mu.RLock()
		defer t.mu.RUnlock()
		switch f.Name {
		case "__typename":
			return "Record", nil
		case "id":
			return p.ID, nil
		case "title":
			return p.Title, nil
		case "status":
			return p.Status, nil
		case "amount":
			return p.Amount, nil
		case "updated":
			return p.Updated.UTC().Format("2006-01-02T15:04:05Z"), nil
		case "owner":
			return res.owner(p.Owner), nil
		}
	case *user:
		switch f.Name {
		case "__typename":
			return "User", nil
		case "id":
			return fmt.Sprint(p.ID), nil
		case "name":
			return p.Name, nil
		case "email":
			return p.Email, nil
		case "tenant":
			return p.Tenant, nil
		}
	case dashboard:
		t := p.tenant
		switch f.Name {
		case "__typename":
			return "Dashboard", nil
		case "tenant":
			return t.name, nil
		case "openRecords":
			t.mu.RLock()
			defer t.mu.RUnlock()
			return res.s.count(t, "open"), nil
		case "wonAmount":
			t.mu.RLock()
			defer t.mu.RUnlock()
			res.s.query()
			sum := 0.0
			for _, r := range t.records[max(0, len(t.records)-500):] { // the last 500 changes
				if r.Status == "won" {
					sum += r.Amount
				}
			}
			return sum, nil
		case "recent":
			t.mu.RLock()
			defer t.mu.RUnlock()
			return res.s.page(t, "", 0, int(res.num(f, "limit", 10))), nil
		}
	case map[string]any:
		if f.Name == "__typename" {
			return "Report", nil
		}
		if f.Name == "name" {
			return p["report"], nil
		}
		return p[f.Name], nil
	}
	return nil, fmt.Errorf("unknown field %s", f.Name)
}
