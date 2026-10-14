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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

const virtUnavailableMessage = "OpenShift Virtualization not available (VirtualMachine API missing)"

// CanarioRunReconciler reconciles CanarioRun objects.
type CanarioRunReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	RESTMapper meta.RESTMapper

	// VirtAvailable overrides API discovery when set (used by unit tests).
	VirtAvailable func(ctx context.Context) (bool, error)

	// SyncCioVM overrides CioVM ensure when set (used by unit tests).
	SyncCioVM func(ctx context.Context, run *canariov1alpha1.CanarioRun) (cioVMStatus, error)
}

// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachineinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cdi.kubevirt.io,resources=datavolumes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile advances a CanarioRun: Pending → create CioVM → Running (suite later).
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

	if needsVirt(run) {
		ok, err := r.virtAvailable(ctx)
		if err != nil {
			log.Error(err, "failed to check OpenShift Virtualization availability")
			return ctrl.Result{}, err
		}
		if !ok {
			log.Info("failing CanarioRun: OpenShift Virtualization unavailable")
			return ctrl.Result{}, r.setPhase(ctx, run, canariov1alpha1.PhaseFailed, virtUnavailableMessage)
		}
	}

	status, err := r.syncCioVM(ctx, run)
	if err != nil {
		log.Error(err, "failed to sync CioVM")
		return ctrl.Result{}, err
	}

	return r.syncStatusFromCioVM(ctx, run, status)
}

func (r *CanarioRunReconciler) syncCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun) (cioVMStatus, error) {
	if r.SyncCioVM != nil {
		return r.SyncCioVM(ctx, run)
	}
	return r.ensureCioVM(ctx, run)
}

func needsVirt(run *canariov1alpha1.CanarioRun) bool {
	return run.Spec.Type == "" || run.Spec.Type == canariov1alpha1.RunTypeGuest
}

func (r *CanarioRunReconciler) virtAvailable(ctx context.Context) (bool, error) {
	if r.VirtAvailable != nil {
		return r.VirtAvailable(ctx)
	}
	if r.RESTMapper == nil {
		return false, fmt.Errorf("RESTMapper not configured")
	}
	_, err := r.RESTMapper.RESTMapping(
		schema.GroupKind{Group: "kubevirt.io", Kind: "VirtualMachine"},
		"v1",
	)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *CanarioRunReconciler) syncStatusFromCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun, status cioVMStatus) (ctrl.Result, error) {
	switch status.Phase {
	case cioVMPhaseFailed:
		return ctrl.Result{}, r.setPhase(ctx, run, canariov1alpha1.PhaseFailed, status.Message)
	case cioVMPhaseReady:
		// Suite / cleanup come next; keep Running while CioVM is up.
		if err := r.setPhase(ctx, run, canariov1alpha1.PhaseRunning, status.Message); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	default:
		if err := r.setPhase(ctx, run, canariov1alpha1.PhaseRunning, status.Message); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
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

func isTerminal(phase string) bool {
	return phase == canariov1alpha1.PhaseSucceeded || phase == canariov1alpha1.PhaseFailed
}

// SetupWithManager registers this controller with the manager.
func (r *CanarioRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&canariov1alpha1.CanarioRun{}).
		Owns(newVirtualMachineForWatch()).
		Named("canariorun").
		Complete(r)
}
