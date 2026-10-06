package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	stampedev1 "github.com/Ivan825/Stampede/deploy/operator/api/v1alpha1"
	"github.com/Ivan825/Stampede/deploy/operator/internal/stampede"
)

// The controller tests run against a real kube-apiserver and etcd from
// envtest (no kubelet, so pods never start). `make test` downloads them and
// sets KUBEBUILDER_ASSETS; without it these tests are skipped.

var (
	k8s       client.Client
	testCtx   context.Context
	newClient = func(baseURL, token string) *stampede.Client { return stampede.New(baseURL, token) }
)

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		// Unit tests in this package still run; envtest tests skip.
		os.Exit(m.Run())
	}
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(os.Stderr)))
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		panic(err)
	}
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = stampedev1.AddToScheme(scheme)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}})
	if err != nil {
		panic(err)
	}
	if err := (&StampedeClusterReconciler{Client: mgr.GetClient(), Scheme: scheme}).SetupWithManager(mgr); err != nil {
		panic(err)
	}
	if err := (&StampedeRunReconciler{
		Client: mgr.GetClient(), Scheme: scheme,
		NewClient:    func(b, t string) *stampede.Client { return newClient(b, t) },
		PollInterval: 200 * time.Millisecond,
	}).SetupWithManager(mgr); err != nil {
		panic(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	testCtx = ctx
	go func() {
		if err := mgr.Start(ctx); err != nil {
			panic(err)
		}
	}()
	k8s, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(err)
	}
	code := m.Run()
	cancel()
	_ = env.Stop()
	os.Exit(code)
}

func needEnvtest(t *testing.T) {
	t.Helper()
	if k8s == nil {
		t.Skip("set KUBEBUILDER_ASSETS (make test) to run the envtest controller tests")
	}
}

// eventually polls cond until it returns true or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}
