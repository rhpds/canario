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

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestShortUID(t *testing.T) {
	t.Parallel()
	if got := ShortUID("a1b2c3d4-e5f6-7890-abcd-ef1234567890"); got != "a1b2c3d4" {
		t.Fatalf("ShortUID=%q want a1b2c3d4", got)
	}
	if got := ShortUID(""); got != "00000000" {
		t.Fatalf("empty ShortUID=%q", got)
	}
}

func TestRecordAndDeleteRun(t *testing.T) {
	now := metav1.Now()
	run := &canariov1alpha1.CanarioRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bench-metrics-ut",
			Namespace: "canario-test",
			UID:       types.UID("a1b2c3d4-e5f6-7890-abcd-ef1234567890"),
		},
		Spec: canariov1alpha1.CanarioRunSpec{
			Profile: canariov1alpha1.ProfileSmall,
		},
		Status: canariov1alpha1.CanarioRunStatus{
			Phase:          canariov1alpha1.PhaseSucceeded,
			CompletionTime: &now,
			Workers: []canariov1alpha1.WorkerResult{{
				NodeName: "worker-a",
				Guest: &canariov1alpha1.GuestResult{
					CmdLsEtcMs:        "42.5",
					DnfInstallMs:      "3500",
					NetPingAvgMs:      "12",
					NetPingStatus:     "ok",
					NetDownloadStatus: "failed",
					GitCloneStatus:    "skipped",
				},
				Disks: []canariov1alpha1.DiskResult{{
					StorageClass:    "ocs-storagecluster-ceph-rbd",
					FioSeqWriteBW:   "BW=10MiB/s",
					FioRandReadIOPS: "IOPS=1.5k",
					FileopsCreateMs: "200",
				}},
			}},
		},
	}

	RecordRun(run)

	uid := "a1b2c3d4"
	if got := gaugeValue(t, "canario_run_info", map[string]string{
		"run": "bench-metrics-ut", "uid": uid, "profile": "small", "phase": "Succeeded",
	}); got != 1 {
		t.Fatalf("canario_run_info=%v want 1", got)
	}
	if got := gaugeValue(t, "canario_guest_cmd_ls_etc_ms", map[string]string{
		"run": "bench-metrics-ut", "uid": uid, "profile": "small", "node": "worker-a",
	}); got != 42.5 {
		t.Fatalf("cmd_ls_etc_ms=%v want 42.5", got)
	}
	if got := gaugeValue(t, "canario_guest_check_ok", map[string]string{
		"run": "bench-metrics-ut", "uid": uid, "profile": "small", "node": "worker-a", "check": "ping",
	}); got != 1 {
		t.Fatalf("check_ok ping=%v want 1", got)
	}
	wantBW := 10 * 1024 * 1024.0
	if got := gaugeValue(t, "canario_disk_fio_seq_write_bytes_per_second", map[string]string{
		"run": "bench-metrics-ut", "uid": uid, "profile": "small",
		"node": "worker-a", "storage_class": "ocs-storagecluster-ceph-rbd",
	}); got != wantBW {
		t.Fatalf("seq write bps=%v want %v", got, wantBW)
	}

	// Same name, different uid → separate series
	run2 := run.DeepCopy()
	run2.UID = types.UID("ffffffff-0000-0000-0000-000000000000")
	run2.Status.Workers[0].Guest.CmdLsEtcMs = "99"
	RecordRun(run2)
	if got := gaugeValue(t, "canario_guest_cmd_ls_etc_ms", map[string]string{
		"run": "bench-metrics-ut", "uid": "ffffffff", "profile": "small", "node": "worker-a",
	}); got != 99 {
		t.Fatalf("run2 ls_etc=%v want 99", got)
	}
	if got := gaugeValue(t, "canario_guest_cmd_ls_etc_ms", map[string]string{
		"run": "bench-metrics-ut", "uid": uid, "profile": "small", "node": "worker-a",
	}); got != 42.5 {
		t.Fatalf("run1 should remain 42.5, got %v", got)
	}

	DeleteRun(run)
	if _, ok := findGauge(t, "canario_run_info", map[string]string{
		"run": "bench-metrics-ut", "uid": uid, "profile": "small", "phase": "Succeeded",
	}); ok {
		t.Fatal("expected run1 removed after DeleteRun")
	}
	if _, ok := findGauge(t, "canario_guest_cmd_ls_etc_ms", map[string]string{
		"run": "bench-metrics-ut", "uid": "ffffffff", "profile": "small", "node": "worker-a",
	}); !ok {
		t.Fatal("run2 series should remain after deleting run1")
	}
	DeleteRun(run2)
}

func gaugeValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	v, ok := findGauge(t, name, labels)
	if !ok {
		t.Fatalf("metric %s with labels %v not found", name, labels)
	}
	return v
}

func findGauge(t *testing.T, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	mfs, err := crmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsMatchExact(m.GetLabel(), labels) && m.Gauge != nil {
				return m.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

func labelsMatchExact(have []*dto.LabelPair, want map[string]string) bool {
	got := make(map[string]string, len(have))
	for _, lp := range have {
		got[lp.GetName()] = lp.GetValue()
	}
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

var _ prometheus.Gatherer = crmetrics.Registry
