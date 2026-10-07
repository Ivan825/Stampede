package engine

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/protocol/httpx"
)

// sensitiveHeaders have their values replaced in error examples.
var sensitiveHeaders = map[string]bool{
	"Authorization": true, "Proxy-Authorization": true, "Cookie": true, "Set-Cookie": true,
	"X-Api-Key": true, "X-Auth-Token": true, "X-Csrf-Token": true,
}

const redacted = "[redacted]"

type exampleKey struct {
	step  int
	class string
}

// wantExample reports whether a failure of step with class should be
// kept as an example: the first MaxErrorExamples of each, per engine.
func (e *Engine) wantExample(step int, class string) bool {
	e.exMu.Lock()
	defer e.exMu.Unlock()
	if e.examples == nil {
		e.examples = map[exampleKey]int{}
	}
	k := exampleKey{step, class}
	if e.examples[k] >= metrics.MaxErrorExamples {
		return false
	}
	e.examples[k]++
	return true
}

// example records what was sent and received, with credentials and the
// run's secret values removed. res may be nil (the request never left)
// and cause explains a transport error.
func (e *Engine) example(req *http.Request, res *httpx.Result, cause error) *metrics.ErrorExample {
	ex := &metrics.ErrorExample{At: time.Now(), Request: req.Method + " " + e.scrub(req.URL.String())}
	ex.RequestHeaders = e.exampleHeaders(req.Header)
	if req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			b, _ := io.ReadAll(io.LimitReader(body, metrics.MaxExampleBody+1))
			body.Close()
			ex.RequestBody = e.snippet(b)
		}
	}
	if tp := req.Header.Get("traceparent"); len(tp) >= 35 {
		ex.TraceID = tp[3:35]
	}
	if res != nil {
		ex.Status = res.Status
		ex.ResponseHeaders = e.exampleHeaders(res.Header)
		ex.ResponseBody = e.snippet(res.Body)
	}
	if cause != nil {
		ex.Detail = e.scrub(cause.Error())
	}
	return ex
}

func (e *Engine) exampleHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make(map[string]string, len(h))
	for _, k := range names {
		v := strings.Join(h[k], ", ")
		if sensitiveHeaders[http.CanonicalHeaderKey(k)] {
			v = redacted
		}
		out[k] = e.scrub(v)
	}
	return out
}

// snippet is the start of a body as text, cut at MaxExampleBody bytes.
func (e *Engine) snippet(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	cut := len(b) > metrics.MaxExampleBody
	if cut {
		b = b[:metrics.MaxExampleBody]
	}
	s := strings.ToValidUTF8(string(bytes.TrimSpace(b)), "�")
	if cut {
		s += "…"
	}
	return e.scrub(s)
}

// scrub replaces the run's secret values wherever they appear, as written
// or URL-encoded.
func (e *Engine) scrub(s string) string {
	for _, v := range e.opts.Secrets {
		if len(v) < 4 {
			continue
		}
		s = strings.ReplaceAll(s, v, redacted)
		for _, enc := range []string{url.QueryEscape(v), url.PathEscape(v)} {
			if enc != v {
				s = strings.ReplaceAll(s, enc, redacted)
			}
		}
	}
	return s
}
