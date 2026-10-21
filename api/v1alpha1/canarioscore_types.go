/*
Copyright 2026 Red Hat Demo Platform.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

    10|Unless required by applicable law or agreed to in writing, software
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
	// Storage tier ids (generic — not vendor names).
	StorageTierLocal  = "local"
	StorageTierRemote = "remote"
	StorageTierObject = "object"
)

// UpperBoundBand awards Points when value < Max (or Max omitted = +Inf).
// Evaluate bands in ascending Max order; first match wins.
type UpperBoundBand struct {
	// Max is an exclusive upper bound. Omit for the catch-all (+Inf).
	// +optional
	Max *int64 `json:"max,omitempty"`

	// Points added when this band matches (lower total is better).
	Points int32 `json:"points"`
}

// LowerBoundBand awards Points when value >= Min (higher-is-better metrics).
// Evaluate bands in descending Min order; first match wins.
// Min units depend on the field (MiB/s, IOPS, or Mbps) — use whole numbers.
type LowerBoundBand struct {
	// Min is an inclusive lower bound. Omit for the catch-all (-Inf).
	// +optional
	Min *int64 `json:"min,omitempty"`

	// Points added when this band matches.
	Points int32 `json:"points"`
}

// ResponsiveBands scores guest interactive / cmd timings (ms).
type ResponsiveBands struct {
	// CmdLsEtcMs bands for ls -laR /etc (stand-in for interactive feel).
	// +optional
	CmdLsEtcMs []UpperBoundBand `json:"cmdLsEtcMs,omitempty"`
}

// PackagesBands scores package install.
type PackagesBands struct {
	// DnfInstallMs bands for dnf install duration.
	// +optional
	DnfInstallMs []UpperBoundBand `json:"dnfInstallMs,omitempty"`
}

// FileopsBands scores filesystem metadata ops.
type FileopsBands struct {
	// Create1000Ms bands for creating 1000 files.
	// +optional
	Create1000Ms []UpperBoundBand `json:"create1000Ms,omitempty"`
}

// NetworkBands scores soft external checks.
type NetworkBands struct {
	// FailedCheckPoints added per failed ping/dns/download/git (when not skipped).
	// +kubebuilder:default=1
	// +optional
	FailedCheckPoints int32 `json:"failedCheckPoints,omitempty"`

	// DownloadFailedPoints when download status is failed.
	// +kubebuilder:default=2
	// +optional
	DownloadFailedPoints int32 `json:"downloadFailedPoints,omitempty"`

	// DownloadMbps bands (higher is better) when download succeeded.
	// +optional
	DownloadMbps []LowerBoundBand `json:"downloadMbps,omitempty"`
}

// StorageTierBands holds fio bands for one storage tier (local|remote|object).
type StorageTierBands struct {
	// FioSeqWriteMBps — sequential write MiB/s (higher better).
	// +optional
	FioSeqWriteMBps []LowerBoundBand `json:"fioSeqWriteMBps,omitempty"`

	// FioSeqReadMBps — sequential read MiB/s (higher better).
	// +optional
	FioSeqReadMBps []LowerBoundBand `json:"fioSeqReadMBps,omitempty"`

	// FioRandReadIOPS — random read IOPS (higher better).
	// +optional
	FioRandReadIOPS []LowerBoundBand `json:"fioRandReadIOPS,omitempty"`

	// FioRandWriteIOPS — random write IOPS (higher better).
	// +optional
	FioRandWriteIOPS []LowerBoundBand `json:"fioRandWriteIOPS,omitempty"`
}

// StorageBands scores disks by tier.
type StorageBands struct {
	// DefaultTier used when a StorageClass is not listed in storageClassTiers.
	// +kubebuilder:default=remote
	// +kubebuilder:validation:Enum=local;remote;object
	// +optional
	DefaultTier string `json:"defaultTier,omitempty"`

	// StorageClassTiers maps StorageClass name → tier id (local|remote|object).
	// +optional
	StorageClassTiers map[string]string `json:"storageClassTiers,omitempty"`

	// Tiers holds per-tier fio bands. Keys: local, remote, object.
	// +optional
	Tiers map[string]StorageTierBands `json:"tiers,omitempty"`
}

// ScoreModules is the modular bracket set keyed to suite modules.
type ScoreModules struct {
	// +optional
	Responsive *ResponsiveBands `json:"responsive,omitempty"`
	// +optional
	Packages *PackagesBands `json:"packages,omitempty"`
	// +optional
	Fileops *FileopsBands `json:"fileops,omitempty"`
	// +optional
	Storage *StorageBands `json:"storage,omitempty"`
	// +optional
	Network *NetworkBands `json:"network,omitempty"`
}

// VerdictBand maps a total score range to a verdict string.
// First band where total <= Max matches (ascending Max); omit Max for catch-all.
type VerdictBand struct {
	// Max inclusive total for this verdict. Omit for +Inf.
	// +optional
	Max *int32 `json:"max,omitempty"`

	// Name of the verdict (e.g. excellent, good, fair, below-average, poor).
	Name string `json:"name"`
}

// CanarioScoreSpec defines scoring brackets for suite modules.
type CanarioScoreSpec struct {
	// Modules holds per-module bands. Omitted modules contribute 0.
	// +optional
	Modules ScoreModules `json:"modules,omitempty"`

	// Verdicts maps total points → verdict name (lower total is better).
	// +optional
	Verdicts []VerdictBand `json:"verdicts,omitempty"`
}

// CanarioScoreStatus is unused in v1 (policy object).
type CanarioScoreStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cscore
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CanarioScore is a reusable scoring policy for CanarioRuns.
type CanarioScore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CanarioScoreSpec   `json:"spec,omitempty"`
	Status CanarioScoreStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CanarioScoreList contains a list of CanarioScore.
type CanarioScoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CanarioScore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CanarioScore{}, &CanarioScoreList{})
}
