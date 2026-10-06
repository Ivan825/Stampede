package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
)

// findCompose looks for docker-compose.yml in the current directory and
// its parents, so `stampede up` works anywhere inside a clone.
func findCompose() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, "docker-compose.yml")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no docker-compose.yml found here or above; run this inside a clone of github.com/Ivan825/Stampede")
		}
		dir = parent
	}
}

func compose(cmd *cobra.Command, args ...string) error {
	file, err := findCompose()
	if err != nil {
		return err
	}
	c := exec.CommandContext(cmd.Context(), "docker", append([]string{"compose", "-f", file}, args...)...) //nolint:gosec // fixed program, user-chosen subcommand
	c.Stdout, c.Stderr, c.Stdin = cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin
	return c.Run()
}

func newUpCmd() *cobra.Command {
	var build bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the full local stack with Docker Compose (server, workers, database, ShopLab)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			args := []string{"up", "-d", "--wait"}
			if build {
				args = append(args, "--build")
			}
			if err := compose(cmd, args...); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\nStampede is running: open http://localhost:8080 (ShopLab demo target: http://localhost:8090)")
			return nil
		},
	}
	cmd.Flags().BoolVar(&build, "build", false, "build images from this clone instead of using existing ones")
	return cmd
}

func newDownCmd() *cobra.Command {
	var volumes bool
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the local Docker Compose stack",
		RunE: func(cmd *cobra.Command, _ []string) error {
			args := []string{"down"}
			if volumes {
				args = append(args, "--volumes")
			}
			return compose(cmd, args...)
		},
	}
	cmd.Flags().BoolVar(&volumes, "volumes", false, "also delete the database and master key (all runs are lost)")
	return cmd
}

func newDoctorCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the server, its database, workers and a target",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ok := true
			check := func(name string, err error, detail string) {
				if err != nil {
					ok = false
					fmt.Fprintf(out, "  ✗ %-22s %v\n", name, err)
					return
				}
				fmt.Fprintf(out, "  ✓ %-22s %s\n", name, detail)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
			defer cancel()
			cfg, _ := client.LoadConfig()
			if cfg.Server == "" {
				cfg.Server = "http://localhost:8080"
			}
			hc := &http.Client{Timeout: 5 * time.Second}
			get := func(path string) error {
				resp, err := hc.Get(cfg.Server + path)
				if err != nil {
					return err
				}
				resp.Body.Close()
				if resp.StatusCode != 200 {
					return fmt.Errorf("HTTP %d", resp.StatusCode)
				}
				return nil
			}
			check("server", get("/healthz"), cfg.Server)
			check("database", get("/readyz"), "reachable")
			if c, err := client.New(); err != nil {
				check("signed in", err, "")
			} else {
				var me gen.Me
				err := c.Do(ctx, "GET", "/me", nil, &me)
				check("signed in", err, me.Email+" ("+string(me.Role)+")")
				var ws []gen.Worker
				err = c.Do(ctx, "GET", "/workers", nil, &ws)
				idle := 0
				for _, w := range ws {
					if w.Status == gen.Idle {
						idle++
					}
				}
				check("workers", err, fmt.Sprintf("%d connected, %d idle", len(ws), idle))
			}
			if target != "" {
				start := time.Now()
				err := get2(hc, target)
				check("target", err, fmt.Sprintf("%s answered in %s", target, time.Since(start).Round(time.Millisecond)))
			}
			if !ok {
				return errors.New("some checks failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "also check that this URL answers")
	return cmd
}

func get2(hc *http.Client, url string) error {
	resp, err := hc.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
