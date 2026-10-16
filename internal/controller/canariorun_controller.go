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

	// Thresholds are install-time node/storage usage ceilings (from CANARIO_*_USAGE_CEILING).
	Thresholds OperatorThresholds

	// VirtAvailable overrides API discovery when set (used by unit tests).
	VirtAvailable func(ctx context.Context) (bool, error)

	// SyncCioVM overrides CioVM ensure when set (used by unit tests; single-worker).
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
// +kubebuilder:rbac:groups=ceph.rook.io,resources=cephclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods/exec,verbs=create
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile: Pending → staggered CioVMs → suite results on status.workers → Succeeded/Failed → cleanup.
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

	// Unit-test hook: single CioVM path.
	if r.SyncCioVM != nil {
		vmStatus, err := r.SyncCioVM(ctx, run)
		if err != nil {
			log.Error(err, "failed to sync CioVM")
			return ctrl.Result{}, err
		}
		return r.advanceFromSingleCioVM(ctx, run, vmStatus)
	}

	return r.reconcileWorkers(ctx, run)
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

// reconcileWorkers fans out CioVMs (staggered create), collects per-worker suites, then cleans up.
func (r *CanarioRunReconciler) reconcileWorkers(ctx context.Context, run *canariov1alpha1.CanarioRun) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	disks, err := r.resolveDataDisks(ctx, run)
	if err != nil {
		return ctrl.Result{}, r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseFailed
			s.Message = "storage: " + err.Error()
		})
	}

	targets, err := r.resolveWorkerNodes(ctx, run, disks)
	if err != nil {
		return ctrl.Result{}, r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseFailed
			s.Message = "workers: " + err.Error()
		})
	}

	boot, err := resolveBootSource(run, r.GuestDefaults)
	if err != nil {
		return ctrl.Result{}, r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseFailed
			s.Message = "guestImage: " + err.Error()
		})
	}
	if err := r.ensureBootSourceReady(ctx, boot); err != nil {
		return ctrl.Result{}, r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseFailed
			s.Message = "guestImage: " + err.Error()
		})
	}

	staggerRequeue, err := r.ensureCioVMsStaggered(ctx, run, targets, disks, boot)
	if err != nil {
		log.Error(err, "failed to ensure CioVMs")
		return ctrl.Result{}, err
	}

	byNode, err := r.cioVMsByNode(ctx, run)
	if err != nil {
		return ctrl.Result{}, err
	}

	prevByNode := workerByNode(run.Status.Workers)
	workers := make([]canariov1alpha1.WorkerResult, 0, len(targets))
	allTerminal := true
	anyFailed := false
	var completion *metav1.Time

	for _, node := range targets {
		wr := prevByNode[node]
		wr.NodeName = node

		if isTerminal(wr.Phase) {
			workers = append(workers, wr)
			if wr.Phase == canariov1alpha1.PhaseFailed {
				anyFailed = true
			}
			continue
		}

		vm := byNode[node]
		if vm == nil {
			wr.Phase = canariov1alpha1.PhasePending
			wr.Message = "waiting to create CioVM"
			allTerminal = false
			workers = append(workers, wr)
			continue
		}

		obs := observeCioVM(vm)
		wr.CioVMName = obs.Name

		switch obs.Phase {
		case cioVMPhaseFailed:
			wr.Phase = canariov1alpha1.PhaseFailed
			wr.Message = obs.Message
			anyFailed = true
		case cioVMPhaseReady:
			done, failed, snap, err := r.collectWorkerSuite(ctx, run.Namespace, obs.Name, node)
			if err != nil {
				log.Info("suite not readable yet", "node", node, "vm", obs.Name, "error", err.Error())
				wr.Phase = canariov1alpha1.PhaseRunning
				wr.SuiteStatus = "running"
				wr.Message = "suite running in guest"
				allTerminal = false
			} else if !done {
				wr.Phase = canariov1alpha1.PhaseRunning
				wr.SuiteStatus = "running"
				wr.Message = "suite running in guest"
				allTerminal = false
			} else {
				wr.SuiteStatus = snap.Status
				wr.Guest = snap.Guest
				wr.Disks = snap.Disks
				if failed {
					wr.Phase = canariov1alpha1.PhaseFailed
					wr.Message = "suite failed"
					anyFailed = true
				} else {
					wr.Phase = canariov1alpha1.PhaseSucceeded
					wr.Message = "suite succeeded"
				}
				if snap.Done != "" {
					if t, err := time.Parse(time.RFC3339, snap.Done); err == nil {
						ct := metav1.NewTime(t)
						if completion == nil || ct.After(completion.Time) {
							completion = &ct
						}
					}
				}
			}
		default:
			wr.Phase = canariov1alpha1.PhaseRunning
			wr.Message = obs.Message
			allTerminal = false
		}
		workers = append(workers, wr)
	}

	if !allTerminal {
		if err := r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseRunning
			s.SuiteStatus = "running"
			s.Message = fmt.Sprintf("experiment running (%d workers)", len(targets))
			s.Workers = workers
		}); err != nil {
			return ctrl.Result{}, err
		}
		requeue := suitePollInterval
		if staggerRequeue > 0 && staggerRequeue < requeue {
			requeue = staggerRequeue
		}
		return ctrl.Result{RequeueAfter: requeue}, nil
	}

	phase := canariov1alpha1.PhaseSucceeded
	suite := "succeeded"
	msg := "all workers succeeded; cleaned up CioVMs"
	if anyFailed {
		phase = canariov1alpha1.PhaseFailed
		suite = "failed"
		msg = "one or more workers failed; cleaned up CioVMs"
	}
	if completion == nil {
		now := metav1.Now()
		completion = &now
	}

	if err := r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
		s.Phase = phase
		s.SuiteStatus = suite
		s.Message = msg
		s.Workers = workers
		s.CompletionTime = completion
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.deleteCioVM(ctx, run); err != nil {
		log.Error(err, "failed to delete CioVMs after suite")
		return ctrl.Result{}, err
	}
	log.Info("experiment finished; CioVMs deleted", "phase", phase, "workers", len(workers))
	return ctrl.Result{}, nil
}

func workerByNode(workers []canariov1alpha1.WorkerResult) map[string]canariov1alpha1.WorkerResult {
	m := make(map[string]canariov1alpha1.WorkerResult, len(workers))
	for _, w := range workers {
		if w.NodeName != "" {
			m[w.NodeName] = w
		}
	}
	return m
}

// collectWorkerSuite returns (done, failed, snap, err). err means not readable yet.
func (r *CanarioRunReconciler) collectWorkerSuite(ctx context.Context, namespace, vmName, nodeName string) (bool, bool, *suiteSnapshot, error) {
	snap, err := r.readSuite(ctx, namespace, vmName)
	if err != nil {
		return false, false, nil, err
	}
	if snap.Done == "" && snap.Status != "succeeded" && snap.Status != "failed" {
		return false, false, snap, nil
	}
	failed := snap.Status == "failed"
	for i := range snap.Disks {
		if snap.Disks[i].NodeName == "" {
			snap.Disks[i].NodeName = nodeName
		}
		if snap.Disks[i].SuiteStatus == "failed" {
			failed = true
		}
	}
	return true, failed, snap, nil
}

// advanceFromSingleCioVM is the SyncCioVM unit-test path (one synthetic worker).
func (r *CanarioRunReconciler) advanceFromSingleCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun, vmStatus cioVMStatus) (ctrl.Result, error) {
	node := vmStatus.NodeName
	if node == "" {
		node = "worker"
	}

	switch vmStatus.Phase {
	case cioVMPhaseFailed:
		return ctrl.Result{}, r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseFailed
			s.Message = vmStatus.Message
			s.Workers = []canariov1alpha1.WorkerResult{{
				NodeName:  node,
				CioVMName: vmStatus.Name,
				Phase:     canariov1alpha1.PhaseFailed,
				Message:   vmStatus.Message,
			}}
		})
	case cioVMPhaseReady:
		if err := r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseRunning
			if s.SuiteStatus == "" {
				s.SuiteStatus = "running"
				s.Message = "suite running in guest"
			}
			s.Workers = []canariov1alpha1.WorkerResult{{
				NodeName:    node,
				CioVMName:   vmStatus.Name,
				Phase:       canariov1alpha1.PhaseRunning,
				SuiteStatus: "running",
				Message:     "suite running in guest",
			}}
		}); err != nil {
			return ctrl.Result{}, err
		}
		return r.collectSingleAndCleanup(ctx, run, vmStatus.Name, node)
	default:
		if err := r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseRunning
			s.Message = vmStatus.Message
			s.Workers = []canariov1alpha1.WorkerResult{{
				NodeName:  node,
				CioVMName: vmStatus.Name,
				Phase:     canariov1alpha1.PhaseRunning,
				Message:   vmStatus.Message,
			}}
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
}

func (r *CanarioRunReconciler) collectSingleAndCleanup(ctx context.Context, run *canariov1alpha1.CanarioRun, vmName, nodeName string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if vmName == "" {
		return ctrl.Result{RequeueAfter: suitePollInterval}, nil
	}

	done, failed, snap, err := r.collectWorkerSuite(ctx, run.Namespace, vmName, nodeName)
	if err != nil {
		log.Info("suite not readable yet", "error", err.Error())
		return ctrl.Result{RequeueAfter: suitePollInterval}, nil
	}
	if !done {
		_ = r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
			s.Phase = canariov1alpha1.PhaseRunning
			s.SuiteStatus = "running"
			s.Message = "suite running in guest"
		})
		return ctrl.Result{RequeueAfter: suitePollInterval}, nil
	}

	phase := canariov1alpha1.PhaseSucceeded
	msg := "suite succeeded; cleaned up CioVM"
	suite := snap.Status
	workerPhase := canariov1alpha1.PhaseSucceeded
	if failed {
		phase = canariov1alpha1.PhaseFailed
		msg = "suite failed; cleaned up CioVM"
		suite = "failed"
		workerPhase = canariov1alpha1.PhaseFailed
	}

	completed := metav1.Now()
	if snap.Done != "" {
		if t, err := time.Parse(time.RFC3339, snap.Done); err == nil {
			completed = metav1.NewTime(t)
		}
	}

	if err := r.patchStatus(ctx, run, func(s *canariov1alpha1.CanarioRunStatus) {
		s.Phase = phase
		s.Message = msg
		s.SuiteStatus = suite
		s.CompletionTime = &completed
		s.Workers = []canariov1alpha1.WorkerResult{{
			NodeName:    nodeName,
			CioVMName:   vmName,
			Phase:       workerPhase,
			SuiteStatus: suite,
			Message:     msg,
			Guest:       snap.Guest,
			Disks:       snap.Disks,
		}}
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.deleteCioVM(ctx, run); err != nil {
		log.Error(err, "failed to delete CioVM after suite")
		return ctrl.Result{}, err
	}
	log.Info("suite finished; CioVM deleted", "phase", phase, "suiteStatus", suite)
	return ctrl.Result{}, nil
}

func (r *CanarioRunReconciler) patchStatus(ctx context.Context, run *canariov1alpha1.CanarioRun, mutate func(*canariov1alpha1.CanarioRunStatus)) error {
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
