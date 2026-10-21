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
	"testing"
	"time"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func scheduleScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := canariov1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func baseSchedule(name string, created time.Time) *canariov1alpha1.CanarioSchedule {
	return &canariov1alpha1.CanarioSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(created),
			UID:               "sched-uid",
		},
		Spec: canariov1alpha1.CanarioScheduleSpec{
			Schedule:          "*/5 * * * *",
			ConcurrencyPolicy: canariov1alpha1.ConcurrencyForbid,
			Template: canariov1alpha1.CanarioRunTemplate{
				Spec: canariov1alpha1.CanarioRunSpec{
					Type:           canariov1alpha1.RunTypeGuest,
					Profile:        canariov1alpha1.ProfileSmall,
					Workers:        canariov1alpha1.WorkersAll,
					Storage:        canariov1alpha1.StorageList,
					StorageClasses: []string{"ocs-rbd"},
				},
			},
		},
	}
}

func TestScheduleCreatesRunWhenDue(t *testing.T) {
	scheme := scheduleScheme(t)
	created := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 21, 12, 5, 1, 0, time.UTC)
	sched := baseSchedule("hourly", created)

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&canariov1alpha1.CanarioSchedule{}).
		WithObjects(sched).
		Build()

	r := &CanarioScheduleReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
		Now:      func() time.Time { return now },
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: sched.Name, Namespace: sched.Namespace},
	})
	if err != nil {
		t.Fatal(err)
	}

	runs := &canariov1alpha1.CanarioRunList{}
	if err := c.List(context.Background(), runs, client.InNamespace("default")); err != nil {
		t.Fatal(err)
	}
	if len(runs.Items) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs.Items))
	}
	run := runs.Items[0]
	if run.Labels[canariov1alpha1.LabelSchedule] != sched.Name {
		t.Fatalf("missing schedule label: %v", run.Labels)
	}
	if run.Spec.Profile != canariov1alpha1.ProfileSmall {
		t.Fatalf("spec not copied: %+v", run.Spec)
	}
	wantName := "hourly-" + now.Format(runNameTimeLayout)
	if run.Name != wantName {
		t.Fatalf("run name: got %q want %q", run.Name, wantName)
	}

	updated := &canariov1alpha1.CanarioSchedule{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(sched), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.LastRunName != run.Name {
		t.Fatalf("LastRunName=%q", updated.Status.LastRunName)
	}
	if updated.Status.ActiveRunName != run.Name {
		t.Fatalf("ActiveRunName=%q", updated.Status.ActiveRunName)
	}
}

func TestScheduleSkipsWhenActive(t *testing.T) {
	scheme := scheduleScheme(t)
	created := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 21, 12, 10, 1, 0, time.UTC)
	sched := baseSchedule("busy", created)
	last := metav1.NewTime(time.Date(2026, 10, 21, 12, 5, 0, 0, time.UTC))
	sched.Status.LastScheduleTime = &last

	active := &canariov1alpha1.CanarioRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "busy-active",
			Namespace: "default",
			Labels:    map[string]string{canariov1alpha1.LabelSchedule: sched.Name},
		},
		Status: canariov1alpha1.CanarioRunStatus{Phase: canariov1alpha1.PhaseRunning},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&canariov1alpha1.CanarioSchedule{}).
		WithObjects(sched, active).
		Build()

	rec := record.NewFakeRecorder(10)
	r := &CanarioScheduleReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: rec,
		Now:      func() time.Time { return now },
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: sched.Name, Namespace: sched.Namespace},
	})
	if err != nil {
		t.Fatal(err)
	}

	runs := &canariov1alpha1.CanarioRunList{}
	if err := c.List(context.Background(), runs, client.InNamespace("default")); err != nil {
		t.Fatal(err)
	}
	if len(runs.Items) != 1 {
		t.Fatalf("expected no new run, got %d total", len(runs.Items))
	}

	updated := &canariov1alpha1.CanarioSchedule{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(sched), updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.LastSkipReason != canariov1alpha1.SkipReasonActive {
		t.Fatalf("LastSkipReason=%q", updated.Status.LastSkipReason)
	}
	if updated.Status.LastSkipTime == nil {
		t.Fatal("expected LastSkipTime")
	}

	select {
	case e := <-rec.Events:
		if e == "" {
			t.Fatal("empty event")
		}
	default:
		t.Fatal("expected Warning Skipped event")
	}
}

func TestScheduleSuspendDoesNotCreate(t *testing.T) {
	scheme := scheduleScheme(t)
	created := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 21, 12, 5, 1, 0, time.UTC)
	sched := baseSchedule("paused", created)
	sched.Spec.Suspend = true

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&canariov1alpha1.CanarioSchedule{}).
		WithObjects(sched).
		Build()

	r := &CanarioScheduleReconciler{
		Client: c,
		Scheme: scheme,
		Now:    func() time.Time { return now },
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: sched.Name, Namespace: sched.Namespace},
	})
	if err != nil {
		t.Fatal(err)
	}

	runs := &canariov1alpha1.CanarioRunList{}
	if err := c.List(context.Background(), runs); err != nil {
		t.Fatal(err)
	}
	if len(runs.Items) != 0 {
		t.Fatalf("expected 0 runs, got %d", len(runs.Items))
	}
}

func TestSchedulePrunesHistory(t *testing.T) {
	scheme := scheduleScheme(t)
	created := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	// Not due yet — only prune path.
	now := time.Date(2026, 10, 21, 10, 1, 0, 0, time.UTC)
	limit := int32(2)
	sched := baseSchedule("prune", created)
	sched.Spec.HistoryLimit = &limit
	sched.Spec.Schedule = "0 0 * * *" // next fire far away

	mkRun := func(name string, age time.Duration, phase string) *canariov1alpha1.CanarioRun {
		return &canariov1alpha1.CanarioRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				Labels:            map[string]string{canariov1alpha1.LabelSchedule: sched.Name},
				CreationTimestamp: metav1.NewTime(now.Add(-age)),
			},
			Status: canariov1alpha1.CanarioRunStatus{Phase: phase},
		}
	}

	objs := []client.Object{
		sched,
		mkRun("old-1", 5*time.Hour, canariov1alpha1.PhaseSucceeded),
		mkRun("old-2", 4*time.Hour, canariov1alpha1.PhaseFailed),
		mkRun("old-3", 3*time.Hour, canariov1alpha1.PhaseSucceeded),
		mkRun("active", 1*time.Hour, canariov1alpha1.PhaseRunning),
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&canariov1alpha1.CanarioSchedule{}).
		WithObjects(objs...).
		Build()

	r := &CanarioScheduleReconciler{
		Client: c,
		Scheme: scheme,
		Now:    func() time.Time { return now },
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: sched.Name, Namespace: sched.Namespace},
	})
	if err != nil {
		t.Fatal(err)
	}

	runs := &canariov1alpha1.CanarioRunList{}
	if err := c.List(context.Background(), runs, client.InNamespace("default")); err != nil {
		t.Fatal(err)
	}
	// 2 newest terminal + 1 active = 3
	if len(runs.Items) != 3 {
		names := make([]string, 0, len(runs.Items))
		for _, r := range runs.Items {
			names = append(names, r.Name)
		}
		t.Fatalf("expected 3 runs kept, got %d %v", len(runs.Items), names)
	}
	kept := map[string]bool{}
	for _, r := range runs.Items {
		kept[r.Name] = true
	}
	if !kept["old-2"] || !kept["old-3"] || !kept["active"] {
		t.Fatalf("unexpected kept set: %v", kept)
	}
	if kept["old-1"] {
		t.Fatal("old-1 should have been pruned")
	}
}
