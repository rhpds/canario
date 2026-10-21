/*
Copyright 2026 Red Hat Demo Platform.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RunTypeGuest = "guest"

	ProfileSmall   = "small"
	ProfileDefault = "default"

	WorkersAll     = "all"
	WorkersBusiest = "busiest"

	StorageDefault = "default"
	StorageList    = "list"

	NetworkAuto = "auto"
	NetworkOn   = "on"
	NetworkOff  = "off"

	PhasePending   = "Pending"
	PhaseRunning   = "Running"
	PhaseSucceeded = "Succeeded"
	PhaseFailed    = "Failed"
)

// GuestDataSourceRef selects a CDI DataSource (golden image PVC) by name and namespace.
type GuestDataSourceRef struct {
	// Name of the DataSource (e.g. fedora, rhel9).
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the DataSource (cluster-specific; no default).
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
}

// GuestImage selects the CioVM boot disk source.
// Exactly one of dataSource or registry must be set.
type GuestImage struct {
	// DataSource clones a Ready CDI DataSource in the given namespace.
	// +optional
	DataSource *GuestDataSourceRef `json:"dataSource,omitempty"`

	// Registry pulls via CDI (e.g. docker://quay.io/containerdisks/fedora:40).
	// +optional
	Registry string `json:"registry,omitempty"`
}

// SuiteSpec selects which in-guest bench sections to run.
type SuiteSpec struct {
	// Sections is a comma-separated list or "all"
	// (storage,fileops,responsive,packages,compile,cpu,memory,network,git).
	// Empty uses operator default, then "all".
	// +optional
	Sections string `json:"sections,omitempty"`
}

// NetworkSpec configures external network / git checks inside the guest.
// Empty fields fall back to operator ConfigMap defaults, then built-in suite defaults.
type NetworkSpec struct {
	// Mode is auto (probe internet), on, or off.
	// +kubebuilder:validation:Enum=auto;on;off
	// +optional
	Mode string `json:"mode,omitempty"`

	// PingTarget is the host for ICMP ping (e.g. 1.1.1.1).
	// +optional
	PingTarget string `json:"pingTarget,omitempty"`

	// DNSName is resolved for the DNS check (e.g. dns.google).
	// +optional
	DNSName string `json:"dnsName,omitempty"`

	// DownloadURL is fetched for the throughput check (~1MiB recommended).
	// +optional
	DownloadURL string `json:"downloadURL,omitempty"`

	// GitURL is cloned when the git section runs (empty skips git).
	// +optional
	GitURL string `json:"gitURL,omitempty"`
}

// CanarioRunSpec is the desired configuration for a one-shot canary run.
type CanarioRunSpec struct {
	// Type selects the canary experiment. "guest" creates a temporary guest VM.
	// +kubebuilder:validation:Enum=guest
	// +kubebuilder:default=guest
	Type string `json:"type,omitempty"`

	// Profile selects VM sizing: small (periodic default) or default (larger).
	// +kubebuilder:validation:Enum=small;default
	// +kubebuilder:default=small
	Profile string `json:"profile,omitempty"`

	// Workers selects placement: "all", "busiest", or a worker node name.
	// +kubebuilder:default=all
	Workers string `json:"workers,omitempty"`

	// GuestImage selects the boot disk (DataSource clone or registry pull).
	// Optional when the operator is configured with install-time defaults
	// (CANARIO_DEFAULT_GUEST_*); otherwise required.
	// +optional
	GuestImage *GuestImage `json:"guestImage,omitempty"`

	// Storage selects how data disks are chosen: "default" (cluster default SC)
	// or "list" (explicit storageClasses).
	// +kubebuilder:validation:Enum=default;list
	// +kubebuilder:default=default
	Storage string `json:"storage,omitempty"`

	// StorageClasses is the explicit list of StorageClasses when storage=list
	// (one guest data disk per entry).
	// +optional
	StorageClasses []string `json:"storageClasses,omitempty"`

	// Suite selects in-guest bench sections. Optional; defaults from operator ConfigMap / "all".
	// +optional
	Suite *SuiteSpec `json:"suite,omitempty"`

	// Network configures external ping/DNS/download/git checks. Optional;
	// defaults from operator ConfigMap / built-in suite defaults.
	// +optional
	Network *NetworkSpec `json:"network,omitempty"`

	// ScoringRef names a CanarioScore in the same namespace. When set, the
	// operator applies that policy once the suite finishes. Omit for metrics-only.
	// +optional
	ScoringRef string `json:"scoringRef,omitempty"`
}

// DiskScore is per-StorageClass storage module points on a worker.
type DiskScore struct {
	StorageClass string `json:"storageClass,omitempty"`
	Tier         string `json:"tier,omitempty"`
	// Storage is points for the storage module on this disk (0 = best; always set when scored).
	Storage int32 `json:"storage"`
}

// WorkerScores holds modular points for one worker (lower is better).
// Point fields omit omitempty so a graded 0 still appears in status JSON.
type WorkerScores struct {
	Total      int32 `json:"total"`
	Responsive int32 `json:"responsive"`
	Packages   int32 `json:"packages"`
	Fileops    int32 `json:"fileops"`
	Storage    int32 `json:"storage"` // worst disk
	Network    int32 `json:"network"`
	// Disks are per-SC storage points (optional detail).
	// +optional
	Disks []DiskScore `json:"disks,omitempty"`
}

// RunScores is the run-level rollup (worst worker) plus policy snapshot.
type RunScores struct {
	// ScoringRef is the CanarioScore name applied.
	ScoringRef string `json:"scoringRef,omitempty"`
	// Total is max(worker totals) — worst worker (0 = best; always set when scored).
	Total int32 `json:"total"`
	// Verdict is from CanarioScore verdict bands (e.g. good, fair).
	Verdict string `json:"verdict,omitempty"`
}

// DiskResult holds suite metrics for one attached data disk / StorageClass
// (storage + fileops sections).
type DiskResult struct {
	StorageClass     string `json:"storageClass,omitempty"`
	DataDev          string `json:"dataDev,omitempty"`
	VolumeMode       string `json:"volumeMode,omitempty"`
	AccessMode       string `json:"accessMode,omitempty"`
	Provisioner      string `json:"provisioner,omitempty"`
	SuiteStatus      string `json:"suiteStatus,omitempty"`
	Sections         string `json:"sections,omitempty"`
	NodeName         string `json:"nodeName,omitempty"`
	FioSeqWriteBW    string `json:"fioSeqWriteBW,omitempty"`
	FioSeqReadBW     string `json:"fioSeqReadBW,omitempty"`
	FioRandReadIOPS  string `json:"fioRandReadIOPS,omitempty"`
	FioRandWriteIOPS string `json:"fioRandWriteIOPS,omitempty"`
	FileopsCreateMs  string `json:"fileopsCreate1000Ms,omitempty"`
	FileopsTarMs     string `json:"fileopsTarCreateMs,omitempty"`
	FileopsExtractMs string `json:"fileopsTarExtractMs,omitempty"`
	Message          string `json:"message,omitempty"`
}

// GuestResult holds once-per-VM suite metrics (responsive, packages, compile,
// cpu, memory, network, git). Network/git may be status "skipped" without internet.
type GuestResult struct {
	CmdLsEtcMs         string `json:"cmdLsEtcMs,omitempty"`
	CmdFindConfMs      string `json:"cmdFindConfMs,omitempty"`
	CmdPsMs            string `json:"cmdPsMs,omitempty"`
	DnfInstallMs       string `json:"dnfInstallMs,omitempty"`
	DnfInstallStatus   string `json:"dnfInstallStatus,omitempty"`
	CompileGccMs       string `json:"compileGccMs,omitempty"`
	CompileRunMs       string `json:"compileRunMs,omitempty"`
	CPUMatrixBogoOps   string `json:"cpuMatrixBogoOps,omitempty"`
	MemMemcpyBogoOps   string `json:"memMemcpyBogoOps,omitempty"`
	NetworkStatus      string `json:"networkStatus,omitempty"` // ok|failed|skipped
	NetPingAvgMs       string `json:"netPingAvgMs,omitempty"`
	NetPingStatus      string `json:"netPingStatus,omitempty"`
	NetDNSMs           string `json:"netDnsMs,omitempty"`
	NetDNSStatus       string `json:"netDnsStatus,omitempty"`
	NetDownloadMbps    string `json:"netDownloadMbps,omitempty"`
	NetTTFBMs          string `json:"netTtfbMs,omitempty"`
	NetDownloadStatus  string `json:"netDownloadStatus,omitempty"`
	GitCloneMs         string `json:"gitCloneMs,omitempty"`
	GitCloneStatus     string `json:"gitCloneStatus,omitempty"`
}

// WorkerResult is the per-worker CioVM outcome for a CanarioRun.
type WorkerResult struct {
	// NodeName is the worker node.
	NodeName string `json:"nodeName,omitempty"`

	// CioVMName is the owned KubeVirt VirtualMachine on this node.
	CioVMName string `json:"cioVMName,omitempty"`

	// Phase is Pending, Running, Succeeded, or Failed for this worker.
	Phase string `json:"phase,omitempty"`

	// SuiteStatus is the in-guest suite status (running|succeeded|failed).
	SuiteStatus string `json:"suiteStatus,omitempty"`

	// Message is a human-readable detail for this worker.
	Message string `json:"message,omitempty"`

	// Guest is once-per-VM suite metrics.
	// +optional
	Guest *GuestResult `json:"guest,omitempty"`

	// Disks are per-StorageClass suite results on this worker.
	// +optional
	Disks []DiskResult `json:"disks,omitempty"`

	// Scores is filled when the Run has scoringRef and scoring completed.
	// +optional
	Scores *WorkerScores `json:"scores,omitempty"`
}

// CanarioRunStatus is the observed state of a CanarioRun.
type CanarioRunStatus struct {
	// Phase is Pending, Running, Succeeded, or Failed (aggregate over workers).
	Phase string `json:"phase,omitempty"`

	// Message is a human-readable detail for the current phase.
	Message string `json:"message,omitempty"`

	// SuiteStatus is the overall suite status (running|succeeded|failed).
	SuiteStatus string `json:"suiteStatus,omitempty"`

	// Workers holds per-worker CioVM results (source of truth for placement/disks).
	// +optional
	Workers []WorkerResult `json:"workers,omitempty"`

	// Scores is the run rollup (worst worker) when scoringRef was set.
	// +optional
	Scores *RunScores `json:"scores,omitempty"`

	// CompletionTime is when the experiment finished (UTC).
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Suite",type=string,JSONPath=`.status.suiteStatus`
// +kubebuilder:printcolumn:name="Score",type=integer,JSONPath=`.status.scores.total`
// +kubebuilder:printcolumn:name="Verdict",type=string,JSONPath=`.status.scores.verdict`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CanarioRun is a one-shot guest canary experiment.
type CanarioRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CanarioRunSpec   `json:"spec,omitempty"`
	Status CanarioRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CanarioRunList contains a list of CanarioRun.
type CanarioRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CanarioRun `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CanarioRun{}, &CanarioRunList{})
}
