// Package script runs a scenario's JavaScript steps with goja. A script
// sees the virtual user's variables as `vars` (and may set new ones), the
// environment as `env`, its feeder rows as `data`, and `vu` and
// `iteration`; `fail(message)` fails the iteration. Scripts have no
// network, file or timer access, and each run is bounded by Timeout.
package script

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// Timeout bounds one run of a script.
const Timeout = time.Second

// Program is a compiled script.
type Program struct {
	prog *goja.Program
	// Source is the script as written, for error messages.
	Source string
}

// Compile parses code once, ahead of the run. The body runs as a
// function in strict mode, so `return` ends it early.
func Compile(name, code string) (*Program, error) {
	p, err := goja.Compile(name, "(function () {\n"+code+"\n})()", true)
	if err != nil {
		return nil, errors.New(strings.TrimSpace(err.Error()))
	}
	return &Program{prog: p, Source: code}, nil
}

// Input is what a script sees.
type Input struct {
	Vars      map[string]any
	Env       map[string]string
	Data      map[string]any
	VU        int64
	Iteration int64
	// Log receives console.log output; nil discards it.
	Log func(string)
}

// Failure is a script calling fail(message).
type Failure struct{ Message string }

func (f *Failure) Error() string { return "fail: " + f.Message }

// Runner runs scripts for one virtual user. It is not safe for
// concurrent use; give each virtual user its own.
type Runner struct {
	rt *goja.Runtime
}

// Run executes p and returns the variables afterwards: in.Vars with the
// script's changes. in.Vars itself is not modified.
func (r *Runner) Run(ctx context.Context, p *Program, in Input) (map[string]any, error) {
	if r.rt == nil {
		r.rt = goja.New()
	}
	rt := r.rt
	vars := make(map[string]any, len(in.Vars)+2)
	for k, v := range in.Vars {
		vars[k] = v
	}
	env := make(map[string]any, len(in.Env))
	for k, v := range in.Env {
		env[k] = v
	}
	data := make(map[string]any, len(in.Data))
	for k, v := range in.Data {
		data[k] = v
	}
	set := func(name string, v any) {
		_ = rt.Set(name, v)
	}
	set("vars", vars)
	set("env", env)
	set("data", data)
	set("vu", in.VU)
	set("iteration", in.Iteration)
	set("fail", func(msg string) {
		panic(rt.NewGoError(&Failure{Message: msg}))
	})
	set("console", map[string]any{"log": func(args ...any) {
		if in.Log != nil {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = fmt.Sprint(a)
			}
			in.Log(strings.Join(parts, " "))
		}
	}})

	stop := context.AfterFunc(ctx, func() { rt.Interrupt("the run is stopping") })
	defer stop()
	timer := time.AfterFunc(Timeout, func() { rt.Interrupt(fmt.Sprintf("the script ran longer than %s", Timeout)) })
	defer timer.Stop()
	_, err := rt.RunProgram(p.prog)
	rt.ClearInterrupt()
	if err != nil {
		var f *Failure
		if errors.As(err, &f) {
			return nil, f
		}
		var ex *goja.Exception
		if errors.As(err, &ex) {
			if f, ok := ex.Value().Export().(*Failure); ok {
				return nil, f
			}
			return nil, errors.New(ex.Value().String())
		}
		var in *goja.InterruptedError
		if errors.As(err, &in) {
			return nil, fmt.Errorf("%v", in.Value())
		}
		return nil, err
	}
	return vars, nil
}
