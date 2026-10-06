// Package sse reads server-sent event streams (text/event-stream), as
// defined by the HTML Living Standard. It is built for load generation:
// a Reader reuses its buffers, so reading an event does not allocate once
// the buffers have grown to the stream's largest event.
package sse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// ErrTooLarge is returned when one event or line exceeds the reader's limit.
var ErrTooLarge = errors.New("sse: event larger than the limit")

// Event is one dispatched event. Data is only valid until the next call
// to Next; copy it to keep it.
type Event struct {
	// Type is the event field, or "message" when the stream sets none.
	Type string
	ID   string
	Data []byte
}

// Reader parses events from a stream.
type Reader struct {
	br    *bufio.Reader
	limit int
	n     int64
	line  []byte
	// skipLF is set after a CR line ending, so a following LF (CRLF)
	// does not count as an empty line.
	skipLF bool
	data   []byte
	typ    []byte
	id     string
	ev     Event
}

// NewReader reads events from r. An event or line larger than limit bytes
// stops the stream with ErrTooLarge; limit <= 0 means 10 MiB.
func NewReader(r io.Reader, limit int) *Reader {
	if limit <= 0 {
		limit = 10 << 20
	}
	return &Reader{br: bufio.NewReaderSize(r, 4096), limit: limit}
}

// BytesRead is the number of bytes consumed from the stream so far.
func (r *Reader) BytesRead() int64 { return r.n }

// Next returns the next event. It returns io.EOF when the stream ends; a
// trailing event without its terminating blank line is discarded, as the
// standard requires.
func (r *Reader) Next() (*Event, error) {
	for {
		line, err := r.readLine()
		if err != nil {
			return nil, err
		}
		ev, err := r.field(line)
		if err != nil || ev != nil {
			return ev, err
		}
	}
}

// readLine returns the next line without its terminator, which may be
// LF, CRLF or a lone CR. It returns as soon as the terminator arrives,
// without waiting to see whether a CR is followed by LF.
func (r *Reader) readLine() ([]byte, error) {
	r.line = r.line[:0]
	for {
		if r.br.Buffered() == 0 {
			if _, err := r.br.Peek(1); err != nil {
				return nil, err
			}
		}
		buf, _ := r.br.Peek(r.br.Buffered())
		if r.skipLF {
			r.skipLF = false
			if buf[0] == '\n' {
				r.discard(1)
				continue
			}
		}
		i := bytes.IndexAny(buf, "\r\n")
		if i < 0 {
			if len(r.line)+len(buf) > r.limit {
				return nil, ErrTooLarge
			}
			r.line = append(r.line, buf...)
			r.discard(len(buf))
			continue
		}
		r.skipLF = buf[i] == '\r'
		r.line = append(r.line, buf[:i]...)
		r.discard(i + 1)
		return r.line, nil
	}
}

func (r *Reader) discard(n int) {
	_, _ = r.br.Discard(n) // n bytes are buffered, so this cannot fail
	r.n += int64(n)
}

// field processes one line and returns an event when the line dispatches one.
func (r *Reader) field(line []byte) (*Event, error) {
	if len(line) == 0 {
		if len(r.data) == 0 {
			r.typ = r.typ[:0]
			return nil, nil
		}
		r.ev.Data = r.data[:len(r.data)-1] // drop the final LF
		r.ev.ID = r.id
		switch {
		case len(r.typ) == 0:
			r.ev.Type = "message"
		case string(r.typ) != r.ev.Type:
			// Streams usually repeat one type; reuse its string.
			r.ev.Type = string(r.typ)
		}
		r.data, r.typ = r.data[:0], r.typ[:0]
		return &r.ev, nil
	}
	if line[0] == ':' {
		return nil, nil // a comment, often a keep-alive
	}
	name, value, _ := bytes.Cut(line, []byte{':'})
	value = bytes.TrimPrefix(value, []byte{' '})
	switch string(name) {
	case "data":
		if len(r.data)+len(value) >= r.limit {
			return nil, ErrTooLarge
		}
		r.data = append(r.data, value...)
		r.data = append(r.data, '\n')
	case "event":
		r.typ = append(r.typ[:0], value...)
	case "id":
		if bytes.IndexByte(value, 0) < 0 {
			r.id = string(value)
		}
	}
	return nil, nil
}
