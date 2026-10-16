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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

const (
	virtUnavailableMessage = "OpenShift Virtualization not available (VirtualMachine API missing)"
	suitePollInterval      = 20 * time.Second
)

// CanarioRunReconciler reconciles CanarioRun objects.
type CanarioRunReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	RESTMapper meta.RESTMapper
	Config     *rest.Config

	// GuestDefaults are install-time boot defaults (from CANARIO_DEFAULT_GUEST_*).
	GuestDefaults OperatorGuestDefaults

	// VirtAvailable overrides API discovery when set (used by unit tests).
	VirtAvailable func(ctx context.Context) (bool, error)

	// SyncCioVM overrides CioVM ensure when set (used by unit tests).
	SyncCioVM func(ctx context.Context, run *canariov1alpha1.CanarioRun) (cioVMStatus, error)

	// ReadSuite overrides guest-agent suite readout when set (used by unit tests).
	ReadSuite func(ctx context.Context, namespace, vmName string) (*suiteSnapshot, error)
}

// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachineinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cdi.kubevirt.io,resources=datavolumes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cdi.kubevirt.io,resources=datasources,verbs=get;list;watch
// +kubebuilder:rbac:groups=cdi.kubevirt.io,resources=storageprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods/exec,verbs=create
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile: Pending → CioVM → suite results on status → Succeeded/Failed → cleanup CioVM.
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
		"storage", run.Spec.Storage,
		"storageClasses", run.Spec.StorageClasses,
		"phase", run.Status.Phase,
		"suiteStatus", run.Status.SuiteStatus,
	)

	if isTerminal(run.Status.Phase) {
		return ctrl.Result{}, nil
	}

	if run.Status.Phase == "" {
		if err := r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhasePending
			s.Message = "accepted"
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	if needsVirt(run) {
		ok, err := r.virtAvailable(ctx)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !ok {
			return ctrl.Result{}, r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
				s.Phase = canariov1alpha1.PhaseFailed
				s.Message = virtUnavailableMessage
			})
		}
	}

	vmStatus, err := r.syncCioVM(ctx, run)
	if err != nil {
		log.Error(err, "failed to sync CioVM")
		return ctrl.Result{}, err
	}

	return r.advanceFromCioVM(ctx, run, vmStatus)
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

func (r *CanarioRunReconciler) advanceFromCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun, vmStatus cioVMStatus) (ctrl.Result, error) {
	switch vmStatus.Phase {
	case cioVMPhaseFailed:
		return ctrl.Result{}, r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseFailed
			s.Message = vmStatus.Message
			s.CioVMName = vmStatus.Name
			s.NodeName = vmStatus.NodeName
		})
	case cioVMPhaseReady:
		if err := r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseRunning
			s.CioVMName = vmStatus.Name
			s.NodeName = vmStatus.NodeName
			if s.SuiteStatus == "" {
				s.SuiteStatus = "running"
				s.Message = "suite running in guest"
			}
		}); err != nil {
			return ctrl.Result{}, err
		}
		return r.collectSuiteAndCleanup(ctx, run, vmStatus.Name)
	default:
		if err := r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseRunning
			s.Message = vmStatus.Message
			s.CioVMName = vmStatus.Name
			s.NodeName = vmStatus.NodeName
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
}

func (r *CanarioRunReconciler) collectSuiteAndCleanup(ctx context.Context, run *canariov1alpha1.CanarioRun, vmName string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if vmName == "" {
		return ctrl.Result{RequeueAfter: suitePollInterval}, nil
	}

	snap, err := r.readSuite(ctx, run.Namespace, vmName)
	if err != nil {
		log.Info("suite not readable yet", "error", err.Error())
		return ctrl.Result{RequeueAfter: suitePollInterval}, nil
	}

	if snap.Done == "" && snap.Status != "succeeded" && snap.Status != "failed" {
		_ = r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseRunning
			s.SuiteStatus = "running"
			s.Message = "suite running in guest"
		})
		return ctrl.Result{RequeueAfter: suitePollInterval}, nil
	}

	phase := canariov1alpha1.PhaseSucceeded
	msg := "suite succeeded; cleaned up CioVM"
	if snap.Status == "failed" {
		phase = canariov1alpha1.PhaseFailed
		msg = "suite failed; cleaned up CioVM"
	}
	for _, d := range snap.Disks {
		if d.SuiteStatus == "failed" {
			phase = canariov1alpha1.PhaseFailed
			msg = "suite failed; cleaned up CioVM"
			break
		}
	}

	completed := metav1.Now()
	if snap.Done != "" {
		if t, err := time.Parse(time.RFC3339, snap.Done); err == nil {
			completed = metav1.NewTime(t)
		}
	}

	disks := snap.Disks
	for i := range disks {
		if disks[i].NodeName == "" {
			disks[i].NodeName = run.Status.NodeName
		}
	}

	if err := r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
		s.Phase = phase
		s.Message = msg
		s.SuiteStatus = snap.Status
		s.Disks = disks
		s.CompletionTime = &completed
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.deleteCioVM(ctx, run); err != nil {
		log.Error(err, "failed to delete CioVM after suite")
		return ctrl.Result{}, err
	}
	log.Info("suite finished; CioVM deleted", "phase", phase, "suiteStatus", snap.Status, "disks", len(disks))
	return ctrl.Result{}, nil
}

func (r *CanarioRunReconciler) patchStatus(ctx context.Context, run *canariov1alpha1.CanarioRun, mutate func(*canariov1alpha1.CanarioRunStatus)) error {
	// Re-fetch to reduce conflicts
	latest := &canariov1alpha1.CanarioRun{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(run), latest); err != nil {
		return err
	}
	mutate(&latest.Status)
	if err := r.Status().Update(ctx, latest); err != nil {
		return err
	}
	latest.Status.DeepCopyInto(&run.Status)
	return nil
}

func isTerminal(phase string) bool {
	return phase == canariov1alpha1.PhaseSucceeded || phase == canariov1alpha1.PhaseFailed
}

// SetupWithManager registers this controller with the manager.
func (r *CanarioRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Config == nil {
		r.Config = mgr.GetConfig()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&canariov1alpha1.CanarioRun{}).
		Owns(newVirtualMachineForWatch()).
		Named("canariorun").
		Complete(r)
}
