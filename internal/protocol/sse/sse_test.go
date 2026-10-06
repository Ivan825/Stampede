package sse

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

// ev is an Event with its data as a string, for comparisons.
type ev struct{ Type, ID, Data string }

func readAll(t *testing.T, r *Reader) ([]ev, error) {
	t.Helper()
	var out []ev
	for {
		e, err := r.Next()
		if err != nil {
			return out, err
		}
		out = append(out, ev{e.Type, e.ID, string(e.Data)})
	}
}

func TestReader(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		want   []ev
	}{
		{"one event", "data: hello\n\n", []ev{{Type: "message", Data: "hello"}}},
		{"multi-line data", "data: a\ndata: b\n\n", []ev{{Type: "message", Data: "a\nb"}}},
		{"type and id", "event: token\nid: 7\ndata: {\"t\":\"hi\"}\n\n",
			[]ev{{Type: "token", ID: "7", Data: `{"t":"hi"}`}}},
		{"id persists", "id: 1\ndata: a\n\ndata: b\n\n",
			[]ev{{Type: "message", ID: "1", Data: "a"}, {Type: "message", ID: "1", Data: "b"}}},
		{"comments and empty events are skipped", ": ping\n\nevent: x\n\ndata: y\n\n", []ev{{Type: "message", Data: "y"}}},
		{"CRLF", "data: a\r\n\r\ndata: b\r\n\r\n", []ev{{Type: "message", Data: "a"}, {Type: "message", Data: "b"}}},
		{"lone CR", "data: a\r\rdata: b\r\r", []ev{{Type: "message", Data: "a"}, {Type: "message", Data: "b"}}},
		{"no space after colon", "data:x\n\n", []ev{{Type: "message", Data: "x"}}},
		{"field without colon", "data\n\n", []ev{{Type: "message", Data: ""}}},
		{"unterminated event is dropped", "data: a\n\ndata: partial", []ev{{Type: "message", Data: "a"}}},
		{"unknown fields ignored", "retry: 10\nfoo: bar\ndata: z\n\n", []ev{{Type: "message", Data: "z"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// One byte at a time exercises lines split across reads.
			for _, rd := range []io.Reader{strings.NewReader(tc.stream), iotest.OneByteReader(strings.NewReader(tc.stream))} {
				r := NewReader(rd, 0)
				got, err := readAll(t, r)
				if !errors.Is(err, io.EOF) {
					t.Fatalf("err = %v", err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("got %q\nwant %q", got, tc.want)
				}
				if r.BytesRead() != int64(len(tc.stream)) {
					t.Errorf("read %d bytes of %d", r.BytesRead(), len(tc.stream))
				}
			}
		})
	}
}

func TestReaderLimit(t *testing.T) {
	long := "data: " + strings.Repeat("x", 10000) + "\n\n"
	if _, err := readAll(t, NewReader(strings.NewReader(long), 100)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("long line: err = %v, want ErrTooLarge", err)
	}
	many := strings.Repeat("data: 0123456789\n", 20) + "\n"
	if _, err := readAll(t, NewReader(strings.NewReader(many), 100)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("many lines: err = %v, want ErrTooLarge", err)
	}
	evs, err := readAll(t, NewReader(strings.NewReader(long), 0))
	if !errors.Is(err, io.EOF) || len(evs) != 1 || len(evs[0].Data) != 10000 {
		t.Errorf("a line longer than the buffer must still parse: %d events, %v", len(evs), err)
	}
}

func BenchmarkReader(b *testing.B) {
	stream := strings.Repeat("event: token\ndata: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n", 100)
	b.ReportAllocs()
	r := strings.NewReader(stream)
	rd := NewReader(r, 0)
	for b.Loop() {
		r.Reset(stream)
		rd.br.Reset(r)
		for {
			if _, err := rd.Next(); err != nil {
				break
			}
		}
	}
}
