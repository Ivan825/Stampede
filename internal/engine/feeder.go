package engine

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"sync/atomic"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// errDataExhausted is returned when a unique feeder has no rows left.
var errDataExhausted = errors.New("test data exhausted")

// feeder hands out rows to virtual users according to its mode.
type feeder struct {
	name   string
	mode   string
	wrap   bool
	rows   []any
	cursor atomic.Uint64
}

// loadFeeder reads a feeder's rows and keeps the partition that belongs to
// this worker: row i is ours when i % count == index, so two workers never
// use the same row in unique mode.
func loadFeeder(name string, f scenario.Feeder, index, count int) (*feeder, error) {
	var rows []any
	var err error
	switch {
	case f.CSV != "":
		rows, err = readCSV(f.CSV)
	case f.JSON != "":
		rows, err = readJSONRows(f.JSON)
	case len(f.List) > 0:
		rows = f.List
	case len(f.Range) == 2:
		n := f.Range[1] - f.Range[0] + 1
		if n > 10_000_000 {
			return nil, fmt.Errorf("data.%s: range has %d values; the limit is 10,000,000", name, n)
		}
		rows = make([]any, 0, n)
		for v := f.Range[0]; v <= f.Range[1]; v++ {
			rows = append(rows, v)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("data.%s: %w", name, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("data.%s: no rows", name)
	}
	if f.Mode == scenario.FeedUnique && count > 1 {
		part := make([]any, 0, len(rows)/count+1)
		for i := index; i < len(rows); i += count {
			part = append(part, rows[i])
		}
		if len(part) == 0 {
			return nil, fmt.Errorf("data.%s: %d rows cannot be split across %d workers", name, len(rows), count)
		}
		rows = part
	}
	return &feeder{name: name, mode: f.Mode, wrap: f.OnExhausted == "wrap", rows: rows}, nil
}

// next returns a row for virtual user vu.
func (f *feeder) next(vu int) (any, error) {
	switch f.mode {
	case scenario.FeedRandom:
		return f.rows[rand.IntN(len(f.rows))], nil
	case scenario.FeedPerVU:
		return f.rows[vu%len(f.rows)], nil
	case scenario.FeedUnique:
		i := f.cursor.Add(1) - 1
		if i >= uint64(len(f.rows)) {
			if !f.wrap {
				return nil, errDataExhausted
			}
			i %= uint64(len(f.rows))
		}
		return f.rows[i], nil
	default: // sequential
		i := f.cursor.Add(1) - 1
		return f.rows[i%uint64(len(f.rows))], nil
	}
}

func readCSV(path string) ([]any, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	r := csv.NewReader(fh)
	r.ReuseRecord = false
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	var rows []any
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(header))
		for i, h := range header {
			if i < len(rec) {
				row[h] = rec[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func readJSONRows(path string) ([]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []any
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("JSON feeder must be an array: %w", err)
	}
	return rows, nil
}
