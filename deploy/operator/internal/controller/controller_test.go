package controller

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	stampedev1 "github.com/Ivan825/Stampede/deploy/operator/api/v1alpha1"
	"github.com/Ivan825/Stampede/deploy/operator/internal/stampede/stampedetest"
)

const wait = 20 * time.Second

func namespace(t *testing.T, name string) string {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8s.Create(testCtx, ns); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestClusterCreatesServerWorkersAndKeys(t *testing.T) {
	needEnvtest(t)
	ns := namespace(t, "cluster")
	sc := &stampedev1.StampedeCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "lab", Namespace: ns},
		Spec: stampedev1.StampedeClusterSpec{
			Image: "ghcr.io/ivan825/stampede:test",
			Database: stampedev1.DatabaseSpec{URLSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "db"}, Key: "url",
			}},
			Workers: stampedev1.WorkersSpec{Replicas: ptr.To[int32](2), Region: "eu", Labels: map[string]string{"pool": "spot"}, MaxVUs: 500},
		},
	}
	if err := k8s.Create(testCtx, sc); err != nil {
		t.Fatal(err)
	}

	var keys corev1.Secret
	eventually(t, wait, "the keys Secret", func() bool {
		return k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "lab-stampede-keys"}, &keys) == nil
	})
	raw, err := base64.StdEncoding.DecodeString(string(keys.Data["master-key"]))
	if err != nil || len(raw) != 32 {
		t.Fatalf("master key %q is not base64 of 32 bytes", keys.Data["master-key"])
	}
	if len(keys.Data["join-token"]) == 0 {
		t.Fatal("no join token")
	}
	if len(keys.OwnerReferences) != 0 {
		t.Fatal("the keys Secret must outlive the StampedeCluster")
	}

	var server, workers appsv1.Deployment
	eventually(t, wait, "the server and worker Deployments", func() bool {
		return k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "lab-server"}, &server) == nil &&
			k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "lab-worker"}, &workers) == nil
	})
	if *server.Spec.Replicas != 1 || server.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("server: replicas %d strategy %s", *server.Spec.Replicas, server.Spec.Strategy.Type)
	}
	c := server.Spec.Template.Spec.Containers[0]
	if c.Image != "ghcr.io/ivan825/stampede:test" || !*c.SecurityContext.ReadOnlyRootFilesystem || *c.SecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("server container: %+v", c)
	}
	envs := map[string]*corev1.EnvVarSource{}
	for _, e := range c.Env {
		envs[e.Name] = e.ValueFrom
	}
	if envs["STAMPEDE_DATABASE_URL"].SecretKeyRef.Name != "db" || envs["STAMPEDE_MASTER_KEY"].SecretKeyRef.Name != "lab-stampede-keys" {
		t.Fatalf("server env: %+v", c.Env)
	}
	if len(server.OwnerReferences) != 1 || server.OwnerReferences[0].Name != "lab" {
		t.Fatalf("server owner refs: %+v", server.OwnerReferences)
	}
	args := workers.Spec.Template.Spec.Containers[0].Args
	for _, want := range []string{"worker", "--server=lab:8081", "--region=eu", "--label=pool=spot", "--max-vus=500", "--mtls"} {
		if !slices.Contains(args, want) {
			t.Fatalf("worker args %v lack %s", args, want)
		}
	}
	if *workers.Spec.Replicas != 2 {
		t.Fatalf("workers = %d, want 2", *workers.Spec.Replicas)
	}
	var svc corev1.Service
	if err := k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "lab"}, &svc); err != nil {
		t.Fatal(err)
	}
	if len(svc.Spec.Ports) != 2 || svc.Spec.Ports[0].Port != 8080 || svc.Spec.Ports[1].Port != 8081 {
		t.Fatalf("service ports: %+v", svc.Spec.Ports)
	}
	eventually(t, wait, "status", func() bool {
		_ = k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "lab"}, sc)
		return sc.Status.URL == "http://lab.cluster.svc:8080" && sc.Status.WorkerDeployment == "lab-worker" && sc.Status.Phase == "Pending"
	})

	// A spec change rolls through, the keys never change.
	sc.Spec.Workers.Replicas = ptr.To[int32](3)
	if err := k8s.Update(testCtx, sc); err != nil {
		t.Fatal(err)
	}
	eventually(t, wait, "3 workers", func() bool {
		_ = k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "lab-worker"}, &workers)
		return *workers.Spec.Replicas == 3
	})
	var keys2 corev1.Secret
	_ = k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "lab-stampede-keys"}, &keys2)
	if string(keys2.Data["master-key"]) != string(keys.Data["master-key"]) {
		t.Fatal("the master key changed")
	}

	// While a run has scaled the workers, the cluster leaves the count alone.
	workers.Annotations = map[string]string{AnnotationScaledForRun: "cluster/some-run", AnnotationReplicasBefore: "3"}
	workers.Spec.Replicas = ptr.To[int32](6)
	if err := k8s.Update(testCtx, &workers); err != nil {
		t.Fatal(err)
	}
	_ = k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "lab"}, sc)
	sc.Spec.Workers.Replicas = ptr.To[int32](4)
	if err := k8s.Update(testCtx, sc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	_ = k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "lab-worker"}, &workers)
	if *workers.Spec.Replicas != 6 {
		t.Fatalf("replicas = %d; the cluster overrode a run's scaling", *workers.Spec.Replicas)
	}
}

const inlineScenario = `apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: checkout
journeys:
  - name: home
    steps:
      - get: /
`

func workerPool(t *testing.T, ns string, replicas int32) {
	t.Helper()
	lbl := map[string]string{"app": "pool"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: lbl},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: lbl},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "w", Image: "ghcr.io/ivan825/stampede:test"}}},
			},
		},
	}
	if err := k8s.Create(testCtx, dep); err != nil {
		t.Fatal(err)
	}
}

func tokenSecret(t *testing.T, ns string) {
	t.Helper()
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: ns}, StringData: map[string]string{"token": "stp_test"}}
	if err := k8s.Create(testCtx, s); err != nil {
		t.Fatal(err)
	}
}

func newRun(ns, name, url string) *stampedev1.StampedeRun {
	return &stampedev1.StampedeRun{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: stampedev1.StampedeRunSpec{
			Server:            stampedev1.RunServer{URL: url},
			APITokenSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "api"}, Key: "token"},
			Project:           "shop",
			Scenario:          stampedev1.RunScenario{Inline: inlineScenario},
			Target:            stampedev1.RunTarget{URL: "http://shop.shop.svc:8090"},
		},
	}
}

func getRun(t *testing.T, ns, name string) *stampedev1.StampedeRun {
	t.Helper()
	var r stampedev1.StampedeRun
	if err := k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: name}, &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestRunScalesWorkersRunsAndRestores(t *testing.T) {
	needEnvtest(t)
	ns := namespace(t, "run-ok")
	f := stampedetest.New("stp_test")
	defer f.Close()
	workerPool(t, ns, 1)
	tokenSecret(t, ns)
	f.SetWorkers(1)

	run := newRun(ns, "release-42", f.URL)
	run.Spec.Workers = 3
	run.Spec.ScaleWorkers = &stampedev1.ScaleWorkers{Deployment: "pool", Replicas: 3, WaitTimeoutSeconds: 60}
	run.Spec.Overrides = &stampedev1.RunOverrides{Mode: "rate", Rate: "50/s", Duration: "1m"}
	run.Spec.Env = map[string]string{"TARGET": "x"}
	run.Spec.Note = "release 42"
	if err := k8s.Create(testCtx, run); err != nil {
		t.Fatal(err)
	}

	var dep appsv1.Deployment
	eventually(t, wait, "the pool scaled to 3 and phase Scaling", func() bool {
		_ = k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "pool"}, &dep)
		r := getRun(t, ns, "release-42")
		return *dep.Spec.Replicas == 3 && r.Status.Phase == stampedev1.RunScaling
	})
	if dep.Annotations[AnnotationScaledForRun] != "run-ok/release-42" || dep.Annotations[AnnotationReplicasBefore] != "1" {
		t.Fatalf("annotations: %v", dep.Annotations)
	}
	if f.RunCreates() != 0 {
		t.Fatal("the run started before the workers joined")
	}

	f.SetWorkers(3)
	var r *stampedev1.StampedeRun
	eventually(t, wait, "phase Running", func() bool {
		r = getRun(t, ns, "release-42")
		return r.Status.Phase == stampedev1.RunRunning && r.Status.RunID != ""
	})
	in := f.LastRunCreate()
	if in.Workers != 3 || in.Overrides == nil || in.Overrides.Rate != "50/s" || in.Env["TARGET"] != "x" ||
		!strings.HasPrefix(in.Note, "release 42 [k8s:run-ok/release-42 ") || in.Version != 1 {
		t.Fatalf("run create body: %+v", in)
	}
	if got := f.Scenarios(r.Status.ProjectID); len(got) != 1 || got[0].Name != "checkout" {
		t.Fatalf("scenarios: %+v", got)
	}

	f.FinishRun(r.Status.RunID, "completed", "pass")
	eventually(t, wait, "phase Completed", func() bool {
		r = getRun(t, ns, "release-42")
		return r.Status.Phase == stampedev1.RunCompleted
	})
	if r.Status.Verdict != "pass" || r.Status.ReportURL != f.URL+"/runs/"+r.Status.RunID ||
		r.Status.Summary == nil || r.Status.Summary.Requests != 1200 || r.Status.Summary.P95 != "12ms" || r.Status.CompletedAt == nil {
		t.Fatalf("status: %+v", r.Status)
	}
	eventually(t, wait, "the pool restored to 1", func() bool {
		_ = k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "pool"}, &dep)
		return *dep.Spec.Replicas == 1 && dep.Annotations[AnnotationScaledForRun] == ""
	})
	if f.RunCreates() != 1 {
		t.Fatalf("%d runs created, want 1", f.RunCreates())
	}

	// The spec is immutable.
	r.Spec.Project = "other"
	if err := k8s.Update(testCtx, r); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("updating the spec: %v", err)
	}
}

func TestRunFailedOnServerAndBadInput(t *testing.T) {
	needEnvtest(t)
	ns := namespace(t, "run-fail")
	f := stampedetest.New("stp_test")
	defer f.Close()
	tokenSecret(t, ns)

	bad := newRun(ns, "no-name", f.URL)
	bad.Spec.Scenario.Inline = "journeys: []\n"
	if err := k8s.Create(testCtx, bad); err != nil {
		t.Fatal(err)
	}
	eventually(t, wait, "the bad run to fail", func() bool {
		r := getRun(t, ns, "no-name")
		return r.Status.Phase == stampedev1.RunFailed && strings.Contains(r.Status.Message, "metadata.name")
	})

	ok := newRun(ns, "aborted", f.URL)
	if err := k8s.Create(testCtx, ok); err != nil {
		t.Fatal(err)
	}
	var r *stampedev1.StampedeRun
	eventually(t, wait, "Running", func() bool {
		r = getRun(t, ns, "aborted")
		return r.Status.RunID != ""
	})
	f.FinishRun(r.Status.RunID, "aborted", "")
	eventually(t, wait, "Failed", func() bool {
		r = getRun(t, ns, "aborted")
		return r.Status.Phase == stampedev1.RunFailed && r.Status.ServerStatus == "aborted"
	})
	if f.RunCreates() != 1 {
		t.Fatalf("%d runs created, want 1", f.RunCreates())
	}
}

func TestRunDeletionStopsTheRun(t *testing.T) {
	needEnvtest(t)
	ns := namespace(t, "run-delete")
	f := stampedetest.New("stp_test")
	defer f.Close()
	tokenSecret(t, ns)
	run := newRun(ns, "doomed", f.URL)
	if err := k8s.Create(testCtx, run); err != nil {
		t.Fatal(err)
	}
	var r *stampedev1.StampedeRun
	eventually(t, wait, "Running", func() bool {
		r = getRun(t, ns, "doomed")
		return r.Status.Phase == stampedev1.RunRunning
	})
	if err := k8s.Delete(testCtx, r); err != nil {
		t.Fatal(err)
	}
	eventually(t, wait, "the object gone and the run stopped", func() bool {
		err := k8s.Get(testCtx, types.NamespacedName{Namespace: ns, Name: "doomed"}, &stampedev1.StampedeRun{})
		return apierrors.IsNotFound(err) && f.Stopped(r.Status.RunID)
	})
}

func TestRunValidation(t *testing.T) {
	needEnvtest(t)
	ns := namespace(t, "run-validate")
	both := newRun(ns, "both", "http://x:8080")
	both.Spec.Server.ClusterRef = "lab"
	if err := k8s.Create(testCtx, both); err == nil || !strings.Contains(err.Error(), "exactly one of server.url") {
		t.Fatalf("server.url and clusterRef together: %v", err)
	}
	neither := newRun(ns, "neither", "http://x:8080")
	neither.Spec.Scenario = stampedev1.RunScenario{}
	if err := k8s.Create(testCtx, neither); err == nil || !strings.Contains(err.Error(), "exactly one of scenario.name") {
		t.Fatalf("no scenario: %v", err)
	}
	badURL := newRun(ns, "bad-url", "ftp://x")
	if err := k8s.Create(testCtx, badURL); err == nil {
		t.Fatal("ftp:// server URL accepted")
	}
}
