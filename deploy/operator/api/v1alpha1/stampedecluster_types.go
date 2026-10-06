package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StampedeClusterSpec describes a Stampede server and its workers. It is a
// simplified form of the Helm chart: the database is always external.
type StampedeClusterSpec struct {
	// Image is the Stampede container image.
	// +kubebuilder:default="ghcr.io/ivan825/stampede:0.1.0"
	// +optional
	Image string `json:"image,omitempty"`

	// ImagePullPolicy for the server and worker containers.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +kubebuilder:default=IfNotPresent
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// Database points at the PostgreSQL (TimescaleDB recommended) URL.
	Database DatabaseSpec `json:"database"`

	// MasterKeySecretRef selects an existing master key (base64 of 32 bytes,
	// from `stampede keygen`). When empty the operator generates one in the
	// Secret <name>-stampede-keys and never changes it afterwards.
	// +optional
	MasterKeySecretRef *corev1.SecretKeySelector `json:"masterKeySecretRef,omitempty"`

	// JoinTokenSecretRef selects an existing worker join token. When empty
	// the operator generates one in the Secret <name>-stampede-keys.
	// +optional
	JoinTokenSecretRef *corev1.SecretKeySelector `json:"joinTokenSecretRef,omitempty"`

	// Server configures the control plane.
	// +optional
	Server ServerSpec `json:"server,omitempty"`

	// Workers configures the in-cluster worker Deployment.
	// +optional
	Workers WorkersSpec `json:"workers,omitempty"`
}

// DatabaseSpec selects the database URL.
type DatabaseSpec struct {
	// URLSecretRef selects a Secret key holding postgres://user:pass@host:5432/db.
	URLSecretRef corev1.SecretKeySelector `json:"urlSecretRef"`
}

// ServerSpec configures the Stampede server.
type ServerSpec struct {
	// Executor: auto (workers when connected, else in-process), workers or local.
	// +kubebuilder:validation:Enum=auto;workers;local
	// +kubebuilder:default=auto
	// +optional
	Executor string `json:"executor,omitempty"`

	// ServiceType of the server Service.
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default=ClusterIP
	// +optional
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// Resources of the server container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// ExtraArgs are appended to `stampede server`.
	// +optional
	ExtraArgs []string `json:"extraArgs,omitempty"`
}

// WorkersSpec configures the worker Deployment.
type WorkersSpec struct {
	// Replicas is the resting number of workers. A StampedeRun may scale the
	// Deployment up for the length of a run.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Region label the workers report.
	// +kubebuilder:default=kubernetes
	// +optional
	Region string `json:"region,omitempty"`

	// Labels are extra worker labels (--label KEY=VALUE).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// MaxVUs caps the virtual users one worker accepts (0 = no limit).
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxVUs int32 `json:"maxVUs,omitempty"`

	// Resources of each worker container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// StampedeClusterStatus reports what the operator observed.
type StampedeClusterStatus struct {
	// Phase is Pending until the server is ready, then Ready.
	// +optional
	Phase string `json:"phase,omitempty"`

	// URL is the in-cluster address of the web UI and REST API.
	// +optional
	URL string `json:"url,omitempty"`

	// WorkerAddress is the host:port workers dial.
	// +optional
	WorkerAddress string `json:"workerAddress,omitempty"`

	// KeysSecret names the Secret holding generated keys, if any.
	// +optional
	KeysSecret string `json:"keysSecret,omitempty"`

	// WorkerDeployment names the worker Deployment (a StampedeRun can scale it).
	// +optional
	WorkerDeployment string `json:"workerDeployment,omitempty"`

	// ReadyWorkers is the number of ready worker pods.
	// +optional
	ReadyWorkers int32 `json:"readyWorkers,omitempty"`

	// ObservedGeneration is the generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions: Ready.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// StampedeCluster deploys a Stampede server and workers.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=stc,categories=stampede
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Workers",type=integer,JSONPath=`.status.readyWorkers`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.url`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type StampedeCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StampedeClusterSpec   `json:"spec,omitempty"`
	Status StampedeClusterStatus `json:"status,omitempty"`
}

// StampedeClusterList is a list of StampedeCluster.
//
// +kubebuilder:object:root=true
type StampedeClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StampedeCluster `json:"items"`
}
