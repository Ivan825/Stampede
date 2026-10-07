package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/report"
)

// clusterOnly are the run flags that only mean something with --cluster.
var clusterOnly = []string{"project", "target", "workers", "note", "detach", "region"}

// localOnly are the run flags a server run cannot honour.
var localOnly = []string{"iterations", "repeat", "pause", "allow-host", "verbose"}

// unused rejects --cluster's flags on an in-process run.
func (c *clusterFlags) unused(fl *pflag.FlagSet) error {
	for _, name := range clusterOnly {
		if fl.Changed(name) {
			return fmt.Errorf("--%s applies only with --cluster", name)
		}
	}
	return nil
}

// runCluster runs a scenario file on the signed-in server's workers: the
// same path as stampede start --file, with run's outputs.
func runCluster(cmd *cobra.Command, path string, f *runFlags) error {
	fl := cmd.Flags()
	for _, name := range localOnly {
		if fl.Changed(name) {
			return fmt.Errorf("--%s applies only to in-process runs, not with --cluster", name)
		}
	}
	narrator, err := f.ai.provider()
	if err != nil {
		return err
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	target := f.cluster.target
	if target == "" {
		// The server runs against a saved target; --base-url picks it.
		target = f.baseURL
	}
	rf := &remoteRunFlags{
		project: f.cluster.project, file: path, target: target, note: f.cluster.note,
		shape: f.shape, rate: f.rate, duration: f.duration, vus: f.vus,
		workers: f.cluster.workers, env: f.env, detach: f.cluster.detach, regions: f.cluster.regions,
	}
	ctx := cmd.Context()
	run, err := createRemoteRun(ctx, cmd.ErrOrStderr(), c, rf, "pushed by stampede run --cluster")
	if err != nil {
		return err
	}
	if rf.detach {
		fmt.Fprintln(cmd.OutOrStdout(), run.Id)
		return nil
	}
	stdout := cmd.OutOrStdout()
	return followRunWith(ctx, cmd, c, run.Id.String(), f.quiet, func(rep *report.Report) error {
		narrate(ctx, cmd.ErrOrStderr(), rep, narrator)
		if f.json != "-" && f.md != "-" && f.csv != "-" && f.timelineCSV != "-" {
			writeSummary(stdout, rep)
		}
		return writeOutputs(ctx, stdout, rep, f)
	})
}
