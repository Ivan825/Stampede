// Package feed provides the test-data sources that are more than a file:
// rows of generated fake data and rows read from a database query.
package feed

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	mrand "math/rand/v2"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Kinds lists the generator kinds, for help text and validation messages.
var Kinds = []string{
	"name", "firstName", "lastName", "email", "username", "password", "phone", "uuid", "seq",
	"company", "street", "city", "country", "zip", "word", "sentence", "paragraph",
	"bool", "date", "datetime", "ipv4", "int(min,max)", "float(min,max)", "pick(a|b|c)", "text(size)",
}

// MaxText bounds text(size): a generated payload is held in memory once
// per field and reused.
const MaxText = 64 << 20

// Generator makes a fresh row of fake data each time it is asked. Values
// that must not collide (email, username, seq) include the worker's index,
// so rows never repeat within a run, even across workers.
type Generator struct {
	fields []field
	index  uint64
	count  uint64
	n      atomic.Uint64
}

type field struct {
	name string
	gen  func(g *Generator, n uint64, row map[string]any) any
}

var kindRe = regexp.MustCompile(`^([a-zA-Z0-9]+)(?:\((.*)\))?$`)

// NewGenerator compiles spec, a map of field name to kind, for worker
// index of count.
func NewGenerator(spec map[string]string, index, count int) (*Generator, error) {
	if len(spec) == 0 {
		return nil, fmt.Errorf("generate: no fields")
	}
	if count < 1 {
		count = 1
	}
	g := &Generator{index: uint64(index), count: uint64(count)}
	names := make([]string, 0, len(spec))
	for name := range spec {
		names = append(names, name)
	}
	// Names come first so email and username can use them.
	sort.Slice(names, func(i, j int) bool {
		pi, pj := priority(spec[names[i]]), priority(spec[names[j]])
		if pi != pj {
			return pi < pj
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		fn, err := Compile(spec[name])
		if err != nil {
			return nil, fmt.Errorf("generate.%s: %w", name, err)
		}
		g.fields = append(g.fields, field{name: name, gen: fn})
	}
	return g, nil
}

func priority(kind string) int {
	switch kind {
	case "name", "firstName", "lastName":
		return 0
	}
	return 1
}

// Row returns a new row.
func (g *Generator) Row() map[string]any {
	n := g.n.Add(1) - 1
	row := make(map[string]any, len(g.fields)+2)
	for _, f := range g.fields {
		row[f.name] = f.gen(g, n, row)
	}
	// Helpers used by email and username; not part of the row.
	delete(row, "\x00first")
	delete(row, "\x00last")
	return row
}

// unique returns a number no other row of this run gets.
func (g *Generator) unique(n uint64) uint64 { return n*g.count + g.index }

// Compile parses one kind.
func Compile(kind string) (func(g *Generator, n uint64, row map[string]any) any, error) {
	m := kindRe.FindStringSubmatch(strings.TrimSpace(kind))
	if m == nil {
		return nil, fmt.Errorf("unknown kind %q; use one of %s", kind, strings.Join(Kinds, ", "))
	}
	name, arg := m[1], m[2]
	hasArg := strings.Contains(kind, "(")
	noArg := func(fn func(g *Generator, n uint64, row map[string]any) any) (func(*Generator, uint64, map[string]any) any, error) {
		if hasArg {
			return nil, fmt.Errorf("%s takes no arguments", name)
		}
		return fn, nil
	}
	switch name {
	case "name":
		return noArg(func(_ *Generator, _ uint64, row map[string]any) any {
			f, l := names(row)
			return f + " " + l
		})
	case "firstName":
		return noArg(func(_ *Generator, _ uint64, row map[string]any) any { f, _ := names(row); return f })
	case "lastName":
		return noArg(func(_ *Generator, _ uint64, row map[string]any) any { _, l := names(row); return l })
	case "email":
		return noArg(func(g *Generator, n uint64, row map[string]any) any {
			f, l := names(row)
			// example.test is reserved (RFC 2606): mail never leaves.
			return fmt.Sprintf("%s.%s.%d@example.test", strings.ToLower(f), strings.ToLower(l), g.unique(n))
		})
	case "username":
		return noArg(func(g *Generator, n uint64, row map[string]any) any {
			f, l := names(row)
			return fmt.Sprintf("%s%s%d", strings.ToLower(f), strings.ToLower(l[:1]), g.unique(n))
		})
	case "password":
		return noArg(func(*Generator, uint64, map[string]any) any { return randomString(16) })
	case "phone":
		// 555-01xx numbers are reserved for fiction in North America.
		return noArg(func(*Generator, uint64, map[string]any) any {
			return fmt.Sprintf("+1-%03d-555-01%02d", 200+mrand.IntN(800), mrand.IntN(100))
		})
	case "uuid":
		return noArg(func(*Generator, uint64, map[string]any) any { return uuid.NewString() })
	case "seq":
		return noArg(func(g *Generator, n uint64, _ map[string]any) any { return int64(g.unique(n)) })
	case "company":
		return noArg(func(*Generator, uint64, map[string]any) any {
			return pick(companyWords) + " " + pick(companySuffixes)
		})
	case "street":
		return noArg(func(*Generator, uint64, map[string]any) any {
			return strconv.Itoa(1+mrand.IntN(999)) + " " + pick(streetNames) + " " + pick(streetTypes)
		})
	case "city":
		return noArg(func(*Generator, uint64, map[string]any) any { return pick(cities) })
	case "country":
		return noArg(func(*Generator, uint64, map[string]any) any { return pick(countries) })
	case "zip":
		return noArg(func(*Generator, uint64, map[string]any) any { return fmt.Sprintf("%05d", mrand.IntN(100000)) })
	case "word":
		return noArg(func(*Generator, uint64, map[string]any) any { return pick(words) })
	case "sentence":
		return noArg(func(*Generator, uint64, map[string]any) any { return sentence() })
	case "paragraph":
		return noArg(func(*Generator, uint64, map[string]any) any {
			s := make([]string, 3+mrand.IntN(4))
			for i := range s {
				s[i] = sentence()
			}
			return strings.Join(s, " ")
		})
	case "bool":
		return noArg(func(*Generator, uint64, map[string]any) any { return mrand.IntN(2) == 1 })
	case "date", "datetime":
		layout := time.DateOnly
		if name == "datetime" {
			layout = time.RFC3339
		}
		return noArg(func(*Generator, uint64, map[string]any) any {
			// Within the last five years.
			ago := time.Duration(mrand.Int64N(int64(5 * 365 * 24 * time.Hour)))
			return time.Now().UTC().Add(-ago).Truncate(time.Second).Format(layout)
		})
	case "ipv4":
		// Documentation ranges (RFC 5737): never a real host.
		nets := []string{"192.0.2", "198.51.100", "203.0.113"}
		return noArg(func(*Generator, uint64, map[string]any) any {
			return fmt.Sprintf("%s.%d", nets[mrand.IntN(len(nets))], 1+mrand.IntN(254))
		})
	case "int":
		lo, hi, err := bounds(arg)
		if err != nil {
			return nil, fmt.Errorf("int(min,max): %w", err)
		}
		a, b := int64(lo), int64(hi)
		if float64(a) != lo || float64(b) != hi {
			return nil, fmt.Errorf("int(min,max): bounds must be whole numbers")
		}
		return func(*Generator, uint64, map[string]any) any { return a + mrand.Int64N(b-a+1) }, nil
	case "float":
		lo, hi, err := bounds(arg)
		if err != nil {
			return nil, fmt.Errorf("float(min,max): %w", err)
		}
		return func(*Generator, uint64, map[string]any) any {
			return float64(int64((lo+mrand.Float64()*(hi-lo))*100)) / 100
		}, nil
	case "pick":
		opts := strings.Split(arg, "|")
		for i := range opts {
			opts[i] = strings.TrimSpace(opts[i])
		}
		if !hasArg || len(opts) == 0 || opts[0] == "" {
			return nil, fmt.Errorf("pick(a|b|c) needs at least one value")
		}
		return func(*Generator, uint64, map[string]any) any { return opts[mrand.IntN(len(opts))] }, nil
	case "text":
		size, err := parseSize(arg)
		if err != nil {
			return nil, fmt.Errorf("text(size): %w", err)
		}
		// Random bytes, base64-encoded so they survive JSON and forms, made
		// once: payload tests care about size, not content.
		raw := make([]byte, size*3/4+3)
		_, _ = rand.Read(raw)
		text := base64.RawURLEncoding.EncodeToString(raw)[:size]
		return func(*Generator, uint64, map[string]any) any { return text }, nil
	}
	return nil, fmt.Errorf("unknown kind %q; use one of %s", kind, strings.Join(Kinds, ", "))
}

// names returns the row's first and last name, choosing them once so
// name, email and username agree.
func names(row map[string]any) (string, string) {
	f, ok := row["\x00first"].(string)
	if !ok {
		f = pick(firstNames)
		row["\x00first"] = f
	}
	l, ok := row["\x00last"].(string)
	if !ok {
		l = pick(lastNames)
		row["\x00last"] = l
	}
	return f, l
}

func bounds(arg string) (float64, float64, error) {
	a, b, ok := strings.Cut(arg, ",")
	if !ok {
		return 0, 0, fmt.Errorf("want two numbers, got %q", arg)
	}
	lo, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	hi, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("want two numbers, got %q", arg)
	}
	if hi < lo {
		return 0, 0, fmt.Errorf("max %v is below min %v", hi, lo)
	}
	return lo, hi, nil
}

// parseSize reads 512, 10KB, 1.5MB (powers of 1024).
func parseSize(s string) (int, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := 1.0
	for _, u := range []struct {
		suffix string
		m      float64
	}{{"KB", 1 << 10}, {"MB", 1 << 20}, {"GB", 1 << 30}, {"K", 1 << 10}, {"M", 1 << 20}, {"B", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.m
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("want a size such as 512, 64KB or 5MB")
	}
	n := int(v * mult)
	if n > MaxText {
		return 0, fmt.Errorf("%d bytes is above the %d MB limit", n, MaxText>>20)
	}
	return n, nil
}

func pick(list []string) string { return list[mrand.IntN(len(list))] }

func sentence() string {
	w := make([]string, 6+mrand.IntN(8))
	for i := range w {
		w[i] = pick(words)
	}
	s := strings.Join(w, " ")
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

const letters = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[mrand.IntN(len(letters))]
	}
	return string(b)
}
