package scenario

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/interpreter"
)

// Built-in variable roots available to every expression.
var builtinRoots = []string{"env", "secret", "data", "vars", "vu", "iter", "replay"}

// Response variables available to check expressions.
var responseRoots = []string{"status", "headers", "body", "json", "latencyMs"}

// IsReserved reports whether name is a built-in expression variable and so
// cannot be used as an extracted variable name.
func IsReserved(name string) bool {
	for _, r := range builtinRoots {
		if r == name {
			return true
		}
	}
	for _, r := range responseRoots {
		if r == name {
			return true
		}
	}
	return false
}

var (
	baseEnvOnce sync.Once
	baseEnv     *cel.Env
	baseEnvErr  error
)

func celBase() (*cel.Env, error) {
	baseEnvOnce.Do(func() {
		opts := []cel.EnvOption{}
		for _, r := range builtinRoots {
			opts = append(opts, cel.Variable(r, cel.DynType))
		}
		opts = append(opts, celFunctions()...)
		baseEnv, baseEnvErr = cel.NewEnv(opts...)
	})
	return baseEnv, baseEnvErr
}

func celFunctions() []cel.EnvOption {
	str := func(f func(string) string) cel.OverloadOpt {
		return cel.UnaryBinding(func(v ref.Val) ref.Val {
			s, ok := v.(types.String)
			if !ok {
				return types.NewErr("expected string")
			}
			return types.String(f(string(s)))
		})
	}
	return []cel.EnvOption{
		cel.Function("rand",
			cel.Overload("rand_int_int", []*cel.Type{cel.IntType, cel.IntType}, cel.IntType,
				cel.BinaryBinding(func(a, b ref.Val) ref.Val {
					lo, hi := int64(a.(types.Int)), int64(b.(types.Int))
					if hi < lo {
						lo, hi = hi, lo
					}
					return types.Int(lo + rand.Int64N(hi-lo+1))
				}))),
		cel.Function("randFloat",
			cel.Overload("randFloat", nil, cel.DoubleType,
				cel.FunctionBinding(func(...ref.Val) ref.Val { return types.Double(rand.Float64()) }))),
		cel.Function("randString",
			cel.Overload("randString_int", []*cel.Type{cel.IntType}, cel.StringType,
				cel.UnaryBinding(func(n ref.Val) ref.Val { return types.String(randString(int(n.(types.Int)))) }))),
		cel.Function("randEmail",
			cel.Overload("randEmail", nil, cel.StringType,
				cel.FunctionBinding(func(...ref.Val) ref.Val {
					return types.String(randString(10) + "@example.test")
				}))),
		cel.Function("pick",
			cel.Overload("pick_list", []*cel.Type{cel.ListType(cel.DynType)}, cel.DynType,
				cel.UnaryBinding(func(v ref.Val) ref.Val {
					l, ok := v.(traits.Lister)
					if !ok {
						return types.NewErr("pick expects a list")
					}
					n := int64(l.Size().(types.Int))
					if n == 0 {
						return types.NullValue
					}
					return l.Get(types.Int(rand.Int64N(n)))
				}))),
		cel.Function("uuid",
			cel.Overload("uuid", nil, cel.StringType,
				cel.FunctionBinding(func(...ref.Val) ref.Val { return types.String(uuidV4()) }))),
		cel.Function("now",
			cel.Overload("now", nil, cel.IntType,
				cel.FunctionBinding(func(...ref.Val) ref.Val { return types.Int(time.Now().Unix()) }))),
		cel.Function("nowMs",
			cel.Overload("nowMs", nil, cel.IntType,
				cel.FunctionBinding(func(...ref.Val) ref.Val { return types.Int(time.Now().UnixMilli()) }))),
		cel.Function("base64", cel.Overload("base64_string", []*cel.Type{cel.StringType}, cel.StringType,
			str(func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }))),
		cel.Function("urlencode", cel.Overload("urlencode_string", []*cel.Type{cel.StringType}, cel.StringType,
			str(url.QueryEscape))),
		cel.Function("sha256", cel.Overload("sha256_string", []*cel.Type{cel.StringType}, cel.StringType,
			str(func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }))),
		cel.Function("toJSON", cel.Overload("toJSON_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				b, err := json.Marshal(ToNative(v))
				if err != nil {
					return types.NewErr("toJSON: %v", err)
				}
				return types.String(b)
			}))),
	}
}

const alphanum = "abcdefghijklmnopqrstuvwxyz0123456789"

func randString(n int) string {
	if n <= 0 {
		return ""
	}
	if n > 4096 {
		n = 4096
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = alphanum[rand.IntN(len(alphanum))]
	}
	return string(b)
}

func uuidV4() string {
	var b [16]byte
	for i := 0; i < 16; i += 8 {
		v := rand.Uint64()
		for j := 0; j < 8; j++ {
			b[i+j] = byte(v >> (8 * j))
		}
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// Scope lists the variable names visible to an expression, beyond the
// built-in roots.
type Scope struct {
	names map[string]bool
	env   *cel.Env
}

// NewScope declares vars as top-level dynamic variables.
func NewScope(vars ...string) (*Scope, error) {
	s := &Scope{names: map[string]bool{}}
	return s, s.rebuild(vars, false)
}

// NewResponseScope is a scope that also sees response variables, used by
// check expressions.
func NewResponseScope(vars ...string) (*Scope, error) {
	s := &Scope{names: map[string]bool{}}
	return s, s.rebuild(vars, true)
}

func (s *Scope) rebuild(vars []string, response bool) error {
	base, err := celBase()
	if err != nil {
		return err
	}
	sort.Strings(vars)
	var opts []cel.EnvOption
	for _, v := range vars {
		if s.names[v] {
			continue
		}
		s.names[v] = true
		opts = append(opts, cel.Variable(v, cel.DynType))
	}
	if response {
		opts = append(opts,
			cel.Variable("status", cel.IntType),
			cel.Variable("headers", cel.MapType(cel.StringType, cel.StringType)),
			cel.Variable("body", cel.StringType),
			cel.Variable("json", cel.DynType),
			cel.Variable("latencyMs", cel.DoubleType),
		)
	}
	s.env, err = base.Extend(opts...)
	return err
}

// Names returns the declared variable names, sorted.
func (s *Scope) Names() []string {
	out := make([]string, 0, len(s.names))
	for n := range s.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Expr is a compiled expression.
type Expr struct {
	src  string
	prog cel.Program
}

// CompileExpr compiles a bare expression (no ${}).
func (s *Scope) CompileExpr(src string) (*Expr, error) {
	ast, iss := s.env.Compile(src)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("expression %q: %s", src, strings.TrimSpace(iss.Err().Error()))
	}
	prg, err := s.env.Program(ast, cel.EvalOptions(cel.OptOptimize))
	if err != nil {
		return nil, fmt.Errorf("expression %q: %w", src, err)
	}
	return &Expr{src: src, prog: prg}, nil
}

// String returns the expression source.
func (e *Expr) String() string { return e.src }

// Eval evaluates the expression against vars.
func (e *Expr) Eval(vars interpreter.Activation) (any, error) {
	out, _, err := e.prog.Eval(vars)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.src, err)
	}
	return ToNative(out), nil
}

// EvalBool evaluates a condition.
func (e *Expr) EvalBool(vars interpreter.Activation) (bool, error) {
	v, err := e.Eval(vars)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s: expected true or false, got %T", e.src, v)
	}
	return b, nil
}

// Template is a string with embedded ${expressions}.
type Template struct {
	src   string
	lits  []string // len(lits) == len(exprs)+1
	exprs []*Expr
}

// CompileTemplate parses and compiles every ${...} in src. "$${" escapes a
// literal "${".
func (s *Scope) CompileTemplate(src string) (*Template, error) {
	lits, srcs, err := splitTemplate(src)
	if err != nil {
		return nil, err
	}
	t := &Template{src: src, lits: lits}
	for _, es := range srcs {
		e, err := s.CompileExpr(es)
		if err != nil {
			return nil, err
		}
		t.exprs = append(t.exprs, e)
	}
	return t, nil
}

// IsLiteral reports whether the template has no expressions.
func (t *Template) IsLiteral() bool { return len(t.exprs) == 0 }

// String returns the template source.
func (t *Template) String() string { return t.src }

// Render evaluates the template to a string.
func (t *Template) Render(vars interpreter.Activation) (string, error) {
	if len(t.exprs) == 0 {
		return t.lits[0], nil
	}
	var b strings.Builder
	for i, e := range t.exprs {
		b.WriteString(t.lits[i])
		v, err := e.Eval(vars)
		if err != nil {
			return "", err
		}
		b.WriteString(Stringify(v))
	}
	b.WriteString(t.lits[len(t.lits)-1])
	return b.String(), nil
}

// Value evaluates the template keeping the type when the whole template is
// a single expression (so "${count}" stays a number in a JSON body).
func (t *Template) Value(vars interpreter.Activation) (any, error) {
	if len(t.exprs) == 1 && t.lits[0] == "" && t.lits[1] == "" {
		return t.exprs[0].Eval(vars)
	}
	return t.Render(vars)
}

// Stringify formats an evaluated value for interpolation into text.
func Stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case []byte:
		return string(x)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

// splitTemplate splits src into literal parts and expression sources.
func splitTemplate(src string) (lits, exprs []string, err error) {
	var cur strings.Builder
	i := 0
	for i < len(src) {
		if strings.HasPrefix(src[i:], "$${") {
			cur.WriteString("${")
			i += 3
			continue
		}
		if !strings.HasPrefix(src[i:], "${") {
			cur.WriteByte(src[i])
			i++
			continue
		}
		end, ok := matchBrace(src, i+2)
		if !ok {
			return nil, nil, fmt.Errorf("unclosed ${ in %q", src)
		}
		expr := strings.TrimSpace(src[i+2 : end])
		if expr == "" {
			return nil, nil, fmt.Errorf("empty ${} in %q", src)
		}
		lits = append(lits, cur.String())
		cur.Reset()
		exprs = append(exprs, expr)
		i = end + 1
	}
	lits = append(lits, cur.String())
	return lits, exprs, nil
}

// matchBrace returns the index of the "}" closing an expression that starts
// at from, skipping nested braces and quoted strings.
func matchBrace(s string, from int) (int, bool) {
	depth := 0
	var quote byte
	for i := from; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i, true
			}
			depth--
		}
	}
	return 0, false
}

// ToNative converts a CEL value into plain Go values (string, int64,
// float64, bool, nil, []any, map[string]any).
func ToNative(v ref.Val) any {
	switch x := v.(type) {
	case types.String:
		return string(x)
	case types.Int:
		return int64(x)
	case types.Uint:
		return uint64(x)
	case types.Double:
		return float64(x)
	case types.Bool:
		return bool(x)
	case types.Null:
		return nil
	case types.Bytes:
		return []byte(x)
	case types.Duration:
		return x.String()
	case types.Timestamp:
		return x.Format(time.RFC3339Nano)
	case traits.Mapper:
		out := map[string]any{}
		it := x.Iterator()
		for it.HasNext() == types.True {
			k := it.Next()
			out[Stringify(ToNative(k))] = ToNative(x.Get(k))
		}
		return out
	case traits.Lister:
		n := int(x.Size().(types.Int))
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i] = ToNative(x.Get(types.Int(i)))
		}
		return out
	default:
		return v.Value()
	}
}
