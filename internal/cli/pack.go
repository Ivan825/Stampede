package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/pack"
	"github.com/Ivan825/Stampede/internal/safety"
	"github.com/Ivan825/Stampede/internal/scenario"
)

func newPackCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pack", Short: "List, install, test and create product packs"}
	cmd.AddCommand(newPackCreateCmd())
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List product packs and whether each is shipped or planned",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cat, err := pack.Catalog()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "PACK\tSTATUS\tPRODUCTS\tDRIVERS")
			for _, e := range cat {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.Status, e.Title, strings.Join(e.Drivers, ", "))
			}
			return tw.Flush()
		},
	})
	var dir string
	var force bool
	install := &cobra.Command{
		Use:   "install <pack>",
		Short: "Copy a pack's journeys, stresses, targets and data into a folder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := pack.Load(args[0])
			if err != nil {
				return err
			}
			files, err := p.Install(dir, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s into %s (%d files written)\n", p.Name, filepath.Join(dir, p.Name), len(files))
			return nil
		},
	}
	install.Flags().StringVar(&dir, "dir", "stampede", "destination folder")
	install.Flags().BoolVar(&force, "force", false, "overwrite files that already exist")
	cmd.AddCommand(install)

	var target string
	var env []string
	test := &cobra.Command{
		Use:   "test <pack or folder>",
		Short: "Dry-run every journey of a pack once against a target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := pack.Load(args[0])
			if err != nil {
				return err
			}
			return testPack(cmd.Context(), cmd.OutOrStdout(), p, target, env)
		},
	}
	test.Flags().StringVar(&target, "target", "", "target base URL (sets TARGET_URL)")
	test.Flags().StringArrayVarP(&env, "env", "e", nil, "KEY=VALUE (repeatable)")
	cmd.AddCommand(test)
	return cmd
}

// packEnv is the environment a pack's scenarios run with: TARGET_URL from
// the target, then KEY=VALUE pairs.
func packEnv(target string, envKV []string) map[string]string {
	env := map[string]string{"TARGET_URL": strings.TrimRight(target, "/")}
	for _, kv := range envKV {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return env
}

// missingVariables lists the variables a pack declares that env does not
// set, such as the broker address of a pack whose scenarios use a plugin.
func missingVariables(p *pack.Pack, env map[string]string) []string {
	var out []string
	for k := range p.Variables {
		if env[k] == "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// testPack dry-runs every journey of a pack. It installs the pack into a
// temporary folder first so relative data paths resolve as they would
// for a user.
func testPack(ctx context.Context, out io.Writer, p *pack.Pack, target string, envKV []string) error {
	if target == "" {
		return errors.New("--target is required")
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid --target %q", target)
	}
	env := packEnv(target, envKV)
	if missing := missingVariables(p, env); len(missing) > 0 {
		return fmt.Errorf("the %s pack also needs %s (add -e %s=<value>)", p.Name, strings.Join(missing, ", "), missing[0])
	}
	tmp, err := os.MkdirTemp("", "stampede-pack-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if _, err := p.Install(tmp, true); err != nil {
		return err
	}
	files, err := p.Files()
	if err != nil {
		return err
	}
	policy := safety.NewHostPolicy(u.Hostname(), nil)
	failed := 0
	for _, f := range files {
		s, err := scenario.LoadFile(filepath.Join(tmp, p.Name, filepath.FromSlash(f)))
		if err != nil {
			return err
		}
		fctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		res, err := pack.DryRun(fctx, f, s, env, policy.Allow)
		cancel()
		if err != nil {
			return err
		}
		for _, r := range res {
			if r.OK {
				fmt.Fprintf(out, "  ✓ %-40s %s\n", f, r.Journey)
			} else {
				failed++
				fmt.Fprintf(out, "  ✗ %-40s %s: %s\n", f, r.Journey, r.Problem)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d journey(s) failed their dry run", failed)
	}
	fmt.Fprintf(out, "Every journey in %s works against %s.\n", p.Name, target)
	return nil
}

// initOptions are stampede init's settings.
type initOptions struct {
	target, dir string
	env         []string
	// yes skips the confirmation read from in.
	yes bool
}

func newInitCmd() *cobra.Command {
	o := initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Detect what kind of product a target is and set up the matching pack",
		Long: `init probes the target (its OpenAPI document at common paths, its OpenID
Connect discovery document, its home page and response headers), scores
the shipped product packs against what it finds, asks you to confirm,
installs the pack into ./stampede and dry-runs its journeys once against
the target. A pack that needs more than the target's URL (the address of
a broker, for example) says what to pass with -e.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), o)
		},
	}
	cmd.Flags().StringVar(&o.target, "target", "", "base URL of the system to test")
	cmd.Flags().StringVar(&o.dir, "dir", "stampede", "folder to install the pack into")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().StringArrayVarP(&o.env, "env", "e", nil, "KEY=VALUE for the pack's other variables, such as a broker address (repeatable)")
	return cmd
}

// runInit is stampede init, also used by the terminal console's /init.
func runInit(ctx context.Context, in io.Reader, out io.Writer, o initOptions) error {
	target, dir, env := o.target, o.dir, o.env
	if dir == "" {
		dir = "stampede"
	}
	if target == "" {
		return errors.New("--target is required, e.g. stampede init --target http://localhost:8090")
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid --target %q", target)
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ev, err := pack.Probe(pctx, u, nil)
	if err != nil {
		return err
	}
	if ev.OpenAPI {
		fmt.Fprintf(out, "Found an OpenAPI document with %d paths.\n", len(ev.Paths))
	}
	if ev.OIDC {
		fmt.Fprintln(out, "Found an OpenID Connect discovery document.")
	}
	shipped, err := pack.Shipped()
	if err != nil {
		return err
	}
	matches := pack.Score(ev, shipped)
	if len(matches) == 0 {
		fmt.Fprintln(out, "No shipped pack matches this target. Run `stampede pack list` to see the packs, or write a scenario by hand (see README).")
		return nil
	}
	best := matches[0]
	fmt.Fprintf(out, "This looks like %s (%s).\n", best.Pack.Title, strings.Join(firstN(best.Reasons, 4), ", "))
	if !o.yes {
		fmt.Fprintf(out, "Set up the %s pack in ./%s? [Y/n] ", best.Pack.Name, dir)
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
			fmt.Fprintln(out, "Nothing installed.")
			return nil
		}
	}
	files, err := best.Pack.Install(dir, false)
	if err != nil {
		return err
	}
	root := filepath.Join(dir, best.Pack.Name)
	var flags string
	for _, kv := range env {
		flags += " -e " + kv
	}
	if missing := missingVariables(best.Pack, packEnv(target, env)); len(missing) > 0 {
		fmt.Fprintf(out, "Installed %d files into %s. The pack also needs:\n", len(files), root)
		for _, k := range missing {
			fmt.Fprintf(out, "  %s: %s\n", k, best.Pack.Variables[k].Description)
		}
		fmt.Fprintf(out, "\nNext: stampede pack test %s --target %s%s -e %s=...\n", root, target, flags, missing[0])
		return nil
	}
	fmt.Fprintf(out, "Installed %d files into %s. Checking each journey once against %s:\n", len(files), root, target)
	if err := testPack(ctx, out, best.Pack, target, env); err != nil {
		fmt.Fprintf(out, "\nSome journeys need adjusting to this API (paths or JSON fields). Edit the files in %s and run stampede pack test %s --target %s%s\n",
			root, root, target, flags)
		return err
	}
	next := "journeys"
	if files, err := best.Pack.Files(); err == nil && len(files) > 0 {
		next = files[0]
		for _, f := range files {
			if strings.HasSuffix(f, "-mix.yaml") {
				next = f
				break
			}
		}
	}
	fmt.Fprintf(out, "\nNext: stampede run %s -e TARGET_URL=%s%s\n", filepath.Join(root, filepath.FromSlash(next)), target, flags)
	return nil
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
