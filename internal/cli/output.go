package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// jsonFlag adds --json, which prints the API's response for scripts.
func jsonFlag(cmd *cobra.Command, p *bool) {
	cmd.Flags().BoolVar(p, "json", false, "print JSON for scripting")
}

// show prints v as indented JSON with --json, or calls human otherwise.
func show(cmd *cobra.Command, asJSON bool, v any, human func(w io.Writer) error) error {
	if asJSON {
		return writeJSON(cmd.OutOrStdout(), v)
	}
	return human(cmd.OutOrStdout())
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table writes tab-separated rows under a header, aligned.
func table(w io.Writer, header string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	return tw.Flush()
}

// stamp formats a time for tables; "-" when unset.
func stamp(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Local().Format("Jan 2 15:04")
}

func or(s *string, def string) string {
	if s == nil || *s == "" {
		return def
	}
	return *s
}

// prompter reads answers from the command's input: hidden in a terminal,
// line by line from a pipe.
type prompter struct {
	cmd *cobra.Command
	in  *bufio.Reader
}

func newPrompter(cmd *cobra.Command) *prompter {
	return &prompter{cmd: cmd, in: bufio.NewReader(cmd.InOrStdin())}
}

func (p *prompter) terminal() (int, bool) {
	if f, ok := p.cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return int(f.Fd()), true
	}
	return 0, false
}

// line asks for a value shown as it is typed.
func (p *prompter) line(label string) string {
	fmt.Fprint(p.cmd.ErrOrStderr(), label)
	s, _ := p.in.ReadString('\n')
	return strings.TrimSpace(s)
}

// secret returns the value of the environment variable env when it is
// set, else asks for it without echo (or reads a line from a pipe). The
// value is never printed.
func (p *prompter) secret(label, env string) (string, error) {
	if env != "" {
		if v := os.Getenv(env); v != "" {
			return v, nil
		}
	}
	errw := p.cmd.ErrOrStderr()
	fmt.Fprint(errw, label)
	if fd, ok := p.terminal(); ok {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(errw)
		return string(b), err
	}
	s, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// newSecret asks for a new password twice in a terminal.
func (p *prompter) newSecret(label, env string) (string, error) {
	pw, err := p.secret(label, env)
	if err != nil || os.Getenv(env) != "" {
		return pw, err
	}
	if _, ok := p.terminal(); ok {
		again, err := p.secret("Again: ", "")
		if err != nil {
			return "", err
		}
		if again != pw {
			return "", errors.New("the passwords do not match")
		}
	}
	return pw, nil
}

// readValue reads a secret value: from the environment variable fromEnv
// when given, else all of a piped stdin, else a hidden prompt.
func readValue(cmd *cobra.Command, label, fromEnv string) (string, error) {
	if fromEnv != "" {
		v, ok := os.LookupEnv(fromEnv)
		if !ok {
			return "", fmt.Errorf("environment variable %s is not set", fromEnv)
		}
		return v, nil
	}
	p := newPrompter(cmd)
	if _, ok := p.terminal(); ok {
		return p.secret(label, "")
	}
	b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}
