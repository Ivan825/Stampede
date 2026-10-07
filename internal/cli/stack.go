package cli

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/api/gen"
	"github.com/Ivan825/Stampede/internal/client"
	"github.com/Ivan825/Stampede/internal/version"
)

// standaloneStack is the stack `stampede up` runs outside a clone: the
// released images of this version, without the ShopLab demo.
//
//go:embed stack.yml
var standaloneStack []byte

// findCompose looks for the repository's docker-compose.yml in the current
// directory and its parents, so `stampede up` inside a clone runs the full
// stack with ShopLab. Elsewhere it writes the standalone stack to the
// user's config directory; clone reports which one it chose.
func findCompose() (file string, clone bool, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false, err
	}
	for {
		p := filepath.Join(dir, "docker-compose.yml")
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte("ghcr.io/ivan825/stampede")) { //nolint:gosec // a file the user's clone holds
			return p, true, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if !version.Released() {
		return "", false, fmt.Errorf("stampede %s is not a release, so it has no published images; run `stampede up --build` inside a clone of github.com/Ivan825/Stampede", version.Version)
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", false, err
	}
	p := filepath.Join(cfg, "stampede", "stack", "docker-compose.yml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", false, err
	}
	return p, false, os.WriteFile(p, standaloneStack, 0o600)
}

func compose(cmd *cobra.Command, args ...string) (clone bool, err error) {
	file, clone, err := findCompose()
	if err != nil {
		return false, err
	}
	c := exec.CommandContext(cmd.Context(), "docker", append([]string{"compose", "-f", file}, args...)...) //nolint:gosec // fixed program, user-chosen subcommand
	c.Stdout, c.Stderr, c.Stdin = cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin
	if !clone {
		c.Env = append(os.Environ(), "STAMPEDE_VERSION="+strings.TrimPrefix(version.Version, "v"))
	}
	return clone, c.Run()
}

func newUpCmd() *cobra.Command {
	var build bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the full stack with Docker Compose: server and web UI, workers, database",
		Long: `Start the full stack with Docker Compose. Inside a clone of the repository
it runs the repository's docker-compose.yml, including the ShopLab demo
app. Anywhere else it runs the released images of this version (server and
web UI on :8080, two workers, TimescaleDB), with no clone needed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			args := []string{"up", "-d", "--wait"}
			if build {
				args = append(args, "--build")
			}
			clone, err := compose(cmd, args...)
			if err != nil {
				return err
			}
			if clone {
				fmt.Fprintln(cmd.OutOrStdout(), "\nStampede is running: open http://localhost:8080 (ShopLab demo target: http://localhost:8090)")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "\nStampede is running: open http://localhost:8080\nAn app on this machine is reachable from runs as http://host.docker.internal:<port>.")
			}
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
			_, err := compose(cmd, args...)
			return err
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
