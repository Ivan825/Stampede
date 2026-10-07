package engine

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// FindChrome returns a Chrome or Chromium executable: $STAMPEDE_CHROME,
// then the usual names on PATH, then the standard install locations.
func FindChrome() (string, error) {
	if p := os.Getenv("STAMPEDE_CHROME"); p != "" {
		if _, err := os.Stat(p); err != nil { //nolint:gosec // the operator chooses the browser
			return "", fmt.Errorf("STAMPEDE_CHROME: %w", err)
		}
		return p, nil
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome", "headless-shell"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	case "windows":
		candidates = []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("browser steps need Chrome or Chromium: install it, or set STAMPEDE_CHROME to its executable")
}

// browserHost owns one headless Chrome for the engine, started on first use.
type browserHost struct {
	once   sync.Once
	err    error
	ctx    context.Context // the browser
	cancel context.CancelFunc
	stopA  context.CancelFunc
}

func (e *Engine) browserCtx() (context.Context, error) {
	h := &e.browser
	h.once.Do(func() {
		path, err := FindChrome()
		if err != nil {
			h.err = err
			return
		}
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(path),
			chromedp.Flag("disable-background-networking", true),
			chromedp.Flag("disable-extensions", true),
			chromedp.Flag("mute-audio", true),
		)
		if os.Getenv("STAMPEDE_CHROME_NO_SANDBOX") == "true" {
			// Containers that run Chrome as root need this; see docs.
			opts = append(opts, chromedp.NoSandbox)
		}
		actx, stopA := chromedp.NewExecAllocator(context.Background(), opts...)
		bctx, cancel := chromedp.NewContext(actx)
		// Start the browser now so its start-up is not counted as a step.
		if err := chromedp.Run(bctx); err != nil {
			cancel()
			stopA()
			h.err = fmt.Errorf("start Chrome (%s): %w", path, err)
			return
		}
		h.ctx, h.cancel, h.stopA = bctx, cancel, stopA
	})
	return h.ctx, h.err
}

func (e *Engine) closeBrowser() {
	if h := &e.browser; h.cancel != nil {
		h.cancel()
		h.stopA()
	}
}

// vitalsJS runs in every document before its own scripts and keeps the
// page's Web Vitals in window.__stampede.
const vitalsJS = `(() => {
  const s = window.__stampede = {fcp: 0, lcp: 0, cls: 0, inp: 0};
  const watch = (type, fn, extra) => { try { new PerformanceObserver(l => l.getEntries().forEach(fn)).observe(Object.assign({type, buffered: true}, extra || {})); } catch (e) {} };
  watch('paint', e => { if (e.name === 'first-contentful-paint') s.fcp = e.startTime; });
  watch('largest-contentful-paint', e => { s.lcp = e.startTime; });
  watch('layout-shift', e => { if (!e.hadRecentInput) s.cls += e.value; });
  watch('event', e => { if (e.interactionId) s.inp = Math.max(s.inp, e.duration); }, {durationThreshold: 16});
})();`

// readVitals reads navigation timing and Web Vitals after a page load.
// Paint entries are delivered a few frames after the load event, so it
// polls each frame until the first contentful paint is known, for at most
// a second (a page with no content never has one). This wait is not part
// of the measured load.
const readVitals = `new Promise(resolve => {
  const t0 = performance.now();
  const read = () => {
    const n = performance.getEntriesByType('navigation')[0] || {};
    const paint = performance.getEntriesByType('paint').find(e => e.name === 'first-contentful-paint');
    if (!paint && performance.now() - t0 < 1000) { requestAnimationFrame(() => setTimeout(read, 16)); return; }
    const s = window.__stampede || {};
    resolve({ttfb: n.responseStart > 0 ? n.responseStart - (n.requestStart || 0) : 0, load: n.loadEventEnd || 0,
      fcp: paint ? paint.startTime : 0, lcp: s.lcp || (paint ? paint.startTime : 0), cls: s.cls || 0, inp: s.inp || 0,
      status: n.responseStatus || 0, bytes: n.transferSize || 0});
  };
  read();
})`

func awaitPromise(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

type vitals struct {
	TTFB   float64 `json:"ttfb"`
	Load   float64 `json:"load"`
	FCP    float64 `json:"fcp"`
	LCP    float64 `json:"lcp"`
	CLS    float64 `json:"cls"`
	INP    float64 `json:"inp"`
	Status int     `json:"status"`
	Bytes  int64   `json:"bytes"`
}

func msDur(ms float64) time.Duration { return time.Duration(ms * float64(time.Millisecond)) }

// page is a VU's browser page for one browser block: a fresh browser
// context, so cookies and storage start empty as for a new visitor.
type page struct {
	ctx     context.Context
	cancel  context.CancelFunc
	timeout time.Duration
	bytes   atomic.Int64 // bytes received by the page so far
	// log keeps console errors and requests for error examples.
	log pageLog
}

func (v *VU) openPage(st *scenario.CStep) (*page, error) {
	bctx, err := v.e.browserCtx()
	if err != nil {
		return nil, err
	}
	// A new browser context per block, like a new visitor. Headless
	// Chrome opens the first tab of a new context only as a new window.
	var bcID cdp.BrowserContextID
	var tid target.ID
	err = chromedp.Run(bctx, chromedp.ActionFunc(func(ctx context.Context) error {
		bx := cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Browser)
		var err error
		if bcID, err = target.CreateBrowserContext().Do(bx); err != nil {
			return err
		}
		tid, err = target.CreateTarget("about:blank").WithBrowserContextID(bcID).WithNewWindow(true).Do(bx)
		return err
	}))
	if err != nil {
		return nil, fmt.Errorf("open browser page: %w", err)
	}
	tctx, tcancel := chromedp.NewContext(bctx, chromedp.WithTargetID(tid))
	ctx := tctx
	cancel := func() {
		tcancel()
		_ = chromedp.Run(bctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return target.DisposeBrowserContext(bcID).Do(cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Browser))
		}))
	}
	p := &page{ctx: ctx, cancel: cancel, timeout: v.stepTimeout(st.Browser.Timeout)}
	allow := v.e.allowHost
	chromedp.ListenTarget(ctx, func(ev any) {
		p.log.observe(ev)
		switch ev := ev.(type) {
		case *fetch.EventRequestPaused:
			// Every request the page makes passes the run's host policy,
			// like the requests of any other step.
			go func() {
				u, err := url.Parse(ev.Request.URL)
				var act chromedp.Action = fetch.ContinueRequest(ev.RequestID)
				if err != nil || (allow != nil && (u.Scheme == "http" || u.Scheme == "https") && !allow(u)) {
					act = fetch.FailRequest(ev.RequestID, network.ErrorReasonBlockedByClient)
				}
				_ = chromedp.Run(ctx, act)
			}()
		case *network.EventLoadingFinished:
			p.bytes.Add(int64(ev.EncodedDataLength))
		}
	})
	vp := st.Browser.Viewport
	setup := chromedp.Tasks{
		network.Enable(),
		cdpruntime.Enable(),
		fetch.Enable(),
		emulation.SetDeviceMetricsOverride(int64(vp.Width), int64(vp.Height), 1, false),
		// Every user's page paints as if it were the focused window, so
		// paint timings and animation frames are not throttled.
		emulation.SetFocusEmulationEnabled(true),
		cdppage.BringToFront(),
		emulation.SetUserAgentOverride(v.e.userAgent + " HeadlessChrome"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := cdppage.AddScriptToEvaluateOnNewDocument(vitalsJS).Do(ctx)
			return err
		}),
	}
	// The first Run attaches to the tab for the lifetime of the context
	// it is given, so it must be the page's own context, not a timeout.
	if err := chromedp.Run(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("open browser page: %w", err)
	}
	sctx, scancel := context.WithTimeout(ctx, 30*time.Second)
	defer scancel()
	if err := chromedp.Run(sctx, setup); err != nil {
		cancel()
		return nil, fmt.Errorf("open browser page: %w", err)
	}
	return p, nil
}

// browserBlock opens a page, loads the block's URL (the step's own
// measurement) and runs the actions in it.
func (v *VU) browserBlock(ctx context.Context, st *scenario.CStep, intended time.Time) error {
	run := v.begin(st, intended)
	u, err := v.resolveURL(st.Browser.URL, nil)
	if err != nil {
		return run.fail("template error", err)
	}
	if err := run.blocked(u); err != nil {
		return err
	}
	p, err := v.openPage(st)
	if err != nil {
		return run.fail("browser unavailable", err)
	}
	defer p.cancel()
	run.page = p
	if err := v.navigate(ctx, &run, p, u.String(), p.timeout); err != nil {
		return err
	}
	v.page = p
	err = v.runSteps(ctx, st.Steps, time.Time{})
	v.page = nil
	return err
}

var netErrRe = regexp.MustCompile(`net::ERR_[A-Z_]+`)

// classifyBrowser turns a chromedp error into a bounded error class.
func classifyBrowser(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded:
		return "timeout"
	case netErrRe.MatchString(err.Error()):
		return "browser " + netErrRe.FindString(err.Error())
	}
	return "browser error"
}

// navigate loads a URL in the page and records the load as run's sample.
func (v *VU) navigate(ctx context.Context, run *stepRun, p *page, rawURL string, timeout time.Duration) error {
	actx, cancel := context.WithTimeout(p.ctx, timeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	before := p.bytes.Load()
	run.s.Start = time.Now()
	var vt vitals
	err := chromedp.Run(actx, chromedp.Navigate(rawURL))
	run.s.End = time.Now()
	if err == nil {
		err = chromedp.Run(actx, chromedp.Evaluate(readVitals, &vt, awaitPromise))
	}
	run.s.BytesIn = p.bytes.Load() - before
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return run.fail(classifyBrowser(actx, err), err)
	}
	run.s.Status = vt.Status
	run.s.Phases[metrics.PhaseWait] = msDur(vt.TTFB)
	run.s.Phases[metrics.PhaseFCP] = msDur(vt.FCP)
	run.s.Phases[metrics.PhaseLCP] = msDur(vt.LCP)
	run.s.Phases[metrics.PhaseCLS] = time.Duration(vt.CLS * float64(time.Second))
	run.s.Phases[metrics.PhaseLoad] = msDur(vt.Load)
	if vt.Status >= 400 {
		return run.fail(fmt.Sprintf("HTTP %d", vt.Status), nil)
	}
	return run.done()
}

var namedKeys = map[string]string{
	"enter": kb.Enter, "tab": kb.Tab, "escape": kb.Escape, "esc": kb.Escape, "backspace": kb.Backspace,
	"delete": kb.Delete, "arrowup": kb.ArrowUp, "arrowdown": kb.ArrowDown, "arrowleft": kb.ArrowLeft,
	"arrowright": kb.ArrowRight, "home": kb.Home, "end": kb.End, "pageup": kb.PageUp, "pagedown": kb.PageDown, "space": " ",
}

// browserAction runs one action in the block's page.
func (v *VU) browserAction(ctx context.Context, st *scenario.CStep) error {
	run := v.begin(st, time.Time{})
	p := v.page
	run.page = p
	if p == nil {
		return run.fail("browser error", errors.New("browser action outside a browser block"))
	}
	a := st.Action
	timeout := p.timeout
	if a.Timeout > 0 {
		timeout = a.Timeout.D()
	}
	render := func(t *scenario.Template) (string, error) { return t.Render(v.vars) }

	var target string
	if a.Target != nil {
		var err error
		if target, err = render(a.Target); err != nil {
			return run.fail("template error", err)
		}
	}
	if st.Kind == scenario.StepGoto {
		u, err := v.resolveURL(a.Target, nil)
		if err != nil {
			return run.fail("template error", err)
		}
		if err := run.blocked(u); err != nil {
			return err
		}
		return v.navigate(ctx, &run, p, u.String(), timeout)
	}

	var tasks chromedp.Tasks
	switch st.Kind {
	case scenario.StepClick:
		tasks = append(tasks, chromedp.Click(target, chromedp.ByQuery))
	case scenario.StepWaitFor:
		tasks = append(tasks, chromedp.WaitVisible(target, chromedp.ByQuery))
	case scenario.StepPress:
		key := target
		if k, ok := namedKeys[strings.ToLower(target)]; ok {
			key = k
		}
		tasks = append(tasks, chromedp.KeyEvent(key))
	case scenario.StepFill:
		for _, pv := range a.Pairs {
			sel, err := render(pv.Selector)
			if err != nil {
				return run.fail("template error", err)
			}
			val, err := render(pv.Value)
			if err != nil {
				return run.fail("template error", err)
			}
			tasks = append(tasks, chromedp.Clear(sel, chromedp.ByQuery), chromedp.SendKeys(sel, val, chromedp.ByQuery))
		}
	case scenario.StepAssert:
		texts := make([]string, len(a.Pairs))
		wants := make([]string, len(a.Pairs))
		for i, pv := range a.Pairs {
			sel, err := render(pv.Selector)
			if err != nil {
				return run.fail("template error", err)
			}
			if wants[i], err = render(pv.Value); err != nil {
				return run.fail("template error", err)
			}
			tasks = append(tasks, chromedp.Text(sel, &texts[i], chromedp.ByQuery))
		}
		actx, cancel := context.WithTimeout(p.ctx, timeout)
		defer cancel()
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		run.s.Start = time.Now()
		err := chromedp.Run(actx, tasks)
		run.s.End = time.Now()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return run.fail(classifyBrowser(actx, err), err)
		}
		for i := range texts {
			if !strings.Contains(texts[i], wants[i]) {
				run.s.ChecksFailed++
				return run.fail("check failed", fmt.Errorf("assert: text %q does not contain %q", truncate(texts[i], 120), wants[i]))
			}
			run.s.ChecksPassed++
		}
		return run.done()
	}

	actx, cancel := context.WithTimeout(p.ctx, timeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	before := p.bytes.Load()
	run.s.Start = time.Now()
	err := chromedp.Run(actx, tasks)
	run.s.End = time.Now()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return run.fail(classifyBrowser(actx, err), err)
	}
	if st.Kind == scenario.StepClick || st.Kind == scenario.StepPress {
		// Interaction to next paint: let the page paint, then read the
		// slowest interaction so far.
		var inp float64
		_ = chromedp.Run(actx, chromedp.Evaluate(`new Promise(r => requestAnimationFrame(() => setTimeout(() => r((window.__stampede || {}).inp || 0), 0)))`, &inp, awaitPromise))
		run.s.Phases[metrics.PhaseINP] = msDur(inp)
	}
	run.s.BytesIn = p.bytes.Load() - before
	return run.done()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// usesBrowser reports whether any journey has a browser step.
func (e *Engine) usesBrowser() bool {
	var has func([]*scenario.CStep) bool
	has = func(steps []*scenario.CStep) bool {
		for _, st := range steps {
			if st.Kind == scenario.StepBrowser || has(st.Steps) {
				return true
			}
			for _, b := range st.Branches {
				if has(b.Steps) {
					return true
				}
			}
		}
		return false
	}
	for _, j := range e.prog.Journeys {
		if has(j.Steps) {
			return true
		}
	}
	return false
}
