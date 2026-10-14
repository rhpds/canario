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

	PhasePending   = "Pending"
	PhaseRunning   = "Running"
	PhaseSucceeded = "Succeeded"
	PhaseFailed    = "Failed"
)

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

	// StorageClass for the guest VM disk. Empty uses the cluster default.
	StorageClass string `json:"storageClass,omitempty"`
}

// CanarioRunStatus is the observed state of a CanarioRun.
type CanarioRunStatus struct {
	// Phase is Pending, Running, Succeeded, or Failed.
	Phase string `json:"phase,omitempty"`

	// Message is a human-readable detail for the current phase.
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=`.spec.profile`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CanarioRun is a one-shot guest canary campaign.
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
