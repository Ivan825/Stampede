// Package crawl visits a web application in headless Chrome, like one
// visitor clicking through it, and records what it saw: the pages, their
// forms, and every document and API request the pages made, as a HAR
// recording. stampede generate --crawl feeds the recording to the AI
// pipeline in place of a HAR file captured by hand.
//
// The crawler only follows links (GET requests) on the start URL's host.
// It never submits a form, skips links that look destructive (logout,
// delete, remove, unsubscribe), and every request the pages make passes a
// host policy, so it cannot wander onto other sites.
package crawl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/Ivan825/Stampede/internal/engine"
)

// Options configures a crawl.
type Options struct {
	Start string
	// MaxPages bounds the pages visited (default 30); MaxDepth the clicks
	// away from Start (default 3).
	MaxPages int
	MaxDepth int
	// Allow vets every request URL; nil allows only Start's host.
	Allow func(*url.URL) bool
	// PageTimeout bounds each page load (default 20s); Settle is how long
	// to wait after the load for API calls (default 800ms).
	PageTimeout time.Duration
	Settle      time.Duration
	Logger      *slog.Logger
}

// Page is a visited page.
type Page struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Depth  int    `json:"depth"`
}

// Form is a form found on a page; it is never submitted.
type Form struct {
	Page   string   `json:"page"`
	Method string   `json:"method"`
	Action string   `json:"action"`
	Fields []string `json:"fields"`
}

// Result is what a crawl saw.
type Result struct {
	Pages []Page `json:"pages"`
	Forms []Form `json:"forms"`
	// HAR is a HAR 1.2 recording of the documents and API calls.
	HAR []byte `json:"-"`
	// Requests is the number of entries in HAR.
	Requests int `json:"requests"`
}

// Summary describes the forms for the model, which may write journeys
// that submit them.
func (r *Result) Summary() string {
	if len(r.Forms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Forms found while crawling the site (not submitted):\n")
	for _, f := range r.Forms {
		fmt.Fprintf(&b, "- on %s: %s %s with fields %s\n", f.Page, f.Method, f.Action, strings.Join(f.Fields, ", "))
	}
	return b.String()
}

var skipRe = regexp.MustCompile(`(?i)(log-?out|sign-?out|delete|remove|destroy|unsubscribe|cancel-account)`)
var staticRe = regexp.MustCompile(`(?i)\.(js|mjs|css|map|png|jpe?g|gif|svg|ico|webp|avif|woff2?|ttf|otf|eot|mp4|webm|pdf|zip)$`)

type entry struct {
	id         network.RequestID
	started    time.Time
	method     string
	url        string
	reqHeaders map[string]string
	postData   string
	resType    network.ResourceType
	status     int64
	mime       string
	resHeaders map[string]string
	body       string
	done       bool
	// settled is set once the request finished or failed.
	settled bool
}

// Crawl visits the site and returns what it saw.
func Crawl(ctx context.Context, o Options) (*Result, error) {
	start, err := url.Parse(o.Start)
	if err != nil || (start.Scheme != "http" && start.Scheme != "https") || start.Host == "" {
		return nil, fmt.Errorf("crawl start %q is not an absolute http(s) URL", o.Start)
	}
	if o.MaxPages <= 0 {
		o.MaxPages = 30
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = 3
	}
	if o.PageTimeout <= 0 {
		o.PageTimeout = 20 * time.Second
	}
	if o.Settle <= 0 {
		o.Settle = 800 * time.Millisecond
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	allow := o.Allow
	if allow == nil {
		allow = func(u *url.URL) bool { return u.Host == start.Host }
	}
	chrome, err := engine.FindChrome()
	if err != nil {
		return nil, err
	}
	// A busy machine can take a while to start Chrome; the default wait is 20s.
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome), chromedp.WSURLReadTimeout(time.Minute))
	if os.Getenv("STAMPEDE_CHROME_NO_SANDBOX") == "true" {
		opts = append(opts, chromedp.NoSandbox)
	}
	actx, stopA := chromedp.NewExecAllocator(ctx, opts...)
	defer stopA()
	tab, cancel := chromedp.NewContext(actx)
	defer cancel()
	if err := chromedp.Run(tab); err != nil {
		return nil, fmt.Errorf("start Chrome: %w", err)
	}

	var mu sync.Mutex
	entries := map[network.RequestID]*entry{}
	var order []network.RequestID
	var bodies sync.WaitGroup
	// inflight counts the page's document, XHR and fetch requests still
	// loading; quietSince is when it last reached zero.
	inflight, quietSince := 0, time.Now()
	keep := func(t network.ResourceType) bool {
		return t == network.ResourceTypeDocument || t == network.ResourceTypeXHR || t == network.ResourceTypeFetch
	}
	chromedp.ListenTarget(tab, func(ev any) {
		switch ev := ev.(type) {
		case *fetch.EventRequestPaused:
			go func() {
				u, err := url.Parse(ev.Request.URL)
				var act chromedp.Action = fetch.ContinueRequest(ev.RequestID)
				if err != nil || ((u.Scheme == "http" || u.Scheme == "https") && !allow(u)) {
					act = fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient)
				}
				_ = chromedp.Run(tab, act)
			}()
		case *network.EventRequestWillBeSent:
			if !keep(ev.Type) {
				return
			}
			e := &entry{id: ev.RequestID, started: time.Now(), method: ev.Request.Method, url: ev.Request.URL, resType: ev.Type, reqHeaders: headerMap(ev.Request.Headers)}
			if ev.Request.HasPostData {
				for _, pd := range ev.Request.PostDataEntries {
					e.postData += pd.Bytes
				}
			}
			mu.Lock()
			if _, seen := entries[ev.RequestID]; !seen {
				order = append(order, ev.RequestID)
				inflight++
			}
			entries[ev.RequestID] = e
			mu.Unlock()
		case *network.EventResponseReceived:
			mu.Lock()
			if e := entries[ev.RequestID]; e != nil {
				e.status, e.mime, e.resHeaders = ev.Response.Status, ev.Response.MimeType, headerMap(ev.Response.Headers)
			}
			mu.Unlock()
		case *network.EventLoadingFailed:
			mu.Lock()
			if e := entries[ev.RequestID]; e != nil && !e.settled {
				e.settled = true
				if inflight--; inflight == 0 {
					quietSince = time.Now()
				}
			}
			mu.Unlock()
		case *network.EventLoadingFinished:
			mu.Lock()
			e := entries[ev.RequestID]
			if e != nil && !e.settled {
				e.settled = true
				if inflight--; inflight == 0 {
					quietSince = time.Now()
				}
			}
			mu.Unlock()
			if e == nil {
				return
			}
			bodies.Add(1)
			go func() {
				defer bodies.Done()
				var body []byte
				_ = chromedp.Run(tab, chromedp.ActionFunc(func(ctx context.Context) error {
					b, err := network.GetResponseBody(ev.RequestID).Do(ctx)
					body = b
					return err
				}))
				mu.Lock()
				if len(body) > 4096 {
					body = body[:4096]
				}
				e.body, e.done = string(body), true
				mu.Unlock()
			}()
		}
	})
	if err := chromedp.Run(tab, network.Enable(), fetch.Enable()); err != nil {
		return nil, err
	}

	res := &Result{}
	type item struct {
		u     string
		depth int
	}
	queue := []item{{normalize(start), 0}}
	seen := map[string]bool{normalize(start): true}
	for len(queue) > 0 && len(res.Pages) < o.MaxPages {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		it := queue[0]
		queue = queue[1:]
		var info pageInfo
		pctx, pcancel := context.WithTimeout(tab, o.PageTimeout)
		resp, err := chromedp.RunResponse(pctx, chromedp.Navigate(it.u))
		if err == nil {
			// Give the page's scripts time to start their requests, then
			// wait until its network has been quiet for a moment (at most
			// five seconds more), so slow API calls are still recorded.
			time.Sleep(o.Settle)
			// A page that polls forever must not hold the crawl up.
			quietBy := time.Now().Add(5 * time.Second)
			for pctx.Err() == nil && time.Now().Before(quietBy) {
				mu.Lock()
				idle := inflight == 0 && time.Since(quietSince) >= 300*time.Millisecond
				mu.Unlock()
				if idle {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			err = chromedp.Run(pctx, chromedp.Evaluate(extractJS, &info))
		}
		pcancel()
		if err != nil {
			o.Logger.Warn("page not crawled", "url", it.u, "error", err)
			continue
		}
		status := 0
		if resp != nil {
			status = int(resp.Status)
		}
		res.Pages = append(res.Pages, Page{URL: it.u, Title: info.Title, Status: status, Depth: it.depth})
		o.Logger.Info("crawled", "url", it.u, "status", status, "links", len(info.Links), "forms", len(info.Forms))
		for _, f := range info.Forms {
			res.Forms = append(res.Forms, Form{Page: it.u, Method: strings.ToUpper(f.Method), Action: f.Action, Fields: f.Fields})
		}
		if it.depth >= o.MaxDepth {
			continue
		}
		for _, l := range info.Links {
			lu, err := url.Parse(l)
			if err != nil || (lu.Scheme != "http" && lu.Scheme != "https") || lu.Host != start.Host || !allow(lu) {
				continue
			}
			if skipRe.MatchString(lu.Path) || staticRe.MatchString(lu.Path) {
				continue
			}
			n := normalize(lu)
			if !seen[n] {
				seen[n] = true
				queue = append(queue, item{n, it.depth + 1})
			}
		}
	}
	if len(res.Pages) == 0 {
		return nil, fmt.Errorf("could not load %s", o.Start)
	}
	waitTimeout(&bodies, 5*time.Second)

	mu.Lock()
	defer mu.Unlock()
	var list []*entry
	for _, id := range order {
		if e := entries[id]; e != nil && e.status != 0 {
			list = append(list, e)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].started.Before(list[j].started) })
	res.HAR, err = toHAR(list)
	res.Requests = len(list)
	return res, err
}

type pageInfo struct {
	Title string   `json:"title"`
	Links []string `json:"links"`
	Forms []struct {
		Method string   `json:"method"`
		Action string   `json:"action"`
		Fields []string `json:"fields"`
	} `json:"forms"`
}

const extractJS = `(() => ({
  title: document.title,
  links: [...document.querySelectorAll('a[href]')].map(a => a.href).slice(0, 500),
  forms: [...document.forms].slice(0, 50).map(f => ({
    method: (f.getAttribute('method') || 'get'), action: f.action,
    fields: [...f.elements].filter(e => e.name && e.type !== 'submit' && e.type !== 'button').map(e => e.name + (e.type ? ' (' + e.type + ')' : ''))
  }))
}))()`

func normalize(u *url.URL) string {
	c := *u
	c.Fragment = ""
	if c.Path == "" {
		c.Path = "/"
	}
	return c.String()
}

func headerMap(h network.Headers) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func waitTimeout(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

type harNV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func nvs(m map[string]string) []harNV {
	out := make([]harNV, 0, len(m))
	for k, v := range m {
		out = append(out, harNV{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// toHAR writes the entries as a HAR 1.2 log.
func toHAR(list []*entry) ([]byte, error) {
	type postData struct {
		MimeType string `json:"mimeType"`
		Text     string `json:"text"`
	}
	type harEntry struct {
		StartedDateTime string `json:"startedDateTime"`
		Request         struct {
			Method      string    `json:"method"`
			URL         string    `json:"url"`
			HTTPVersion string    `json:"httpVersion"`
			Headers     []harNV   `json:"headers"`
			PostData    *postData `json:"postData,omitempty"`
		} `json:"request"`
		Response struct {
			Status  int64   `json:"status"`
			Headers []harNV `json:"headers"`
			Content struct {
				MimeType string `json:"mimeType"`
				Text     string `json:"text"`
			} `json:"content"`
		} `json:"response"`
	}
	var out struct {
		Log struct {
			Version string     `json:"version"`
			Creator harNV      `json:"creator"`
			Entries []harEntry `json:"entries"`
		} `json:"log"`
	}
	out.Log.Version = "1.2"
	out.Log.Creator = harNV{"stampede crawl", "1"}
	for _, e := range list {
		var h harEntry
		h.StartedDateTime = e.started.UTC().Format(time.RFC3339Nano)
		h.Request.Method, h.Request.URL, h.Request.HTTPVersion = e.method, e.url, "HTTP/1.1"
		h.Request.Headers = nvs(e.reqHeaders)
		if e.postData != "" {
			h.Request.PostData = &postData{MimeType: e.reqHeaders["Content-Type"], Text: e.postData}
		}
		h.Response.Status, h.Response.Headers = e.status, nvs(e.resHeaders)
		h.Response.Content.MimeType, h.Response.Content.Text = e.mime, e.body
		out.Log.Entries = append(out.Log.Entries, h)
	}
	if len(out.Log.Entries) == 0 {
		return nil, errors.New("the crawl recorded no documents or API calls")
	}
	return json.Marshal(out)
}
