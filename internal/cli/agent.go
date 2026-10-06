package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ivan825/Stampede/internal/agent"
)

type agentFlags struct {
	listen        string
	token         string
	proxies       []string
	maxDuration   time.Duration
	containers    []string
	dockerHost    string
	deployments   []string
	kubernetesAPI string
	auditFile     string
}

func newAgentCmd() *cobra.Command {
	f := &agentFlags{}
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Inject faults into dependencies during a load test",
		Long: `Run the fault-injection agent inside your environment, next to the
dependencies you want to break on purpose.

Each --proxy puts a TCP proxy in front of a dependency. Point your app at the
proxy instead of the dependency; while no fault is active it forwards traffic
untouched. A scenario's faults block (or the control API) then adds latency,
jitter or a bandwidth cap, resets connections, refuses new ones or stops
forwarding, for a set time.

With explicit permission the agent also pauses, stops, kills or restarts
Docker containers (--allow-container) and scales Kubernetes deployments
(--allow-deployment). Name each one; globs are allowed.

Every fault has a duration of at most --max-duration and is reverted when it
ends, when it is cleared (the run ends or the kill switch is used), and when
the agent stops. Every action is written to the audit log.

The control API needs the token from --token or STAMPEDE_AGENT_TOKEN.`,
		Example: `  STAMPEDE_AGENT_TOKEN=$(openssl rand -hex 16) stampede agent \
    --proxy db=:15432=postgres:5432 --proxy cache=:16379=redis:6379
  stampede agent --proxy api=:18080=payments.internal:80 --allow-container 'shop-*'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgent(cmd.Context(), cmd.ErrOrStderr(), f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.listen, "listen", envOr("STAMPEDE_AGENT_ADDR", ":7070"), "address of the control API")
	fl.StringVar(&f.token, "token", "", "control API token (default $STAMPEDE_AGENT_TOKEN)")
	fl.StringArrayVar(&f.proxies, "proxy", nil, "NAME=LISTEN=UPSTREAM, e.g. db=:15432=postgres:5432 (repeatable)")
	fl.DurationVar(&f.maxDuration, "max-duration", 30*time.Minute, "longest fault allowed")
	fl.StringArrayVar(&f.containers, "allow-container", nil, "Docker container (name or glob) the agent may pause, stop, kill or restart (repeatable)")
	fl.StringVar(&f.dockerHost, "docker-host", envOr("DOCKER_HOST", "unix:///var/run/docker.sock"), "Docker Engine API")
	fl.StringArrayVar(&f.deployments, "allow-deployment", nil, "Kubernetes deployment (namespace/name or glob) the agent may scale (repeatable)")
	fl.StringVar(&f.kubernetesAPI, "kubernetes-api", "", "Kubernetes API URL, e.g. a kubectl proxy (default: the in-cluster service account)")
	fl.StringVar(&f.auditFile, "audit-file", "", "also append the audit log (JSON lines) to this file")
	return cmd
}

func runAgent(ctx context.Context, stderr io.Writer, f *agentFlags) error {
	token := f.token
	if token == "" {
		token = os.Getenv("STAMPEDE_AGENT_TOKEN")
	}
	if len(token) < 16 {
		return errors.New("set a control API token of at least 16 characters with --token or STAMPEDE_AGENT_TOKEN")
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	auditOut := io.Writer(stderr)
	if f.auditFile != "" {
		af, err := os.OpenFile(f.auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the operator chooses the file
		if err != nil {
			return err
		}
		defer func() { _ = af.Close() }()
		auditOut = io.MultiWriter(stderr, af)
	}
	audit := slog.New(slog.NewJSONHandler(auditOut, nil)).With("audit", true)

	cfg := agent.Config{MaxDuration: f.maxDuration, Audit: audit, Logger: log}
	if len(f.proxies) == 0 && len(f.containers) == 0 && len(f.deployments) == 0 {
		return errors.New("nothing to do: add at least one --proxy, --allow-container or --allow-deployment")
	}
	seen := map[string]bool{}
	for _, spec := range f.proxies {
		parts := strings.SplitN(spec, "=", 3)
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return fmt.Errorf("--proxy %q: use NAME=LISTEN=UPSTREAM, e.g. db=:15432=postgres:5432", spec)
		}
		if seen[parts[0]] {
			return fmt.Errorf("--proxy %q: the name %s is used twice", spec, parts[0])
		}
		seen[parts[0]] = true
		p := agent.NewProxy(parts[0], parts[1], parts[2], log)
		if err := p.Start(ctx); err != nil {
			return fmt.Errorf("proxy %s: %w", parts[0], err)
		}
		log.Info("proxy ready", "name", p.Name, "listen", p.Addr(), "upstream", p.Upstream)
		cfg.Proxies = append(cfg.Proxies, p)
	}
	if len(f.containers) > 0 {
		d, err := agent.NewDocker(f.dockerHost, f.containers)
		if err != nil {
			return err
		}
		cfg.Docker = d
		log.Info("container actions allowed", "containers", strings.Join(f.containers, ", "))
	}
	if len(f.deployments) > 0 {
		k, err := agent.NewKubernetes(f.kubernetesAPI, "", f.deployments)
		if err != nil {
			return err
		}
		cfg.Kubernetes = k
		log.Info("deployment scaling allowed", "deployments", strings.Join(f.deployments, ", "))
	}
	a := agent.New(cfg)

	srv := &http.Server{Addr: f.listen, Handler: a.Handler(token), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("agent listening", "addr", f.listen, "max-duration", f.maxDuration)

	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	// Leave nothing broken behind.
	rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if rerr := a.RevertAll(rctx, "", "agent stopping"); rerr != nil {
		log.Error("could not revert every fault", "error", rerr)
	}
	_ = srv.Shutdown(rctx)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
