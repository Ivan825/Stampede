package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

func newProjectsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project"},
		Short:   "List and manage projects, their settings and per-project roles",
		Long: `A project holds targets, scenarios, runs, schedules and secrets. Commands
that work in a project take --project (a name, slug or id); without it
they use the default project set with stampede projects use (or
STAMPEDE_PROJECT), or the only project when there is just one.`,
	}
	cmd.AddCommand(newProjectsListCmd(), newProjectsCreateCmd(), newProjectsShowCmd(), newProjectsUpdateCmd(), newProjectsDeleteCmd(),
		newProjectsUseCmd(), newProjectSettingsCmd(), newProjectRolesCmd())
	return cmd
}

func newProjectsListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			ps, err := c.Projects(cmd.Context())
			if err != nil {
				return err
			}
			return show(cmd, asJSON, ps, func(w io.Writer) error {
				if len(ps) == 0 {
					_, err := fmt.Fprintln(w, "No projects. Create one with stampede projects create <name>.")
					return err
				}
				var rows [][]string
				for _, p := range ps {
					name := p.Name
					if isDefault(c, p) {
						name += " (default)"
					}
					role := "-"
					if p.Role != nil {
						role = string(*p.Role)
					}
					rows = append(rows, []string{name, p.Slug, role, or(p.Description, ""), stamp(&p.CreatedAt)})
				}
				return table(w, "NAME\tSLUG\tYOUR ROLE\tDESCRIPTION\tCREATED", rows)
			})
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newProjectsCreateCmd() *cobra.Command {
	var desc string
	var use, asJSON bool
	cmd := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a project",
		Example: `  stampede projects create Shop --description "The storefront and its API" --use`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			body := map[string]string{"name": args[0]}
			if desc != "" {
				body["description"] = desc
			}
			var p gen.Project
			if err := c.Do(cmd.Context(), "POST", "/projects", body, &p); err != nil {
				return err
			}
			if use {
				if err := setDefaultProject(p.Slug); err != nil {
					return err
				}
			}
			return show(cmd, asJSON, p, func(w io.Writer) error {
				fmt.Fprintf(w, "Created project %s (slug %s).\n", p.Name, p.Slug)
				if use {
					fmt.Fprintln(w, "It is now the default project.")
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&desc, "description", "", "what the project tests")
	cmd.Flags().BoolVar(&use, "use", false, "make it the default project (stampede projects use)")
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newProjectsShowCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show [project]",
		Short: "Show a project with its targets, scenarios and schedules",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client.New()
			if err != nil {
				return err
			}
			p, err := c.FindProject(ctx, firstArg(args))
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), p)
			}
			pid := p.Id.String()
			var ts []gen.Target
			var ss []gen.Scenario
			var sch []gen.Schedule
			var set gen.ProjectSettings
			for path, v := range map[string]any{"/targets": &ts, "/scenarios": &ss, "/schedules": &sch, "/settings": &set} {
				if err := c.Do(ctx, "GET", "/projects/"+pid+path, nil, v); err != nil {
					return err
				}
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%s (slug %s, id %s)\n", p.Name, p.Slug, p.Id)
			if d := or(p.Description, ""); d != "" {
				fmt.Fprintf(w, "  %s\n", d)
			}
			if p.Role != nil {
				fmt.Fprintf(w, "Your role:  %s\n", *p.Role)
			}
			fmt.Fprintf(w, "Created:    %s\n", p.CreatedAt.Local().Format(time.RFC1123))
			fmt.Fprintf(w, "Caps:       %s\n", capsText(set.Caps))
			fmt.Fprintf(w, "Dry run:    %s\n", map[bool]string{true: "required before every run", false: "not required"}[set.RequireDryRun])
			var names []string
			for _, t := range ts {
				names = append(names, t.Name+" ("+t.BaseURL+")")
			}
			fmt.Fprintf(w, "Targets:    %d  %s\n", len(ts), strings.Join(names, ", "))
			names = nil
			for _, s := range ss {
				names = append(names, fmt.Sprintf("%s v%d", s.Name, s.LatestVersion.Version))
			}
			fmt.Fprintf(w, "Scenarios:  %d  %s\n", len(ss), strings.Join(names, ", "))
			names = nil
			for _, s := range sch {
				names = append(names, s.Name)
			}
			fmt.Fprintf(w, "Schedules:  %d  %s\n", len(sch), strings.Join(names, ", "))
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func newProjectsUpdateCmd() *cobra.Command {
	var name, desc string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "update <project>",
		Short: "Rename a project or change its description",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("name") && !cmd.Flags().Changed("description") {
				return errors.New("nothing to change: give --name or --description")
			}
			c, err := client.New()
			if err != nil {
				return err
			}
			p, err := c.FindProject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]string{"name": p.Name, "description": or(p.Description, "")}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if cmd.Flags().Changed("description") {
				body["description"] = desc
			}
			wasDefault := isDefault(c, p)
			if err := c.Do(cmd.Context(), "PATCH", "/projects/"+p.Id.String(), body, &p); err != nil {
				return err
			}
			if wasDefault {
				// A new name gives a new slug.
				if err := setDefaultProject(p.Slug); err != nil {
					return err
				}
			}
			return show(cmd, asJSON, p, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Updated project %s (slug %s).\n", p.Name, p.Slug)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&desc, "description", "", "new description")
	jsonFlag(cmd, &asJSON)
	return cmd
}

func newProjectsDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <project>",
		Short: "Delete a project with everything in it",
		Long: `Delete a project together with its targets, scenarios, runs, reports,
schedules and secrets. This cannot be undone, so it needs --yes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			p, err := c.FindProject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("deleting %s also deletes its targets, scenarios, runs, schedules and secrets; add --yes to confirm", p.Name)
			}
			if err := c.Do(cmd.Context(), "DELETE", "/projects/"+p.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted project %s.\n", p.Name)
			if isDefault(c, p) {
				if err := setDefaultProject(""); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the deletion")
	return cmd
}

func newProjectsUseCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "use [project]",
		Short: "Set the default project for commands given no --project",
		Long: `Store a default project in the config file, used by every command that
takes --project when the flag is left out. STAMPEDE_PROJECT overrides it.
--clear removes it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear {
				if err := setDefaultProject(""); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "No default project.")
				return nil
			}
			if len(args) != 1 {
				return errors.New("name a project, or use --clear")
			}
			c, err := client.New()
			if err != nil {
				return err
			}
			c.Project = ""
			p, err := c.FindProject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := setDefaultProject(p.Slug); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Default project: %s (slug %s).\n", p.Name, p.Slug)
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the default project")
	return cmd
}

// isDefault reports whether p is the stored default project.
func isDefault(c *client.Client, p gen.Project) bool {
	return c.Project != "" && (c.Project == p.Slug || c.Project == p.Id.String() || strings.EqualFold(c.Project, p.Name))
}

func setDefaultProject(slug string) error {
	cfg, err := client.ReadConfigFile()
	if err != nil {
		return err
	}
	cfg.Project = slug
	_, err = client.SaveConfig(cfg)
	return err
}

// capsFlags set run caps; 0 removes a cap.
type capsFlags struct {
	rate float64
	vus  int
	dur  time.Duration
}

func (f *capsFlags) register(fl *pflag.FlagSet) {
	fl.Float64Var(&f.rate, "max-rate", 0, "cap on the arrival rate, iterations per second (0 removes it)")
	fl.IntVar(&f.vus, "max-vus", 0, "cap on virtual users (0 removes it)")
	fl.DurationVar(&f.dur, "max-duration", 0, "cap on a run's duration, e.g. 30m (0 removes it)")
}

func (f *capsFlags) changed(fl *pflag.FlagSet) bool {
	return fl.Changed("max-rate") || fl.Changed("max-vus") || fl.Changed("max-duration")
}

// apply sets the caps whose flags were given.
func (f *capsFlags) apply(fl *pflag.FlagSet, c *gen.Caps) error {
	if fl.Changed("max-rate") {
		if f.rate < 0 {
			return errors.New("--max-rate cannot be negative")
		}
		c.MaxRate = nil
		if f.rate > 0 {
			c.MaxRate = &f.rate
		}
	}
	if fl.Changed("max-vus") {
		if f.vus < 0 {
			return errors.New("--max-vus cannot be negative")
		}
		c.MaxVUs = nil
		if f.vus > 0 {
			c.MaxVUs = &f.vus
		}
	}
	if fl.Changed("max-duration") {
		if f.dur < 0 {
			return errors.New("--max-duration cannot be negative")
		}
		c.MaxDurationSeconds = nil
		if s := int(f.dur.Seconds()); s > 0 {
			c.MaxDurationSeconds = &s
		}
	}
	return nil
}

// capsText describes caps, e.g. "rate ≤ 100/s, VUs ≤ 500, duration ≤ 30m0s".
func capsText(c gen.Caps) string {
	var parts []string
	if c.MaxRate != nil {
		parts = append(parts, "rate ≤ "+strconv.FormatFloat(*c.MaxRate, 'f', -1, 64)+"/s")
	}
	if c.MaxVUs != nil {
		parts = append(parts, fmt.Sprintf("VUs ≤ %d", *c.MaxVUs))
	}
	if c.MaxDurationSeconds != nil {
		parts = append(parts, fmt.Sprintf("duration ≤ %s", time.Duration(*c.MaxDurationSeconds)*time.Second))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func newProjectSettingsCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Show or change a project's caps and dry-run gate",
		Long: `A project's caps bound every run in it, together with the server's, the
organisation's and the target's. With the dry-run gate on, every run first
runs each journey once with one user and fails before any load when a
journey fails. Changing them needs the admin role in the project.`,
	}
	cmd.PersistentFlags().StringVar(&project, "project", "", "project name, slug or id (default: the default or only project)")

	var asJSON bool
	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show a project's caps and dry-run gate",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, err := projectClient(cmd.Context(), project)
			if err != nil {
				return err
			}
			var s gen.ProjectSettings
			if err := c.Do(cmd.Context(), "GET", "/projects/"+pid+"/settings", nil, &s); err != nil {
				return err
			}
			return show(cmd, asJSON, s, func(w io.Writer) error { return writeSettings(w, s) })
		},
	}
	jsonFlag(showCmd, &asJSON)

	var caps capsFlags
	var dryRun, setJSON bool
	setCmd := &cobra.Command{
		Use:     "set",
		Short:   "Change a project's caps or dry-run gate (admin)",
		Example: `  stampede projects settings set --project shop --max-rate 200 --max-duration 1h --require-dry-run`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fl := cmd.Flags()
			if !caps.changed(fl) && !fl.Changed("require-dry-run") {
				return errors.New("nothing to change: give --max-rate, --max-vus, --max-duration or --require-dry-run")
			}
			c, pid, err := projectClient(cmd.Context(), project)
			if err != nil {
				return err
			}
			var s gen.ProjectSettings
			if err := c.Do(cmd.Context(), "GET", "/projects/"+pid+"/settings", nil, &s); err != nil {
				return err
			}
			if err := caps.apply(fl, &s.Caps); err != nil {
				return err
			}
			if fl.Changed("require-dry-run") {
				s.RequireDryRun = dryRun
			}
			if err := c.Do(cmd.Context(), "PUT", "/projects/"+pid+"/settings", s, &s); err != nil {
				return err
			}
			return show(cmd, setJSON, s, func(w io.Writer) error { return writeSettings(w, s) })
		},
	}
	caps.register(setCmd.Flags())
	setCmd.Flags().BoolVar(&dryRun, "require-dry-run", false, "dry-run every journey before each run's load (--require-dry-run=false turns it off)")
	jsonFlag(setCmd, &setJSON)
	cmd.AddCommand(showCmd, setCmd)
	return cmd
}

func writeSettings(w io.Writer, s gen.ProjectSettings) error {
	fmt.Fprintf(w, "Caps:     %s\n", capsText(s.Caps))
	_, err := fmt.Fprintf(w, "Dry run:  %s\n", map[bool]string{true: "required before every run", false: "not required"}[s.RequireDryRun])
	return err
}

func newProjectRolesCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "roles",
		Short: "Give members a different role in one project",
		Long: `A member's organisation role applies in every project unless an override
sets another role, higher or lower, for one project. Owners are owners
everywhere. Changing overrides needs the admin role in the project.`,
	}
	cmd.PersistentFlags().StringVar(&project, "project", "", "project name, slug or id (default: the default or only project)")

	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List a project's role overrides",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, err := projectClient(cmd.Context(), project)
			if err != nil {
				return err
			}
			var rs []gen.ProjectRole
			if err := c.Do(cmd.Context(), "GET", "/projects/"+pid+"/roles", nil, &rs); err != nil {
				return err
			}
			return show(cmd, asJSON, rs, func(w io.Writer) error {
				if len(rs) == 0 {
					_, err := fmt.Fprintln(w, "No overrides; organisation roles apply.")
					return err
				}
				var rows [][]string
				for _, r := range rs {
					org := "-"
					if r.OrgRole != nil {
						org = string(*r.OrgRole)
					}
					rows = append(rows, []string{r.Email, or(r.Name, ""), string(r.Role), org})
				}
				return table(w, "EMAIL\tNAME\tPROJECT ROLE\tORGANISATION ROLE", rows)
			})
		},
	}
	jsonFlag(list, &asJSON)

	set := &cobra.Command{
		Use:     "set <user> <role>",
		Short:   "Give a member a role in this project (admin)",
		Example: `  stampede projects roles set pat@acme.test viewer --project shop`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, pid, err := projectClient(cmd.Context(), project)
			if err != nil {
				return err
			}
			u, err := c.FindUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var r gen.ProjectRole
			if err := c.Do(cmd.Context(), "PUT", "/projects/"+pid+"/roles/"+u.Id.String(), map[string]string{"role": args[1]}, &r); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is %s in this project (%s in the organisation).\n", r.Email, r.Role, u.Role)
			return nil
		},
	}
	remove := &cobra.Command{
		Use:   "remove <user>",
		Short: "Remove an override, so the organisation role applies again (admin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, pid, err := projectClient(cmd.Context(), project)
			if err != nil {
				return err
			}
			u, err := c.FindUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "DELETE", "/projects/"+pid+"/roles/"+u.Id.String(), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s has their organisation role (%s) in this project again.\n", u.Email, u.Role)
			return nil
		},
	}
	cmd.AddCommand(list, set, remove)
	return cmd
}

func newCapsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "caps",
		Short: "Show or set the organisation's hard caps on every run",
		Long: `Every run in the organisation must fit within these caps, as well as the
server's, its project's and its target's (stampede settings limits shows
them all). Setting them needs the admin role and is audited.`,
	}
	var asJSON bool
	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show the organisation's caps",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.New()
			if err != nil {
				return err
			}
			var caps gen.Caps
			if err := c.Do(cmd.Context(), "GET", "/organisation/caps", nil, &caps); err != nil {
				return err
			}
			return show(cmd, asJSON, caps, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Organisation caps: %s\n", capsText(caps))
				return err
			})
		},
	}
	jsonFlag(showCmd, &asJSON)

	var f capsFlags
	var clear, setJSON bool
	setCmd := &cobra.Command{
		Use:   "set",
		Short: "Change the organisation's caps (admin)",
		Example: `  stampede caps set --max-rate 1000 --max-vus 5000 --max-duration 2h
  stampede caps set --max-vus 0      # remove the VU cap
  stampede caps set --clear          # remove every cap`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fl := cmd.Flags()
			if !clear && !f.changed(fl) {
				return errors.New("nothing to change: give --max-rate, --max-vus, --max-duration or --clear")
			}
			if clear && f.changed(fl) {
				return errors.New("--clear removes every cap; leave out the other flags")
			}
			c, err := client.New()
			if err != nil {
				return err
			}
			var caps gen.Caps
			if !clear {
				if err := c.Do(cmd.Context(), "GET", "/organisation/caps", nil, &caps); err != nil {
					return err
				}
				if err := f.apply(fl, &caps); err != nil {
					return err
				}
			}
			if err := c.Do(cmd.Context(), "PUT", "/organisation/caps", caps, &caps); err != nil {
				return err
			}
			return show(cmd, setJSON, caps, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Organisation caps: %s\n", capsText(caps))
				return err
			})
		},
	}
	f.register(setCmd.Flags())
	setCmd.Flags().BoolVar(&clear, "clear", false, "remove every organisation cap")
	jsonFlag(setCmd, &setJSON)
	cmd.AddCommand(showCmd, setCmd)
	return cmd
}
