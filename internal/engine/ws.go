package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/tidwall/gjson"

	"github.com/Ivan825/Stampede/internal/protocol/httpx"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// wsQueue is how many received messages a connection holds for later
// expect steps. A full queue drops its oldest message rather than stop
// reading, so pings and the close handshake are always answered.
const wsQueue = 32

// wsBufMax is the largest message buffer returned to the pool; rarer,
// larger buffers are left to the garbage collector.
const wsBufMax = 64 << 10

// wsBufs recycles message buffers, so a long-lived connection does not
// allocate for every message it receives.
var wsBufs = sync.Pool{New: func() any { b := make([]byte, 0, 1024); return &b }}

// wsMsg is one received message and when it arrived.
type wsMsg struct {
	buf *[]byte
	at  time.Time
}

func (m wsMsg) release() {
	if cap(*m.buf) <= wsBufMax {
		*m.buf = (*m.buf)[:0]
		wsBufs.Put(m.buf)
	}
}

// wsConn is a VU's open WebSocket connection. One goroutine per
// connection reads messages into a small queue; expect steps take from it.
type wsConn struct {
	c    *websocket.Conn
	msgs chan wsMsg
	// done closes when the reader stops; err says why.
	done chan struct{}
	err  error
	// opened and lastSend are the reference points for expect latency.
	opened   time.Time
	lastSend time.Time
}

func newWSConn(c *websocket.Conn, opened time.Time) *wsConn {
	w := &wsConn{c: c, msgs: make(chan wsMsg, wsQueue), done: make(chan struct{}), opened: opened}
	go w.read()
	return w
}

func (w *wsConn) read() {
	defer close(w.done)
	for {
		_, r, err := w.c.Reader(context.Background())
		if err != nil {
			w.err = err
			return
		}
		buf := wsBufs.Get().(*[]byte)
		if *buf, err = readAppend((*buf)[:0], r); err != nil {
			w.err = err
			return
		}
		m := wsMsg{buf: buf, at: time.Now()}
		for sent := false; !sent; {
			select {
			case w.msgs <- m:
				sent = true
			default:
				select {
				case old := <-w.msgs:
					old.release()
				default:
				}
			}
		}
	}
}

// readAppend reads r to the end into b's spare capacity, growing it.
func readAppend(b []byte, r io.Reader) ([]byte, error) {
	for {
		if len(b) == cap(b) {
			b = append(b, 0)[:len(b)]
		}
		n, err := r.Read(b[len(b):cap(b)])
		b = b[:len(b)+n]
		if errors.Is(err, io.EOF) {
			return b, nil
		}
		if err != nil {
			return b, err
		}
	}
}

// close ends the connection with a normal closure. The close handshake
// runs in the background so the user moves on, as a browser does.
func (w *wsConn) close() {
	go func() {
		_ = w.c.Close(websocket.StatusNormalClosure, "")
		<-w.done
		for {
			select {
			case m := <-w.msgs:
				m.release()
			default:
				return
			}
		}
	}()
}

// websocket opens a connection, records the handshake as the step's
// latency, then runs the block's steps with the connection and closes it.
func (v *VU) websocket(ctx context.Context, st *scenario.CStep, intended time.Time) error {
	run := v.begin(st, intended)
	u, err := v.resolveURL(st.Req.URL, nil)
	if err != nil {
		return run.fail("template error", err)
	}
	if err := run.blocked(u); err != nil {
		return err
	}
	header := http.Header{}
	header.Set("User-Agent", v.e.userAgent)
	if err := v.renderHeaders(header, st.Req.Headers); err != nil {
		return run.fail("template error", err)
	}
	v.addTraceHeaders(header)

	dctx, cancel := context.WithTimeout(ctx, v.stepTimeout(st.Req.Timeout))
	defer cancel()
	res := &httpx.Result{Start: time.Now()}
	// The handshake has its own HTTP/1.1 transport (WebSocket cannot ride
	// an HTTP/2 connection) but shares the user's cookies.
	client := &http.Client{Transport: v.e.wsTransport, Jar: v.client.Jar}
	conn, resp, err := websocket.Dial(httpx.Trace(dctx, res), u.String(), &websocket.DialOptions{
		HTTPClient: client, HTTPHeader: header, Subprotocols: st.WS.Subprotocols,
	})
	res.Finish(0, nil)
	if resp != nil {
		res.Status, res.Proto = resp.StatusCode, resp.Proto
	}
	run.fromHTTP(res)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case resp != nil && resp.StatusCode >= 400:
			return run.fail(httpx.StatusError(resp.StatusCode), nil)
		case resp != nil && resp.StatusCode != http.StatusSwitchingProtocols:
			return run.fail("ws handshake failed", err)
		}
		return run.fail(httpx.ClassifyError(err), err)
	}
	conn.SetReadLimit(v.e.maxBody)
	w := newWSConn(conn, res.End)
	_ = run.done()

	v.ws = w
	err = v.runSteps(ctx, st.Steps, time.Time{})
	v.ws = nil
	w.close()
	return err
}

// wsSend writes one text message; its latency is the time to write it.
func (v *VU) wsSend(ctx context.Context, st *scenario.CStep) error {
	run := v.begin(st, time.Time{})
	var payload []byte
	if st.Send.JSON != nil {
		val, err := st.Send.JSON.Value(v.vars)
		if err != nil {
			return run.fail("template error", err)
		}
		if payload, err = json.Marshal(val); err != nil {
			return run.fail("template error", err)
		}
	} else {
		s, err := st.Send.Text.Render(v.vars)
		if err != nil {
			return run.fail("template error", err)
		}
		payload = []byte(s)
	}
	wctx, cancel := context.WithTimeout(ctx, v.stepTimeout(0))
	defer cancel()
	run.s.Start = time.Now()
	err := v.ws.c.Write(wctx, websocket.MessageText, payload)
	run.s.End = time.Now()
	run.s.BytesOut = int64(len(payload))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return run.fail(wsErrorClass(err, v.ws), nil)
	}
	v.ws.lastSend = run.s.Start
	return run.done()
}

// wsExpect waits for a matching message. Its latency runs from the last
// send on the connection (or the handshake, before any send) to the
// arrival of the matching message, so time the message spent queued
// while other steps ran is not counted.
func (v *VU) wsExpect(ctx context.Context, st *scenario.CStep) error {
	run := v.begin(st, time.Time{})
	w, e := v.ws, st.Expect
	base := w.lastSend
	if base.IsZero() {
		base = w.opened
	}
	timer := time.NewTimer(v.stepTimeout(e.Timeout))
	defer timer.Stop()
	take := func(m wsMsg) (bool, error) {
		defer m.release()
		data := *m.buf
		run.s.BytesIn += int64(len(data))
		if !expectMatches(e, data) {
			return false, nil
		}
		run.s.Start, run.s.End = base, m.at
		if m.at.Before(base) {
			run.s.Start = m.at
		}
		run.s.ChecksPassed++
		if err := v.extractAll(&run, e.Extract, &httpx.Result{Body: data}); err != nil {
			return true, err
		}
		return true, run.done()
	}
	for {
		select {
		case m := <-w.msgs:
			if ok, err := take(m); ok {
				return err
			}
		case <-w.done:
			// Messages that arrived before the close still count.
			for {
				select {
				case m := <-w.msgs:
					if ok, err := take(m); ok {
						return err
					}
					continue
				default:
				}
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return run.fail(wsErrorClass(w.err, w), nil)
		case <-timer.C:
			return run.fail("ws expect timeout", nil)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// expectMatches reports whether a message meets every condition.
func expectMatches(e *scenario.CExpect, data []byte) bool {
	if e.Match != nil && !e.Match.Match(data) {
		return false
	}
	for _, jc := range e.JSON {
		got := gjson.GetBytes(data, jc.GJSON)
		if !got.Exists() || !jc.Exists && !jsonEqual(got.Value(), jc.Expected) {
			return false
		}
	}
	return true
}

// wsErrorClass labels a WebSocket failure with a bounded class.
func wsErrorClass(err error, w *wsConn) string {
	var ce websocket.CloseError
	switch {
	case errors.As(err, &ce), errors.Is(err, net.ErrClosed):
		return "ws closed"
	case errors.Is(err, websocket.ErrMessageTooBig):
		return "ws message too large"
	}
	select {
	case <-w.done:
		if errors.As(w.err, &ce) || errors.Is(w.err, net.ErrClosed) || errors.Is(w.err, io.EOF) {
			return "ws closed"
		}
	default:
	}
	if l := httpx.ClassifyError(err); l != "network error" {
		return l
	}
	if strings.Contains(err.Error(), "closed") {
		return "ws closed"
	}
	return "ws error"
}
