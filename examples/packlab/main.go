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
	"github.com/Ivan825/Stampede/examples/packlab/banklab"
	"github.com/Ivan825/Stampede/examples/packlab/chatlab"
	"github.com/Ivan825/Stampede/examples/packlab/examlab"
	"github.com/Ivan825/Stampede/examples/packlab/govlab"
	"github.com/Ivan825/Stampede/examples/packlab/labkit"
	"github.com/Ivan825/Stampede/examples/packlab/llmlab"
	"github.com/Ivan825/Stampede/examples/packlab/newslab"
	"github.com/Ivan825/Stampede/examples/packlab/saaslab"
	"github.com/Ivan825/Stampede/examples/packlab/sociallab"
	"github.com/Ivan825/Stampede/examples/packlab/streamlab"
	"github.com/Ivan825/Stampede/examples/packlab/ticketlab"
)

// product is one reference app.
type product struct {
	// pack is the product pack the app is the reference for.
	pack string
	// addr is the default listen address.
	addr string
	new  func(labkit.Config) http.Handler
}

var products = map[string]product{
	"saas":        {pack: "saas", addr: ":8091", new: saaslab.New},
	"llm-apps":    {pack: "llm-apps", addr: ":8092", new: llmlab.New},
	"chat":        {pack: "chat", addr: ":8093", new: chatlab.New},
	"ticketing":   {pack: "ticketing", addr: ":8094", new: ticketlab.New},
	"identity":    {pack: "identity", addr: ":8095", new: authlab.New},
	"public-apis": {pack: "public-apis", addr: ":8096", new: apilab.New},
	"fintech":     {pack: "fintech", addr: ":8097", new: banklab.New},
	"social":      {pack: "social", addr: ":8098", new: sociallab.New},
	"content":     {pack: "content", addr: ":8099", new: newslab.New},
	"streaming":   {pack: "streaming", addr: ":8100", new: streamlab.New},
	"edtech":      {pack: "edtech", addr: ":8101", new: examlab.New},
	"government":  {pack: "government", addr: ":8102", new: govlab.New},
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
	srv := &http.Server{
		Addr:              *addr,
		Handler:           p.new(labkit.Config{Fixes: labkit.ParseFixes(*fix), Fast: *fast}),
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
	slog.Info("packlab serving", "product", *name, "addr", *addr, "fixes", *fix)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("packlab stopped", "err", err)
		os.Exit(1)
	}
}
