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
	// LabelSchedule links a CanarioRun to its CanarioSchedule.
	LabelSchedule = "canario.rhpds.com/schedule"

	ConcurrencyForbid = "Forbid"

	// SkipReasonActive is recorded when a fire is skipped because a Run is still active.
	SkipReasonActive = "previous run still active"
)

// CanarioRunTemplate is the Run blueprint created on each schedule fire.
type CanarioRunTemplate struct {
	// Spec is copied onto each created CanarioRun.
	Spec CanarioRunSpec `json:"spec"`
}

// CanarioScheduleSpec defines the desired periodic Run creation policy.
type CanarioScheduleSpec struct {
	// Schedule is a standard 5-field cron expression evaluated in UTC
	// (minute hour dom month dow), e.g. "0 */6 * * *".
	// +kubebuilder:validation:MinLength=1
	Schedule string `json:"schedule"`

	// Suspend stops creating new Runs when true.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// ConcurrencyPolicy controls overlapping Runs. v1 only supports Forbid
	// (skip the fire if a previous Run is still active).
	// +kubebuilder:validation:Enum=Forbid
	// +kubebuilder:default=Forbid
	// +optional
	ConcurrencyPolicy string `json:"concurrencyPolicy,omitempty"`

	// HistoryLimit is how many terminal Runs owned by this Schedule to keep
	// (newest retained). Default 5.
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=0
	// +optional
	HistoryLimit *int32 `json:"historyLimit,omitempty"`

	// Template is the CanarioRun spec created on each successful fire.
	Template CanarioRunTemplate `json:"template"`
}

// CanarioScheduleStatus is the observed state of a CanarioSchedule.
type CanarioScheduleStatus struct {
	// LastScheduleTime is when the last Run was successfully created (UTC).
	// +optional
	LastScheduleTime *metav1.Time `json:"lastScheduleTime,omitempty"`

	// LastRunName is the name of the last created CanarioRun.
	// +optional
	LastRunName string `json:"lastRunName,omitempty"`

	// ActiveRunName is a non-terminal Run for this Schedule, if any.
	// +optional
	ActiveRunName string `json:"activeRunName,omitempty"`

	// LastSkipTime is when a fire was last skipped.
	// +optional
	LastSkipTime *metav1.Time `json:"lastSkipTime,omitempty"`

	// LastSkipReason explains the last skip (e.g. previous run still active).
	// +optional
	LastSkipReason string `json:"lastSkipReason,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=csched
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Suspend",type=boolean,JSONPath=`.spec.suspend`
// +kubebuilder:printcolumn:name="LastRun",type=string,JSONPath=`.status.lastRunName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CanarioSchedule periodically creates CanarioRuns from a template.
type CanarioSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CanarioScheduleSpec   `json:"spec,omitempty"`
	Status CanarioScheduleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CanarioScheduleList contains a list of CanarioSchedule.
type CanarioScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CanarioSchedule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CanarioSchedule{}, &CanarioScheduleList{})
}
