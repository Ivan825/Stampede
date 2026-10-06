package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"time"

	"github.com/tidwall/gjson"

	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// gqlRequest is the GraphQL-over-HTTP request body.
type gqlRequest struct {
	Query         string         `json:"query,omitempty"`
	Variables     any            `json:"variables,omitempty"`
	OperationName string         `json:"operationName,omitempty"`
	Extensions    *gqlExtensions `json:"extensions,omitempty"`
}

type gqlExtensions struct {
	PersistedQuery gqlPersisted `json:"persistedQuery"`
}

type gqlPersisted struct {
	Version int    `json:"version"`
	Hash    string `json:"sha256Hash"`
}

// graphql runs a GraphQL operation. With an automatic persisted query it
// first sends only the hash and, if the server does not know it, sends
// again with the full query; the step's latency covers both, as a user
// would wait for both.
func (v *VU) graphql(ctx context.Context, st *scenario.CStep, intended time.Time) error {
	r, g := st.Req, st.GraphQL
	run := v.begin(st, intended)

	body := gqlRequest{OperationName: g.OperationName}
	if g.Query != nil {
		q, err := g.Query.Render(v.vars)
		if err != nil {
			return run.fail("template error", err)
		}
		body.Query = q
	}
	if g.Variables != nil {
		vars, err := g.Variables.Value(v.vars)
		if err != nil {
			return run.fail("template error", err)
		}
		body.Variables = vars
	}
	query := body.Query
	if g.Persisted {
		hash := g.Hash
		if hash == "" {
			sum := sha256.Sum256([]byte(query))
			hash = hex.EncodeToString(sum[:])
		}
		body.Extensions = &gqlExtensions{PersistedQuery: gqlPersisted{Version: 1, Hash: hash}}
		body.Query = ""
	}

	u, err := v.resolveURL(r.URL, nil)
	if err != nil {
		return run.fail("template error", err)
	}
	if err := run.blocked(u); err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, v.stepTimeout(r.Timeout))
	defer cancel()

	res, err := v.gqlExchange(rctx, u, r, &body)
	if err != nil {
		return run.fail("template error", err)
	}
	if g.Persisted && res.Err == nil && persistedQueryNotFound(res.Body) {
		if query == "" {
			run.fromHTTP(res)
			return run.fail("graphql persisted query not found", nil)
		}
		first := res
		body.Query = query
		if res, err = v.gqlExchange(rctx, u, r, &body); err != nil {
			return run.fail("template error", err)
		}
		res.Start = first.Start
		res.BytesIn += first.BytesIn
		res.BytesOut += first.BytesOut
	}
	run.fromHTTP(res)
	if res.Err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return run.fail(httpx.ClassifyError(res.Err), nil)
	}

	// A GraphQL error usually arrives with HTTP 200, so it is checked once
	// the status is acceptable and before the step's own checks, whose
	// failures would otherwise hide the real cause.
	statusOK := res.Status < 400
	if r.Check != nil && !r.Check.Status.Empty() {
		statusOK = r.Check.Status.Match(res.Status)
	}
	if statusOK {
		if !gjson.ValidBytes(res.Body) {
			return run.fail("graphql invalid response", nil)
		}
		if !g.AllowErrors && gjson.GetBytes(res.Body, "errors.#").Int() > 0 {
			return run.fail("graphql error", nil)
		}
	}
	return v.verify(&run, r, res)
}

// gqlExchange posts one GraphQL request and keeps the whole response,
// which is needed to look for errors.
func (v *VU) gqlExchange(ctx context.Context, u *url.URL, r *scenario.CRequest, body *gqlRequest) (*httpx.Result, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := v.newRequest(ctx, "POST", u, bytes.NewReader(b), "application/json", r.Headers)
	if err != nil {
		return nil, err
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/graphql-response+json, application/json")
	}
	return httpx.Do(v.client, req, int64(len(b)), true, v.e.maxBody), nil
}

// persistedQueryNotFound reports whether the server asked for the full
// query because it does not know a persisted query's hash. Apollo and
// most other servers send either the message or the error code.
func persistedQueryNotFound(body []byte) bool {
	found := false
	gjson.GetBytes(body, "errors").ForEach(func(_, e gjson.Result) bool {
		if e.Get("message").Str == "PersistedQueryNotFound" || e.Get("extensions.code").Str == "PERSISTED_QUERY_NOT_FOUND" {
			found = true
		}
		return !found
	})
	return found
}
