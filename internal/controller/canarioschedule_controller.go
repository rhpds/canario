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

package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

const (
	defaultHistoryLimit int32 = 5
	runNameTimeLayout         = "20060102t150405z"
)

// CanarioScheduleReconciler reconciles CanarioSchedule objects.
type CanarioScheduleReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// Now is overridden in tests (UTC).
	Now func() time.Time
}

// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioschedules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioschedules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioschedules/finalizers,verbs=update
// +kubebuilder:rbac:groups=canario.rhpds.com,resources=canarioruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *CanarioScheduleReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *CanarioScheduleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	sched := &canariov1alpha1.CanarioSchedule{}
	if err := r.Get(ctx, req.NamespacedName, sched); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cronSched, err := parseUTCCron(sched.Spec.Schedule)
	if err != nil {
		log.Error(err, "invalid cron schedule", "schedule", sched.Spec.Schedule)
		return ctrl.Result{}, err
	}

	runs, err := r.listOwnedRuns(ctx, sched)
	if err != nil {
		return ctrl.Result{}, err
	}

	active := findActiveRun(runs)
	activeName := ""
	if active != nil {
		activeName = active.Name
	}
	if err := r.patchStatus(ctx, sched, func(s *canariov1alpha1.CanarioScheduleStatus) {
		s.ActiveRunName = activeName
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.pruneHistory(ctx, sched, runs); err != nil {
		return ctrl.Result{}, err
	}

	if sched.Spec.Suspend {
		next := cronSched.Next(r.now())
		return ctrl.Result{RequeueAfter: requeueDelay(r.now(), next)}, nil
	}

	last := time.Time{}
	if sched.Status.LastScheduleTime != nil {
		last = sched.Status.LastScheduleTime.Time.UTC()
	} else if !sched.CreationTimestamp.IsZero() {
		// Missed fires before create are ignored; first eligible is next after creation.
		last = sched.CreationTimestamp.Time.UTC()
	} else {
		last = r.now().Add(-time.Second)
	}

	next := cronSched.Next(last)
	now := r.now()
	if now.Before(next) {
		return ctrl.Result{RequeueAfter: requeueDelay(now, next)}, nil
	}

	// Due: Forbid if active.
	if active != nil {
		reason := canariov1alpha1.SkipReasonActive
		if r.Recorder != nil {
			r.Recorder.Eventf(sched, corev1.EventTypeWarning, "Skipped",
				"skipped schedule fire: %s (%s)", reason, active.Name)
		}
		skipTime := metav1.NewTime(now)
		if err := r.patchStatus(ctx, sched, func(s *canariov1alpha1.CanarioScheduleStatus) {
			s.LastSkipTime = &skipTime
			s.LastSkipReason = reason
			s.ActiveRunName = active.Name
			// Advance lastScheduleTime so we don't tight-loop on the same fire.
			s.LastScheduleTime = &skipTime
		}); err != nil {
			return ctrl.Result{}, err
		}
		nextAfterSkip := cronSched.Next(now)
		return ctrl.Result{RequeueAfter: requeueDelay(now, nextAfterSkip)}, nil
	}

	runName := fmt.Sprintf("%s-%s", sched.Name, now.Format(runNameTimeLayout))
	if len(runName) > 63 {
		runName = runName[:63]
		for len(runName) > 0 && runName[len(runName)-1] == '-' {
			runName = runName[:len(runName)-1]
		}
	}

	run := &canariov1alpha1.CanarioRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runName,
			Namespace: sched.Namespace,
			Labels: map[string]string{
				canariov1alpha1.LabelSchedule: sched.Name,
			},
		},
		Spec: *sched.Spec.Template.Spec.DeepCopy(),
	}
	if err := controllerutil.SetControllerReference(sched, run, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, run); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Same-second collision; bump and retry soon.
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	fireTime := metav1.NewTime(now)
	if err := r.patchStatus(ctx, sched, func(s *canariov1alpha1.CanarioScheduleStatus) {
		s.LastScheduleTime = &fireTime
		s.LastRunName = run.Name
		s.ActiveRunName = run.Name
		s.LastSkipReason = ""
	}); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("created CanarioRun from schedule", "run", run.Name)
	nextAfter := cronSched.Next(now)
	return ctrl.Result{RequeueAfter: requeueDelay(now, nextAfter)}, nil
}

func parseUTCCron(expr string) (cron.Schedule, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	return parser.Parse(expr)
}

func requeueDelay(now, next time.Time) time.Duration {
	d := next.Sub(now)
	if d < time.Second {
		return time.Second
	}
	// Cap requeue so status/active checks stay reasonably fresh.
	if d > time.Hour {
		return time.Hour
	}
	return d
}

func (r *CanarioScheduleReconciler) listOwnedRuns(ctx context.Context, sched *canariov1alpha1.CanarioSchedule) ([]canariov1alpha1.CanarioRun, error) {
	list := &canariov1alpha1.CanarioRunList{}
	if err := r.List(ctx, list,
		client.InNamespace(sched.Namespace),
		client.MatchingLabels{canariov1alpha1.LabelSchedule: sched.Name},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func findActiveRun(runs []canariov1alpha1.CanarioRun) *canariov1alpha1.CanarioRun {
	for i := range runs {
		ph := runs[i].Status.Phase
		if ph == "" || ph == canariov1alpha1.PhasePending || ph == canariov1alpha1.PhaseRunning {
			return &runs[i]
		}
	}
	return nil
}

func historyLimit(sched *canariov1alpha1.CanarioSchedule) int {
	if sched.Spec.HistoryLimit == nil {
		return int(defaultHistoryLimit)
	}
	return int(*sched.Spec.HistoryLimit)
}

func (r *CanarioScheduleReconciler) pruneHistory(ctx context.Context, sched *canariov1alpha1.CanarioSchedule, runs []canariov1alpha1.CanarioRun) error {
	limit := historyLimit(sched)
	var terminal []canariov1alpha1.CanarioRun
	for _, run := range runs {
		if isTerminal(run.Status.Phase) {
			terminal = append(terminal, run)
		}
	}
	if len(terminal) <= limit {
		return nil
	}
	sort.Slice(terminal, func(i, j int) bool {
		return terminal[i].CreationTimestamp.After(terminal[j].CreationTimestamp.Time)
	})
	for _, old := range terminal[limit:] {
		if err := r.Delete(ctx, &old); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *CanarioScheduleReconciler) patchStatus(ctx context.Context, sched *canariov1alpha1.CanarioSchedule, mutate func(*canariov1alpha1.CanarioScheduleStatus)) error {
	latest := &canariov1alpha1.CanarioSchedule{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(sched), latest); err != nil {
		return err
	}
	mutate(&latest.Status)
	if err := r.Status().Update(ctx, latest); err != nil {
		return err
	}
	latest.Status.DeepCopyInto(&sched.Status)
	return nil
}

// SetupWithManager registers this controller with the manager.
func (r *CanarioScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("canarioschedule")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&canariov1alpha1.CanarioSchedule{}).
		Owns(&canariov1alpha1.CanarioRun{}).
		Named("canarioschedule").
		Complete(r)
}
