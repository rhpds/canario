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

package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

// CanarioRunReconciler reconciles CanarioRun objects.
type CanarioRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile advances a CanarioRun: Pending → Job → Running → Succeeded/Failed.
// The Job is a placeholder (echo); the real guest probe image comes later.
func (r *CanarioRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	run := &canariov1alpha1.CanarioRun{}
	if err := r.Get(ctx, req.NamespacedName, run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("reconciling CanarioRun",
		"type", run.Spec.Type,
		"profile", run.Spec.Profile,
		"workers", run.Spec.Workers,
		"storageClass", run.Spec.StorageClass,
		"phase", run.Status.Phase,
	)

	if isTerminal(run.Status.Phase) {
		return ctrl.Result{}, nil
	}

	if run.Status.Phase == "" {
		if err := r.setPhase(ctx, run, canariov1alpha1.PhasePending, "accepted"); err != nil {
			log.Error(err, "failed to update status to Pending")
			return ctrl.Result{}, err
		}
	}

	job, err := r.ensureProbeJob(ctx, run)
	if err != nil {
		log.Error(err, "failed to ensure probe Job")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.syncStatusFromJob(ctx, run, job)
}

func (r *CanarioRunReconciler) ensureProbeJob(ctx context.Context, run *canariov1alpha1.CanarioRun) (*batchv1.Job, error) {
	log := logf.FromContext(ctx)
	jobName := probeJobName(run)

	job := &batchv1.Job{}
	err := r.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: jobName}, job)
	if err == nil {
		return job, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	backoff := int32(0)
	job = &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: run.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "canario",
				"app.kubernetes.io/component": "probe",
				"canario.rhpds.com/run":       run.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":      "canario",
						"app.kubernetes.io/component": "probe",
						"canario.rhpds.com/run":       run.Name,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  "probe",
						Image: "registry.access.redhat.com/ubi9/ubi-minimal:latest",
						Command: []string{
							"/bin/sh", "-c",
							fmt.Sprintf(
								"echo 'canario placeholder type=%s profile=%s workers=%s'; sleep 5",
								run.Spec.Type, run.Spec.Profile, run.Spec.Workers,
							),
						},
					}},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(run, job, r.Scheme); err != nil {
		return nil, err
	}
	if err := r.Create(ctx, job); err != nil {
		return nil, err
	}
	log.Info("created probe Job", "job", jobName)
	return job, nil
}

func (r *CanarioRunReconciler) syncStatusFromJob(ctx context.Context, run *canariov1alpha1.CanarioRun, job *batchv1.Job) error {
	switch {
	case job.Status.Succeeded > 0:
		return r.setPhase(ctx, run, canariov1alpha1.PhaseSucceeded, "probe Job completed")
	case jobFailed(job):
		return r.setPhase(ctx, run, canariov1alpha1.PhaseFailed, "probe Job failed")
	case run.Status.Phase != canariov1alpha1.PhaseRunning:
		return r.setPhase(ctx, run, canariov1alpha1.PhaseRunning, fmt.Sprintf("probe Job %s running", job.Name))
	default:
		return nil
	}
}

func (r *CanarioRunReconciler) setPhase(ctx context.Context, run *canariov1alpha1.CanarioRun, phase, message string) error {
	if run.Status.Phase == phase && run.Status.Message == message {
		return nil
	}
	run.Status.Phase = phase
	run.Status.Message = message
	return r.Status().Update(ctx, run)
}

func probeJobName(run *canariov1alpha1.CanarioRun) string {
	return run.Name + "-probe"
}

func isTerminal(phase string) bool {
	return phase == canariov1alpha1.PhaseSucceeded || phase == canariov1alpha1.PhaseFailed
}

func jobFailed(job *batchv1.Job) bool {
	if job.Status.Failed > 0 {
		return true
	}
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// SetupWithManager registers this controller with the manager.
func (r *CanarioRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&canariov1alpha1.CanarioRun{}).
		Owns(&batchv1.Job{}).
		Named("canariorun").
		Complete(r)
}
