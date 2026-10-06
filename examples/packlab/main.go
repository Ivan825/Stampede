// Command packlab runs one of the in-memory reference apps that the
// product packs are tested against:
//
//	packlab -product llm-apps -addr :8092
//
// Each product has planted bottlenecks; -fix switches them off by name
// (or "all"). See README.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/apilab"
	"github.com/Ivan825/Stampede/examples/packlab/authlab"
	"github.com/Ivan825/Stampede/examples/packlab/chatlab"
	"github.com/Ivan825/Stampede/examples/packlab/gamelab"
	"github.com/Ivan825/Stampede/examples/packlab/iotlab"
	"github.com/Ivan825/Stampede/examples/packlab/labkit"
	"github.com/Ivan825/Stampede/examples/packlab/llmlab"
	"github.com/Ivan825/Stampede/examples/packlab/saaslab"
	"github.com/Ivan825/Stampede/examples/packlab/ticketlab"
)

// product is one reference app.
type product struct {
	// pack is the product pack the app is the reference for.
	pack string
	// addr is the default listen address.
	addr string
	new  func(labkit.Config) http.Handler
	// open starts an app that also listens on another protocol (a broker,
	// a cache, a UDP server), by default on listen. Such an app has no new.
	open   func(labkit.Config) (*labkit.App, error)
	listen string
	// plugins are the protocol plugins the pack's scenarios use.
	plugins []string
	// postgres is set for an app that seeds a PostgreSQL database.
	postgres bool
}

// start builds the app.
func (p product) start(cfg labkit.Config) (*labkit.App, error) {
	if p.open == nil {
		return &labkit.App{Handler: p.new(cfg), Close: func() {}}, nil
	}
	return p.open(cfg)
}

var products = map[string]product{
	"saas":        {pack: "saas", addr: ":8091", new: saaslab.New},
	"llm-apps":    {pack: "llm-apps", addr: ":8092", new: llmlab.New},
	"chat":        {pack: "chat", addr: ":8093", new: chatlab.New},
	"ticketing":   {pack: "ticketing", addr: ":8094", new: ticketlab.New},
	"identity":    {pack: "identity", addr: ":8095", new: authlab.New},
	"public-apis": {pack: "public-apis", addr: ":8096", new: apilab.New},
	"iot":         {pack: "iot", addr: ":8110", open: iotlab.Open, listen: ":8111", plugins: []string{"mqtt"}},
	"gaming":      {pack: "gaming", addr: ":8116", open: gamelab.Open, listen: ":8117", plugins: []string{"udp"}},
}

func names() []string {
	var out []string
	for n := range products {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func main() {
	name := flag.String("product", "", "product to serve: "+strings.Join(names(), ", "))
	addr := flag.String("addr", "", "listen address (default: the product's own port)")
	listen := flag.String("listen", "", "address of the app's broker, cache or UDP server, for apps with one (default: the product's own port)")
	pg := flag.String("postgres", os.Getenv("PACKLAB_POSTGRES_DSN"), "PostgreSQL connection string, for apps that seed a database")
	fix := flag.String("fix", os.Getenv("PACKLAB_FIX"), `planted bottlenecks to switch off, comma-separated, or "all"`)
	fast := flag.Bool("fast", false, "shorten deliberate waits (token pacing, admission ticks) for quick tests")
	flag.Parse()
	p, ok := products[*name]
	if !ok {
		fmt.Fprintf(os.Stderr, "packlab: -product must be one of %s\n", strings.Join(names(), ", "))
		os.Exit(2)
	}
	if *addr == "" {
		*addr = p.addr
	}
	if *listen == "" {
		*listen = p.listen
	}
	if p.postgres && *pg == "" {
		fmt.Fprintf(os.Stderr, "packlab: %s needs a PostgreSQL database: -postgres or PACKLAB_POSTGRES_DSN\n", *name)
		os.Exit(2)
	}
	app, err := p.start(labkit.Config{Fixes: labkit.ParseFixes(*fix), Fast: *fast, Listen: *listen, Postgres: *pg})
	if err != nil {
		slog.Error("packlab could not start", "product", *name, "err", err)
		os.Exit(1)
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           app.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("packlab serving", "product", *name, "addr", *addr, "fixes", *fix, "env", app.Env)
	err = srv.ListenAndServe()
	app.Close()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("packlab stopped", "err", err)
		os.Exit(1)
	}
}
