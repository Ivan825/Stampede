// Command manager runs the Stampede operator: the StampedeCluster and
// StampedeRun controllers.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	stampedev1 "github.com/Ivan825/Stampede/deploy/operator/api/v1alpha1"
	"github.com/Ivan825/Stampede/deploy/operator/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(stampedev1.AddToScheme(scheme))
}

func main() {
	var metricsAddr, probeAddr string
	var leaderElect bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "address of the Prometheus metrics endpoint (0 disables it)")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "address of the /healthz and /readyz endpoints")
	flag.BoolVar(&leaderElect, "leader-elect", false, "enable leader election, for running more than one replica")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("setup")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "stampede-operator.stampede.dev",
	})
	if err != nil {
		log.Error(err, "unable to create the manager")
		os.Exit(1)
	}
	if err := (&controller.StampedeClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to set up the StampedeCluster controller")
		os.Exit(1)
	}
	if err := (&controller.StampedeRunReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("stampede-operator"),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to set up the StampedeRun controller")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "unable to add the health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to add the readiness check")
		os.Exit(1)
	}
	log.Info("starting the manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "the manager stopped")
		os.Exit(1)
	}
}
