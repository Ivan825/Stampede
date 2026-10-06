// Package controller holds the StampedeCluster and StampedeRun reconcilers.
package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	stampedev1 "github.com/Ivan825/Stampede/deploy/operator/api/v1alpha1"
)

const (
	httpPort   = 8080
	workerPort = 8081

	keyMaster = "master-key"
	keyJoin   = "join-token"

	// AnnotationScaledForRun marks a worker Deployment a StampedeRun has
	// scaled; its value is <namespace>/<name> of the run.
	AnnotationScaledForRun = "stampede.dev/scaled-for-run"
	// AnnotationReplicasBefore keeps the replica count to restore.
	AnnotationReplicasBefore = "stampede.dev/replicas-before-run"
)

// StampedeClusterReconciler deploys a server and workers for each StampedeCluster.
type StampedeClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=stampede.dev,resources=stampedeclusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=stampede.dev,resources=stampedeclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=stampede.dev,resources=stampedeclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile makes the cluster's Secret, Deployments and Service match its spec.
func (r *StampedeClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var sc stampedev1.StampedeCluster
	if err := r.Get(ctx, req.NamespacedName, &sc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	applyClusterDefaults(&sc)

	keysSecret := ""
	if sc.Spec.MasterKeySecretRef == nil || sc.Spec.JoinTokenSecretRef == nil {
		keysSecret = keysSecretName(&sc)
		if err := r.ensureKeys(ctx, &sc); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.ensureService(ctx, &sc); err != nil {
		return ctrl.Result{}, err
	}
	server, err := r.ensureServer(ctx, &sc)
	if err != nil {
		return ctrl.Result{}, err
	}
	workers, err := r.ensureWorkers(ctx, &sc)
	if err != nil {
		return ctrl.Result{}, err
	}

	st := stampedev1.StampedeClusterStatus{
		URL:                fmt.Sprintf("http://%s.%s.svc:%d", sc.Name, sc.Namespace, httpPort),
		WorkerAddress:      fmt.Sprintf("%s.%s.svc:%d", sc.Name, sc.Namespace, workerPort),
		KeysSecret:         keysSecret,
		WorkerDeployment:   workers.Name,
		ReadyWorkers:       workers.Status.ReadyReplicas,
		ObservedGeneration: sc.Generation,
		Conditions:         sc.Status.Conditions,
	}
	cond := metav1.Condition{Type: "Ready", ObservedGeneration: sc.Generation}
	if server.Status.AvailableReplicas > 0 {
		st.Phase = "Ready"
		cond.Status, cond.Reason, cond.Message = metav1.ConditionTrue, "ServerAvailable", "the server is available"
	} else {
		st.Phase = "Pending"
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, "ServerUnavailable", "waiting for the server to become available"
	}
	meta.SetStatusCondition(&st.Conditions, cond)
	sc.Status = st
	if err := r.Status().Update(ctx, &sc); err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("reconciled", "phase", st.Phase, "readyWorkers", st.ReadyWorkers)
	return ctrl.Result{}, nil
}

func applyClusterDefaults(sc *stampedev1.StampedeCluster) {
	s := &sc.Spec
	if s.Image == "" {
		s.Image = "ghcr.io/ivan825/stampede:0.1.0"
	}
	if s.ImagePullPolicy == "" {
		s.ImagePullPolicy = corev1.PullIfNotPresent
	}
	if s.Server.Executor == "" {
		s.Server.Executor = "auto"
	}
	if s.Server.ServiceType == "" {
		s.Server.ServiceType = corev1.ServiceTypeClusterIP
	}
	if s.Workers.Replicas == nil {
		s.Workers.Replicas = ptr.To[int32](1)
	}
	if s.Workers.Region == "" {
		s.Workers.Region = "kubernetes"
	}
}

func keysSecretName(sc *stampedev1.StampedeCluster) string { return sc.Name + "-stampede-keys" }

// WorkerDeploymentName is the worker Deployment of a StampedeCluster.
func WorkerDeploymentName(clusterName string) string { return clusterName + "-worker" }

func labelsFor(sc *stampedev1.StampedeCluster, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "stampede",
		"app.kubernetes.io/instance":   sc.Name,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": "stampede-operator",
	}
}

func randomKey(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.StdEncoding.EncodeToString(b)
}

// ensureKeys creates the generated keys once. Existing values are never
// changed: a new master key would make stored secrets unreadable.
func (r *StampedeClusterReconciler) ensureKeys(ctx context.Context, sc *stampedev1.StampedeCluster) error {
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: keysSecretName(sc), Namespace: sc.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sec, func() error {
		sec.Labels = labelsFor(sc, "keys")
		if sec.Data == nil {
			sec.Data = map[string][]byte{}
		}
		if sc.Spec.MasterKeySecretRef == nil && len(sec.Data[keyMaster]) == 0 {
			sec.Data[keyMaster] = []byte(randomKey(32))
		}
		if sc.Spec.JoinTokenSecretRef == nil && len(sec.Data[keyJoin]) == 0 {
			sec.Data[keyJoin] = []byte(randomKey(36))
		}
		// Deliberately no owner reference: deleting the StampedeCluster
		// keeps the master key, which the database still depends on.
		return nil
	})
	return err
}

func (r *StampedeClusterReconciler) ensureService(ctx context.Context, sc *stampedev1.StampedeCluster) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: sc.Name, Namespace: sc.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = labelsFor(sc, "server")
		svc.Spec.Type = sc.Spec.Server.ServiceType
		svc.Spec.Selector = labelsFor(sc, "server")
		svc.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: httpPort, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP},
			{Name: "workers", Port: workerPort, TargetPort: intstr.FromString("workers"), Protocol: corev1.ProtocolTCP, AppProtocol: ptr.To("grpc")},
		}
		return controllerutil.SetControllerReference(sc, svc, r.Scheme)
	})
	return err
}

func secretEnv(name string, sel corev1.SecretKeySelector) corev1.EnvVar {
	return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &sel}}
}

func (r *StampedeClusterReconciler) keyRefs(sc *stampedev1.StampedeCluster) (master, join corev1.SecretKeySelector) {
	gen := corev1.LocalObjectReference{Name: keysSecretName(sc)}
	master = corev1.SecretKeySelector{LocalObjectReference: gen, Key: keyMaster}
	join = corev1.SecretKeySelector{LocalObjectReference: gen, Key: keyJoin}
	if sc.Spec.MasterKeySecretRef != nil {
		master = *sc.Spec.MasterKeySecretRef
	}
	if sc.Spec.JoinTokenSecretRef != nil {
		join = *sc.Spec.JoinTokenSecretRef
	}
	return master, join
}

func podSecurity() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		RunAsNonRoot:   ptr.To(true),
		RunAsUser:      ptr.To[int64](65532),
		RunAsGroup:     ptr.To[int64](65532),
		FSGroup:        ptr.To[int64](65532),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

func containerSecurity() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

func tmpVolume() (corev1.Volume, corev1.VolumeMount) {
	return corev1.Volume{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		corev1.VolumeMount{Name: "tmp", MountPath: "/tmp"}
}

func httpProbe(path string, period, failures int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:     corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromString("http")}},
		PeriodSeconds:    period,
		TimeoutSeconds:   3,
		FailureThreshold: failures,
	}
}

func (r *StampedeClusterReconciler) ensureServer(ctx context.Context, sc *stampedev1.StampedeCluster) (*appsv1.Deployment, error) {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: sc.Name + "-server", Namespace: sc.Namespace}}
	master, join := r.keyRefs(sc)
	tmpV, tmpM := tmpVolume()
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		lbl := labelsFor(sc, "server")
		dep.Labels = lbl
		// One replica, never overlapping: a starting server fails every
		// unfinished run, including another replica's.
		dep.Spec.Replicas = ptr.To[int32](1)
		dep.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: lbl}
		dep.Spec.Template.Labels = lbl
		ps := &dep.Spec.Template.Spec
		ps.AutomountServiceAccountToken = ptr.To(false)
		ps.SecurityContext = podSecurity()
		ps.TerminationGracePeriodSeconds = ptr.To[int64](60)
		ps.Volumes = []corev1.Volume{tmpV}
		ps.Containers = []corev1.Container{{
			Name:            "server",
			Image:           sc.Spec.Image,
			ImagePullPolicy: sc.Spec.ImagePullPolicy,
			Args:            append([]string{"server"}, sc.Spec.Server.ExtraArgs...),
			Env: []corev1.EnvVar{
				{Name: "STAMPEDE_ADDR", Value: fmt.Sprintf(":%d", httpPort)},
				{Name: "STAMPEDE_WORKER_ADDR", Value: fmt.Sprintf(":%d", workerPort)},
				{Name: "STAMPEDE_EXECUTOR", Value: sc.Spec.Server.Executor},
				secretEnv("STAMPEDE_DATABASE_URL", sc.Spec.Database.URLSecretRef),
				secretEnv("STAMPEDE_MASTER_KEY", master),
				secretEnv("STAMPEDE_JOIN_TOKEN", join),
				// Workers enroll for certificates from a CA derived from the
				// master key; the join token never crosses the network.
				{Name: "STAMPEDE_WORKER_MTLS", Value: "true"},
			},
			Ports: []corev1.ContainerPort{
				{Name: "http", ContainerPort: httpPort, Protocol: corev1.ProtocolTCP},
				{Name: "workers", ContainerPort: workerPort, Protocol: corev1.ProtocolTCP},
			},
			StartupProbe:    httpProbe("/healthz", 2, 90),
			LivenessProbe:   httpProbe("/healthz", 10, 3),
			ReadinessProbe:  httpProbe("/readyz", 5, 3),
			Resources:       sc.Spec.Server.Resources,
			SecurityContext: containerSecurity(),
			VolumeMounts:    []corev1.VolumeMount{tmpM},
		}}
		return controllerutil.SetControllerReference(sc, dep, r.Scheme)
	})
	return dep, err
}

func (r *StampedeClusterReconciler) ensureWorkers(ctx context.Context, sc *stampedev1.StampedeCluster) (*appsv1.Deployment, error) {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: WorkerDeploymentName(sc.Name), Namespace: sc.Namespace}}
	_, join := r.keyRefs(sc)
	tmpV, tmpM := tmpVolume()
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		lbl := labelsFor(sc, "worker")
		dep.Labels = lbl
		// While a StampedeRun has scaled the workers it owns the replica
		// count; it restores it when the run ends.
		if _, scaled := dep.Annotations[AnnotationScaledForRun]; !scaled || dep.Spec.Replicas == nil {
			dep.Spec.Replicas = sc.Spec.Workers.Replicas
		}
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: lbl}
		dep.Spec.Template.Labels = lbl
		args := []string{
			"worker",
			fmt.Sprintf("--server=%s:%d", sc.Name, workerPort),
			"--mtls",
			"--name=$(POD_NAME)",
			"--region=" + sc.Spec.Workers.Region,
		}
		keys := make([]string, 0, len(sc.Spec.Workers.Labels))
		for k := range sc.Spec.Workers.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, fmt.Sprintf("--label=%s=%s", k, sc.Spec.Workers.Labels[k]))
		}
		if sc.Spec.Workers.MaxVUs > 0 {
			args = append(args, fmt.Sprintf("--max-vus=%d", sc.Spec.Workers.MaxVUs))
		}
		ps := &dep.Spec.Template.Spec
		ps.AutomountServiceAccountToken = ptr.To(false)
		ps.SecurityContext = podSecurity()
		ps.Volumes = []corev1.Volume{tmpV}
		ps.Containers = []corev1.Container{{
			Name:            "worker",
			Image:           sc.Spec.Image,
			ImagePullPolicy: sc.Spec.ImagePullPolicy,
			Args:            args,
			Env: []corev1.EnvVar{
				{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
				secretEnv("STAMPEDE_JOIN_TOKEN", join),
			},
			Resources:       sc.Spec.Workers.Resources,
			SecurityContext: containerSecurity(),
			VolumeMounts:    []corev1.VolumeMount{tmpM},
		}}
		return controllerutil.SetControllerReference(sc, dep, r.Scheme)
	})
	return dep, err
}

// SetupWithManager registers the controller.
func (r *StampedeClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&stampedev1.StampedeCluster{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("stampedecluster").
		Complete(r)
}

// ignoreConflict turns optimistic-concurrency conflicts into a quiet requeue.
func ignoreConflict(err error) (ctrl.Result, error) {
	if apierrors.IsConflict(err) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return ctrl.Result{}, err
}
