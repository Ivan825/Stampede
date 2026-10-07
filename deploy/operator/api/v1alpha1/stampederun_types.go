package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StampedeRunSpec declares one load test run. The spec is immutable: to run
// again, create a new StampedeRun.
//
// +kubebuilder:validation:XValidation:rule="has(self.server.url) != has(self.server.clusterRef)",message="set exactly one of server.url and server.clusterRef"
// +kubebuilder:validation:XValidation:rule="has(self.scenario.name) != has(self.scenario.inline)",message="set exactly one of scenario.name and scenario.inline"
type StampedeRunSpec struct {
	// Server is the Stampede server that runs the test.
	Server RunServer `json:"server"`

	// APITokenSecretRef selects a Secret key holding a Stampede API token
	// (stp_..., created under Settings > API tokens). The runner role can
	// start runs of existing scenarios on existing targets; editor or above
	// is needed when the operator must create the project, the target or an
	// inline scenario.
	APITokenSecretRef corev1.SecretKeySelector `json:"apiTokenSecretRef"`

	// Project is the Stampede project, by ID, slug or name. It is created
	// when no project matches.
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`

	// Scenario is the scenario to run.
	Scenario RunScenario `json:"scenario"`

	// Target is the system under test.
	Target RunTarget `json:"target"`

	// Overrides change the scenario's load for this run.
	// +optional
	Overrides *RunOverrides `json:"overrides,omitempty"`

	// Env sets ${env.NAME} values for the scenario.
	// +optional
	Env map[string]string `json:"env,omitempty"`

	// Workers is how many connected workers share the run (0 = all).
	// +kubebuilder:validation:Minimum=0
	// +optional
	Workers int32 `json:"workers,omitempty"`

	// ScaleWorkers scales a worker Deployment up before the run and back to
	// its previous size afterwards.
	// +optional
	ScaleWorkers *ScaleWorkers `json:"scaleWorkers,omitempty"`

	// Note is stored with the run.
	// +kubebuilder:validation:MaxLength=300
	// +optional
	Note string `json:"note,omitempty"`
}

// RunServer locates the Stampede server.
type RunServer struct {
	// URL of the server, for example http://stampede.stampede.svc:8080.
	// +kubebuilder:validation:Pattern=`^https?://`
	// +optional
	URL string `json:"url,omitempty"`

	// ClusterRef names a StampedeCluster in the run's namespace.
	// +optional
	ClusterRef string `json:"clusterRef,omitempty"`
}

// RunScenario selects a stored scenario or carries one inline.
type RunScenario struct {
	// Name (or ID) of a scenario stored in the project.
	// +optional
	Name string `json:"name,omitempty"`

	// Version of the stored scenario (default: latest).
	// +kubebuilder:validation:Minimum=1
	// +optional
	Version int32 `json:"version,omitempty"`

	// Inline is a complete scenario document (YAML). It is saved in the
	// project under its metadata.name, as a new version when it changed.
	// +optional
	Inline string `json:"inline,omitempty"`
}

// RunTarget is the system under test.
type RunTarget struct {
	// URL is the base URL. A project target with this URL is reused or
	// created. Public targets must be verified in Stampede before they can
	// take more than the low default caps.
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`
}

// RunOverrides mirror the API's run overrides.
type RunOverrides struct {
	// Shape: smoke, baseline, stress, spike, soak, breakpoint, step, recovery, wave.
	// +optional
	Shape string `json:"shape,omitempty"`
	// Mode: vus or rate.
	// +kubebuilder:validation:Enum=vus;rate
	// +optional
	Mode string `json:"mode,omitempty"`
	// VUs is the number of virtual users.
	// +kubebuilder:validation:Minimum=1
	// +optional
	VUs int32 `json:"vus,omitempty"`
	// Rate, for example 100/s.
	// +optional
	Rate string `json:"rate,omitempty"`
	// Duration, for example 5m.
	// +optional
	Duration string `json:"duration,omitempty"`
	// Start is the starting load of a ramping shape.
	// +optional
	Start string `json:"start,omitempty"`
	// Max is the peak load of a ramping shape.
	// +optional
	Max string `json:"max,omitempty"`
}

// ScaleWorkers scales a worker Deployment for the run.
type ScaleWorkers struct {
	// Deployment in the run's namespace. Defaults to the worker Deployment of
	// server.clusterRef. Do not point it at a Deployment an HPA manages.
	// +optional
	Deployment string `json:"deployment,omitempty"`

	// Replicas during the run.
	// +kubebuilder:validation:Minimum=1
	Replicas int32 `json:"replicas"`

	// WaitTimeoutSeconds is how long to wait for the workers to connect.
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:default=300
	// +optional
	WaitTimeoutSeconds int32 `json:"waitTimeoutSeconds,omitempty"`

	// OnSaturation, when set, restarts the run with more workers if one
	// of its workers reports itself saturated early in the run, while
	// load is still ramping. A run's shares are fixed when it starts, so
	// added workers only help a new run.
	// +optional
	OnSaturation *SaturationScaling `json:"onSaturation,omitempty"`
}

// SaturationScaling doubles the worker Deployment, up to MaxReplicas,
// each time a run's workers saturate within WindowSeconds of its start.
type SaturationScaling struct {
	// MaxReplicas caps the Deployment.
	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas"`

	// WindowSeconds after the run starts in which saturation restarts it.
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:default=120
	// +optional
	WindowSeconds int32 `json:"windowSeconds,omitempty"`
}

// RunAttempt is a run the operator stopped to restart it with more workers.
type RunAttempt struct {
	RunID    string `json:"runId"`
	Replicas int32  `json:"replicas"`
	Reason   string `json:"reason"`
}

// Run phases.
const (
	RunPending   = "Pending"   // resolving project, scenario and target
	RunScaling   = "Scaling"   // waiting for workers to connect
	RunRunning   = "Running"   // the run exists on the server
	RunCompleted = "Completed" // the run completed; see verdict
	RunFailed    = "Failed"    // the run failed, was aborted, or could not start
)

// StampedeRunStatus reports progress and the result.
type StampedeRunStatus struct {
	// Phase: Pending, Scaling, Running, Completed or Failed.
	// +optional
	Phase string `json:"phase,omitempty"`

	// RunID is the Stampede run ID.
	// +optional
	RunID string `json:"runId,omitempty"`

	// ProjectID, ScenarioID, ScenarioVersion and TargetID were resolved for the run.
	// +optional
	ProjectID string `json:"projectId,omitempty"`
	// +optional
	ScenarioID string `json:"scenarioId,omitempty"`
	// +optional
	ScenarioVersion int32 `json:"scenarioVersion,omitempty"`
	// +optional
	TargetID string `json:"targetId,omitempty"`

	// ServerStatus is the run status reported by Stampede.
	// +optional
	ServerStatus string `json:"serverStatus,omitempty"`

	// Verdict: pass, fail, generator-limited or no-targets.
	// +optional
	Verdict string `json:"verdict,omitempty"`

	// ReportURL is the run's page in the web UI.
	// +optional
	ReportURL string `json:"reportURL,omitempty"`

	// Message explains the phase.
	// +optional
	Message string `json:"message,omitempty"`

	// Summary of the finished run.
	// +optional
	Summary *RunSummary `json:"summary,omitempty"`

	// ScaledDeployment is the worker Deployment scaled for this run, until it
	// is restored.
	// +optional
	ScaledDeployment string `json:"scaledDeployment,omitempty"`

	// ScalingStartedAt is when the operator began waiting for workers.
	// +optional
	ScalingStartedAt *metav1.Time `json:"scalingStartedAt,omitempty"`

	// Replicas is the worker Deployment size the current attempt asked for,
	// when onSaturation raised it above scaleWorkers.replicas.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Attempts lists runs stopped because their workers saturated; the
	// current run is RunID.
	// +optional
	Attempts []RunAttempt `json:"attempts,omitempty"`

	// StoppingRunID is an attempt being stopped before the restart.
	// +optional
	StoppingRunID string `json:"stoppingRunId,omitempty"`

	// StartedAt and CompletedAt bracket the run.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// Conditions: Ready (the run finished and resources were restored).
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// RunSummary is the headline result of a run.
type RunSummary struct {
	Requests  int64  `json:"requests,omitempty"`
	ErrorRate string `json:"errorRate,omitempty"`
	RPS       string `json:"rps,omitempty"`
	P95       string `json:"p95,omitempty"`
	P99       string `json:"p99,omitempty"`
}

// StampedeRun declares a Stampede load test run.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=str,categories=stampede
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Verdict",type=string,JSONPath=`.status.verdict`
// +kubebuilder:printcolumn:name="Run",type=string,JSONPath=`.status.runId`,priority=1
// +kubebuilder:printcolumn:name="Report",type=string,JSONPath=`.status.reportURL`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type StampedeRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable; create a new StampedeRun"
	Spec   StampedeRunSpec   `json:"spec,omitempty"`
	Status StampedeRunStatus `json:"status,omitempty"`
}

// StampedeRunList is a list of StampedeRun.
//
// +kubebuilder:object:root=true
type StampedeRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StampedeRun `json:"items"`
}
