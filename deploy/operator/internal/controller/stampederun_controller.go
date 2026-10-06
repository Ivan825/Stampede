package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	stampedev1 "github.com/Ivan825/Stampede/deploy/operator/api/v1alpha1"
	"github.com/Ivan825/Stampede/deploy/operator/internal/stampede"
)

const runFinalizer = "stampede.dev/run-cleanup"

// StampedeRunReconciler drives a StampedeRun through the Stampede REST API.
type StampedeRunReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	// NewClient builds the API client; tests replace it.
	NewClient func(baseURL, token string) *stampede.Client
	// PollInterval is how often a running run is checked (default 5s).
	PollInterval time.Duration
}

// +kubebuilder:rbac:groups=stampede.dev,resources=stampederuns,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=stampede.dev,resources=stampederuns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=stampede.dev,resources=stampederuns/finalizers,verbs=update
// +kubebuilder:rbac:groups=stampede.dev,resources=stampedeclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile advances one StampedeRun.
func (r *StampedeRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var run stampedev1.StampedeRun
	if err := r.Get(ctx, req.NamespacedName, &run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !run.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &run)
	}
	if controllerutil.AddFinalizer(&run, runFinalizer) {
		if err := r.Update(ctx, &run); err != nil {
			return ignoreConflict(err)
		}
	}
	if run.Status.Phase == stampedev1.RunCompleted || run.Status.Phase == stampedev1.RunFailed {
		// Finished. Make sure the workers were scaled back.
		if run.Status.ScaledDeployment != "" {
			if err := r.restoreWorkers(ctx, &run); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, r.Status().Update(ctx, &run)
		}
		return ctrl.Result{}, nil
	}

	res, err := r.advance(ctx, &run)
	if err != nil {
		if stampede.Permanent(err) || errors.Is(err, errPermanent) {
			r.fail(ctx, &run, err.Error())
			if uerr := r.Status().Update(ctx, &run); uerr != nil {
				return ignoreConflict(uerr)
			}
			return ctrl.Result{}, nil
		}
		// Transient: record why and retry with backoff.
		run.Status.Message = err.Error()
		_ = r.Status().Update(ctx, &run)
		return ctrl.Result{}, err
	}
	if err := r.Status().Update(ctx, &run); err != nil {
		return ignoreConflict(err)
	}
	return res, nil
}

var errPermanent = errors.New("permanent")

func permanentf(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errPermanent}, a...)...)
}

func (r *StampedeRunReconciler) poll() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return 5 * time.Second
}

// apiClient resolves the server URL and token.
func (r *StampedeRunReconciler) apiClient(ctx context.Context, run *stampedev1.StampedeRun) (*stampede.Client, string, error) {
	base := run.Spec.Server.URL
	if ref := run.Spec.Server.ClusterRef; ref != "" {
		var sc stampedev1.StampedeCluster
		if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: ref}, &sc); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, "", fmt.Errorf("StampedeCluster %q not found", ref)
			}
			return nil, "", err
		}
		if sc.Status.URL == "" || sc.Status.Phase != "Ready" {
			return nil, "", fmt.Errorf("StampedeCluster %q is not ready", ref)
		}
		base = sc.Status.URL
	}
	var sec corev1.Secret
	sel := run.Spec.APITokenSecretRef
	if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: sel.Name}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, "", fmt.Errorf("API token secret %q not found", sel.Name)
		}
		return nil, "", err
	}
	token := strings.TrimSpace(string(sec.Data[sel.Key]))
	if token == "" {
		return nil, "", permanentf("secret %q has no key %q", sel.Name, sel.Key)
	}
	newClient := r.NewClient
	if newClient == nil {
		newClient = stampede.New
	}
	return newClient(base, token), strings.TrimRight(base, "/"), nil
}

func (r *StampedeRunReconciler) advance(ctx context.Context, run *stampedev1.StampedeRun) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	api, base, err := r.apiClient(ctx, run)
	if err != nil {
		return ctrl.Result{}, err
	}
	st := &run.Status

	if st.RunID == "" {
		if st.Phase == "" {
			st.Phase = stampedev1.RunPending
			st.Message = "resolving project, scenario and target"
		}
		if st.ProjectID == "" || st.ScenarioID == "" || st.TargetID == "" {
			if err := r.resolve(ctx, api, run); err != nil {
				return ctrl.Result{}, err
			}
		}
		if sw := run.Spec.ScaleWorkers; sw != nil {
			ready, err := r.scaleUp(ctx, api, run)
			if err != nil || !ready {
				return ctrl.Result{RequeueAfter: r.poll()}, err
			}
		}
		marker := runMarker(run)
		existing, err := api.FindRunByNote(ctx, st.ProjectID, marker)
		if err != nil {
			return ctrl.Result{}, err
		}
		if existing == nil {
			in := stampede.RunCreate{
				ScenarioID: st.ScenarioID, Version: st.ScenarioVersion, TargetID: st.TargetID,
				Env: run.Spec.Env, Workers: run.Spec.Workers,
				Note: strings.TrimSpace(run.Spec.Note + " " + marker),
			}
			if o := run.Spec.Overrides; o != nil {
				in.Overrides = &stampede.Overrides{Shape: o.Shape, Mode: o.Mode, VUs: o.VUs, Rate: o.Rate, Duration: o.Duration, Start: o.Start, Max: o.Max}
			}
			existing, err = api.CreateRun(ctx, st.ProjectID, in)
			if err != nil {
				return ctrl.Result{}, err
			}
			r.event(run, corev1.EventTypeNormal, "RunCreated", "created Stampede run %s", existing.ID)
			log.Info("created run", "runId", existing.ID)
		}
		st.RunID = existing.ID
		st.ServerStatus = existing.Status
		st.ReportURL = fmt.Sprintf("%s/runs/%s", base, existing.ID)
		st.Phase = stampedev1.RunRunning
		st.Message = "the run is " + existing.Status
		st.StartedAt = ptrNow()
		return ctrl.Result{RequeueAfter: r.poll()}, nil
	}

	got, err := api.GetRun(ctx, st.RunID)
	if err != nil {
		return ctrl.Result{}, err
	}
	st.ServerStatus = got.Status
	st.Message = "the run is " + got.Status
	if !got.Terminal() {
		return ctrl.Result{RequeueAfter: r.poll()}, nil
	}
	if got.Verdict != nil {
		st.Verdict = *got.Verdict
	}
	if s := got.Summary; s != nil {
		st.Summary = &stampedev1.RunSummary{
			Requests:  s.Requests,
			ErrorRate: strconv.FormatFloat(s.ErrorRate, 'g', 4, 64),
			RPS:       strconv.FormatFloat(s.RPS, 'f', 1, 64),
			P95:       (time.Duration(s.P95 * float64(time.Second))).Round(time.Microsecond).String(),
			P99:       (time.Duration(s.P99 * float64(time.Second))).Round(time.Microsecond).String(),
		}
	}
	st.CompletedAt = ptrNow()
	if got.Status == "completed" {
		st.Phase = stampedev1.RunCompleted
		st.Message = "the run completed"
		if st.Verdict != "" {
			st.Message += " with verdict " + st.Verdict
		}
		r.event(run, corev1.EventTypeNormal, "RunCompleted", "%s", st.Message)
	} else {
		st.Phase = stampedev1.RunFailed
		st.Message = "the run " + got.Status
		if got.Error != nil && *got.Error != "" {
			st.Message += ": " + *got.Error
		}
		r.event(run, corev1.EventTypeWarning, "RunFailed", "%s", st.Message)
	}
	if err := r.restoreWorkers(ctx, run); err != nil {
		return ctrl.Result{}, err
	}
	r.setReady(run)
	return ctrl.Result{}, nil
}

func (r *StampedeRunReconciler) resolve(ctx context.Context, api *stampede.Client, run *stampedev1.StampedeRun) error {
	st := &run.Status
	p, err := api.ResolveProject(ctx, run.Spec.Project)
	if err != nil {
		return fmt.Errorf("project %q: %w", run.Spec.Project, err)
	}
	msg := fmt.Sprintf("from StampedeRun %s/%s", run.Namespace, run.Name)
	sid, ver, err := api.ResolveScenario(ctx, p.ID, run.Spec.Scenario.Name, run.Spec.Scenario.Version, run.Spec.Scenario.Inline, msg)
	if err != nil {
		if strings.HasPrefix(err.Error(), "inline scenario") {
			return permanentf("%s", err.Error())
		}
		return fmt.Errorf("scenario: %w", err)
	}
	t, err := api.ResolveTarget(ctx, p.ID, run.Spec.Target.URL)
	if err != nil {
		if !isAPIError(err) {
			return permanentf("%s", err.Error())
		}
		return fmt.Errorf("target: %w", err)
	}
	st.ProjectID, st.ScenarioID, st.ScenarioVersion, st.TargetID = p.ID, sid, ver, t.ID
	return nil
}

func isAPIError(err error) bool {
	var ae *stampede.APIError
	return errors.As(err, &ae)
}

// runMarker identifies runs created for this object, so a retry after a lost
// status update finds the run instead of starting a second one.
func runMarker(run *stampedev1.StampedeRun) string {
	return fmt.Sprintf("[k8s:%s/%s %s]", run.Namespace, run.Name, run.UID)
}

func (r *StampedeRunReconciler) workerDeploymentName(run *stampedev1.StampedeRun) string {
	if d := run.Spec.ScaleWorkers.Deployment; d != "" {
		return d
	}
	if ref := run.Spec.Server.ClusterRef; ref != "" {
		return WorkerDeploymentName(ref)
	}
	return ""
}

// scaleUp scales the worker Deployment for the run and reports whether enough
// workers have joined the server.
func (r *StampedeRunReconciler) scaleUp(ctx context.Context, api *stampede.Client, run *stampedev1.StampedeRun) (bool, error) {
	sw := run.Spec.ScaleWorkers
	st := &run.Status
	name := r.workerDeploymentName(run)
	if name == "" {
		return false, permanentf("scaleWorkers.deployment is required without server.clusterRef")
	}
	if st.ScaledDeployment == "" {
		var dep appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: name}, &dep); err != nil {
			if apierrors.IsNotFound(err) {
				return false, permanentf("worker Deployment %q not found", name)
			}
			return false, err
		}
		me := run.Namespace + "/" + run.Name
		if owner := dep.Annotations[AnnotationScaledForRun]; owner != "" && owner != me {
			st.Phase = stampedev1.RunPending
			st.Message = fmt.Sprintf("waiting: Deployment %s is scaled for %s", name, owner)
			return false, nil
		}
		before := int32(1)
		if dep.Spec.Replicas != nil {
			before = *dep.Spec.Replicas
		}
		if dep.Annotations == nil {
			dep.Annotations = map[string]string{}
		}
		if _, ok := dep.Annotations[AnnotationReplicasBefore]; !ok {
			dep.Annotations[AnnotationReplicasBefore] = strconv.Itoa(int(before))
		}
		dep.Annotations[AnnotationScaledForRun] = me
		if sw.Replicas > before {
			dep.Spec.Replicas = &sw.Replicas
		}
		if err := r.Update(ctx, &dep); err != nil {
			return false, err
		}
		st.ScaledDeployment = name
		st.ScalingStartedAt = ptrNow()
		st.Phase = stampedev1.RunScaling
		r.event(run, corev1.EventTypeNormal, "ScaledWorkers", "scaled Deployment %s to %d for the run", name, max(sw.Replicas, before))
	}
	want := int(sw.Replicas)
	if run.Spec.Workers > 0 {
		want = int(run.Spec.Workers)
	}
	n, err := api.ConnectedWorkers(ctx)
	if err != nil {
		return false, err
	}
	st.Message = fmt.Sprintf("waiting for workers: %d of %d connected", n, want)
	if n >= want {
		return true, nil
	}
	timeout := time.Duration(sw.WaitTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if st.ScalingStartedAt != nil && time.Since(st.ScalingStartedAt.Time) > timeout {
		return false, permanentf("only %d of %d workers connected after %s", n, want, timeout)
	}
	return false, nil
}

// restoreWorkers puts the worker Deployment back to its size before the run.
func (r *StampedeRunReconciler) restoreWorkers(ctx context.Context, run *stampedev1.StampedeRun) error {
	name := run.Status.ScaledDeployment
	if name == "" {
		return nil
	}
	var dep appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: name}, &dep)
	if apierrors.IsNotFound(err) {
		run.Status.ScaledDeployment = ""
		return nil
	}
	if err != nil {
		return err
	}
	if dep.Annotations[AnnotationScaledForRun] == run.Namespace+"/"+run.Name {
		if v, err := strconv.ParseInt(dep.Annotations[AnnotationReplicasBefore], 10, 32); err == nil {
			dep.Spec.Replicas = ptrInt32(int32(v))
		}
		delete(dep.Annotations, AnnotationScaledForRun)
		delete(dep.Annotations, AnnotationReplicasBefore)
		if err := r.Update(ctx, &dep); err != nil {
			return err
		}
		r.event(run, corev1.EventTypeNormal, "RestoredWorkers", "scaled Deployment %s back to %s", name, replicasString(dep.Spec.Replicas))
	}
	run.Status.ScaledDeployment = ""
	return nil
}

func (r *StampedeRunReconciler) fail(ctx context.Context, run *stampedev1.StampedeRun, msg string) {
	msg = strings.TrimPrefix(msg, errPermanent.Error()+": ")
	run.Status.Phase = stampedev1.RunFailed
	run.Status.Message = msg
	run.Status.CompletedAt = ptrNow()
	if err := r.restoreWorkers(ctx, run); err != nil {
		run.Status.Message += "; restoring workers: " + err.Error()
	}
	r.setReady(run)
	r.event(run, corev1.EventTypeWarning, "RunFailed", "%s", msg)
}

// finalize stops an active run and restores the workers before deletion.
func (r *StampedeRunReconciler) finalize(ctx context.Context, run *stampedev1.StampedeRun) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(run, runFinalizer) {
		return ctrl.Result{}, nil
	}
	if run.Status.RunID != "" && run.Status.Phase == stampedev1.RunRunning {
		if api, _, err := r.apiClient(ctx, run); err == nil {
			if err := api.StopRun(ctx, run.Status.RunID); err != nil && !stampede.Permanent(err) {
				return ctrl.Result{}, err
			}
		}
	}
	if err := r.restoreWorkers(ctx, run); err != nil {
		return ctrl.Result{}, err
	}
	controllerutil.RemoveFinalizer(run, runFinalizer)
	return ctrl.Result{}, client.IgnoreNotFound(r.Update(ctx, run))
}

func (r *StampedeRunReconciler) setReady(run *stampedev1.StampedeRun) {
	cond := metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: run.Status.Phase, Message: run.Status.Message, ObservedGeneration: run.Generation}
	meta.SetStatusCondition(&run.Status.Conditions, cond)
}

func (r *StampedeRunReconciler) event(run *stampedev1.StampedeRun, typ, reason, format string, a ...any) {
	if r.Recorder != nil {
		r.Recorder.Eventf(run, nil, typ, reason, "Reconcile", format, a...)
	}
}

func ptrNow() *metav1.Time { t := metav1.Now(); return &t }

func ptrInt32(v int32) *int32 { return &v }

func replicasString(p *int32) string {
	if p == nil {
		return "1"
	}
	return strconv.Itoa(int(*p))
}

// SetupWithManager registers the controller.
func (r *StampedeRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&stampedev1.StampedeRun{}).
		Named("stampederun").
		Complete(r)
}
