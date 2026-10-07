package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/version"
)

// Limits on what a page keeps for error examples.
const (
	maxConsoleLines = 50
	maxHAREntries   = 200
	// maxHARBytes bounds a HAR kept with an example.
	maxHARBytes = 64 << 10
)

// pageLog records what a browser page did, for error examples: console
// errors and warnings, uncaught exceptions, and its requests (no bodies).
type pageLog struct {
	mu      sync.Mutex
	console []string
	order   []network.RequestID
	entries map[network.RequestID]*harEntry
}

type harEntry struct {
	started  time.Time
	wall     float64 // network timestamp of the request, for the duration
	method   string
	url      string
	reqHdr   map[string]string
	status   int64
	statusTx string
	respHdr  map[string]string
	mime     string
	size     float64
	done     float64 // network timestamp when finished or failed
	failure  string
}

func (l *pageLog) logConsole(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.console) < maxConsoleLines {
		l.console = append(l.console, line)
	}
}

// observe handles a page event; it is called from chromedp's listener.
func (l *pageLog) observe(ev any) {
	switch ev := ev.(type) {
	case *cdpruntime.EventConsoleAPICalled:
		if ev.Type != cdpruntime.APITypeError && ev.Type != cdpruntime.APITypeWarning && ev.Type != cdpruntime.APITypeAssert {
			return
		}
		var parts []string
		for _, a := range ev.Args {
			switch {
			case len(a.Value) > 0:
				parts = append(parts, strings.Trim(string(a.Value), `"`))
			case a.Description != "":
				parts = append(parts, a.Description)
			}
		}
		l.logConsole(string(ev.Type) + ": " + strings.Join(parts, " "))
	case *cdpruntime.EventExceptionThrown:
		d := ev.ExceptionDetails
		msg := d.Text
		if d.Exception != nil && d.Exception.Description != "" {
			msg = d.Exception.Description
		}
		l.logConsole("uncaught: " + firstLine(msg))
	case *network.EventRequestWillBeSent:
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.entries == nil {
			l.entries = map[network.RequestID]*harEntry{}
		}
		if _, ok := l.entries[ev.RequestID]; ok || len(l.order) >= maxHAREntries {
			return
		}
		e := &harEntry{method: ev.Request.Method, url: ev.Request.URL, reqHdr: headerStrings(ev.Request.Headers)}
		if ev.WallTime != nil {
			e.started = ev.WallTime.Time()
		}
		if ev.Timestamp != nil {
			e.wall = float64(ev.Timestamp.Time().UnixNano()) / 1e9
		}
		l.entries[ev.RequestID] = e
		l.order = append(l.order, ev.RequestID)
	case *network.EventResponseReceived:
		l.mu.Lock()
		defer l.mu.Unlock()
		if e := l.entries[ev.RequestID]; e != nil && ev.Response != nil {
			e.status, e.statusTx, e.mime = ev.Response.Status, ev.Response.StatusText, ev.Response.MimeType
			e.respHdr = headerStrings(ev.Response.Headers)
		}
	case *network.EventLoadingFinished:
		l.mu.Lock()
		defer l.mu.Unlock()
		if e := l.entries[ev.RequestID]; e != nil {
			e.size = ev.EncodedDataLength
			if ev.Timestamp != nil {
				e.done = float64(ev.Timestamp.Time().UnixNano()) / 1e9
			}
		}
	case *network.EventLoadingFailed:
		l.mu.Lock()
		defer l.mu.Unlock()
		if e := l.entries[ev.RequestID]; e != nil {
			e.failure = ev.ErrorText
			if ev.Timestamp != nil {
				e.done = float64(ev.Timestamp.Time().UnixNano()) / 1e9
			}
		}
	}
}

func headerStrings(h network.Headers) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// har renders the page's requests as a HAR 1.2 log, headers redacted
// and secrets scrubbed by e, newest entries dropped past maxHARBytes.
func (l *pageLog) har(e *Engine) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	type nv struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	pairs := func(h map[string]string) []nv {
		out := []nv{}
		for k, v := range e.exampleHeaders(toHTTPHeader(h)) {
			out = append(out, nv{k, v})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	type entry struct {
		StartedDateTime string         `json:"startedDateTime"`
		Time            float64        `json:"time"`
		Request         map[string]any `json:"request"`
		Response        map[string]any `json:"response"`
		Cache           map[string]any `json:"cache"`
		Timings         map[string]any `json:"timings"`
		Comment         string         `json:"comment,omitempty"`
	}
	var entries []entry
	for _, id := range l.order {
		h := l.entries[id]
		ms := -1.0
		if h.done > 0 && h.wall > 0 {
			ms = (h.done - h.wall) * 1000
		}
		entries = append(entries, entry{
			StartedDateTime: h.started.UTC().Format(time.RFC3339Nano), Time: max(ms, 0),
			Request: map[string]any{"method": h.method, "url": e.scrub(h.url), "httpVersion": "", "headers": pairs(h.reqHdr),
				"queryString": []nv{}, "cookies": []nv{}, "headersSize": -1, "bodySize": -1},
			Response: map[string]any{"status": h.status, "statusText": h.statusTx, "httpVersion": "", "headers": pairs(h.respHdr),
				"cookies": []nv{}, "content": map[string]any{"size": h.size, "mimeType": h.mime}, "redirectURL": "", "headersSize": -1, "bodySize": h.size},
			Cache: map[string]any{}, Timings: map[string]any{"send": 0, "wait": max(ms, 0), "receive": 0},
			Comment: h.failure,
		})
	}
	for {
		b, err := json.Marshal(map[string]any{"log": map[string]any{
			"version": "1.2", "creator": map[string]any{"name": "stampede", "version": version.Version}, "entries": entries,
		}})
		if err != nil {
			return ""
		}
		if len(b) <= maxHARBytes || len(entries) == 0 {
			return string(b)
		}
		entries = entries[:len(entries)*3/4]
	}
}

func toHTTPHeader(m map[string]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = []string{v}
	}
	return out
}

// browserExample builds an error example for a failed browser step: the
// page's URL, a JPEG screenshot, console errors and a HAR of its requests.
func (e *Engine) browserExample(p *page, cause error) *metrics.ErrorExample {
	ex := &metrics.ErrorExample{At: time.Now()}
	if cause != nil {
		ex.Detail = e.scrub(firstLine(cause.Error()))
	}
	// The step's own context may have expired; give the capture its own.
	ctx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()
	var loc string
	var shot []byte
	_ = chromedp.Run(ctx,
		chromedp.Location(&loc),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			shot, err = cdppage.CaptureScreenshot().WithFormat(cdppage.CaptureScreenshotFormatJpeg).WithQuality(55).Do(ctx)
			return err
		}),
	)
	ex.Request = "BROWSER " + e.scrub(loc)
	ex.Screenshot = shot
	p.log.mu.Lock()
	for _, line := range p.log.console {
		ex.Console = append(ex.Console, e.scrub(line))
	}
	p.log.mu.Unlock()
	ex.HAR = p.log.har(e)
	return ex
}
