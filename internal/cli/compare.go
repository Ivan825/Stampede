package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/report"
)

// ExitRegression is returned by compare when version B regressed.
const ExitRegression = 4

func newCompareCmd() *cobra.Command {
	var a, b []string
	var labelA, labelB, md, jsonOut string
	cmd := &cobra.Command{
		Use:   "compare --a <reports...> --b <reports...>",
		Short: "Compare two versions using repeated runs of each",
		Long: `Compare JSON reports (from stampede run --json) of version A with version B.

A change counts as a regression or improvement only when the bootstrap 95%
confidence interval of the difference excludes zero and the change is larger
than the noise measured between repeats of the same version. Use at least 3
runs per version; stampede run --repeat 3 produces them.

Exit codes: 0 no regression, 4 regression, 1 error.`,
		Example: `  stampede run checkout.yaml --repeat 3 --json reports/v1.json
  stampede compare --a reports/v1-1.json,reports/v1-2.json,reports/v1-3.json --b ... --md comment.md
  stampede compare base.json head.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 && len(a) == 0 && len(b) == 0 {
				a, b = []string{args[0]}, []string{args[1]}
			} else if len(args) > 0 {
				return fmt.Errorf("give two report files, or --a and --b lists")
			}
			if len(a) == 0 || len(b) == 0 {
				return fmt.Errorf("need reports for both versions (--a and --b)")
			}
			ra, err := readReports(a)
			if err != nil {
				return err
			}
			rb, err := readReports(b)
			if err != nil {
				return err
			}
			c := report.Compare(ra, rb, labelA, labelB)
			c.WriteText(cmd.OutOrStdout())
			if err := writeFile(cmd.OutOrStdout(), md, func(w io.Writer) error { c.WriteMarkdown(w); return nil }); err != nil {
				return err
			}
			if err := writeFile(cmd.OutOrStdout(), jsonOut, func(w io.Writer) error {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(c)
			}); err != nil {
				return err
			}
			if c.Verdict == report.CmpRegression {
				return &exitError{code: ExitRegression, msg: "regression detected"}
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&a, "a", nil, "reports of the baseline version")
	cmd.Flags().StringSliceVar(&b, "b", nil, "reports of the new version")
	cmd.Flags().StringVar(&labelA, "label-a", "A", "name for the baseline")
	cmd.Flags().StringVar(&labelB, "label-b", "B", "name for the new version")
	cmd.Flags().StringVar(&md, "md", "", "write a Markdown summary (- for stdout)")
	cmd.Flags().StringVar(&jsonOut, "json", "", "write the comparison as JSON (- for stdout)")
	return cmd
}

func readReports(paths []string) ([]*report.Report, error) {
	var out []*report.Report
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		r, err := report.ReadJSON(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func newReportCmd() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "report <report.json>",
		Short: "Render a saved JSON report as HTML, JUnit, Markdown or a summary",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rs, err := readReports(args)
			if err != nil {
				return err
			}
			r := rs[0]
			if f.html == "" && f.junit == "" && f.md == "" {
				r.WriteText(cmd.OutOrStdout())
				return nil
			}
			return writeOutputs(cmd.OutOrStdout(), r, f)
		},
	}
	cmd.Flags().StringVarP(&f.html, "out", "o", "", "write HTML to this file")
	cmd.Flags().StringVar(&f.junit, "junit", "", "write JUnit XML to this file")
	cmd.Flags().StringVar(&f.md, "md", "", "write Markdown to this file (- for stdout)")
	return cmd
}

// repeatPath turns report.json into report-2.json for the nth repeat.
func repeatPath(path string, n, total int) string {
	if total <= 1 || path == "" || path == "-" {
		return path
	}
	if i := strings.LastIndex(path, "."); i > strings.LastIndex(path, "/") {
		return fmt.Sprintf("%s-%d%s", path[:i], n, path[i:])
	}
	return fmt.Sprintf("%s-%d", path, n)
}
