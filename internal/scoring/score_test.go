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

package scoring

import (
	"testing"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func i64(v int64) *int64 { return &v }
func i32(v int32) *int32 { return &v }

func TestApplyWorstWorker(t *testing.T) {
	policy := &canariov1alpha1.CanarioScore{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: canariov1alpha1.CanarioScoreSpec{
			Modules: canariov1alpha1.ScoreModules{
				Packages: &canariov1alpha1.PackagesBands{
					DnfInstallMs: []canariov1alpha1.UpperBoundBand{
						{Max: i64(3500), Points: 0},
						{Max: i64(5000), Points: 1},
						{Max: i64(6500), Points: 2},
						{Points: 3},
					},
				},
			},
			Verdicts: []canariov1alpha1.VerdictBand{
				{Max: i32(1), Name: "excellent"},
				{Max: i32(3), Name: "good"},
				{Name: "poor"},
			},
		},
	}

	run := &canariov1alpha1.CanarioRun{
		Status: canariov1alpha1.CanarioRunStatus{
			Workers: []canariov1alpha1.WorkerResult{
				{NodeName: "w1", Guest: &canariov1alpha1.GuestResult{DnfInstallMs: "3000"}},
				{NodeName: "w2", Guest: &canariov1alpha1.GuestResult{DnfInstallMs: "7000"}},
			},
		},
	}
	rs := Apply(policy, run)
	if rs == nil || rs.Total != 3 {
		t.Fatalf("run total=%v want 3 (worst worker)", rs)
	}
	if run.Status.Workers[0].Scores.Total != 0 || run.Status.Workers[1].Scores.Total != 3 {
		t.Fatalf("worker scores=%v %v", run.Status.Workers[0].Scores, run.Status.Workers[1].Scores)
	}
	if rs.Verdict != "good" && rs.Total == 3 {
		// total 3 → good (max 3)
		if rs.Verdict != "good" {
			t.Fatalf("verdict=%q want good", rs.Verdict)
		}
	}
}

func TestStorageTierLocalVsRemote(t *testing.T) {
	policy := &canariov1alpha1.CanarioScore{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: canariov1alpha1.CanarioScoreSpec{
			Modules: canariov1alpha1.ScoreModules{
				Storage: &canariov1alpha1.StorageBands{
					DefaultTier: canariov1alpha1.StorageTierRemote,
					StorageClassTiers: map[string]string{
						"local-sc": "local",
					},
					Tiers: map[string]canariov1alpha1.StorageTierBands{
						"local": {
							FioSeqWriteMBps: []canariov1alpha1.LowerBoundBand{
								{Min: i64(500), Points: 0},
								{Min: i64(200), Points: 1},
								{Points: 3},
							},
						},
						"remote": {
							FioSeqWriteMBps: []canariov1alpha1.LowerBoundBand{
								{Min: i64(200), Points: 0},
								{Min: i64(50), Points: 1},
								{Points: 3},
							},
						},
					},
				},
			},
		},
	}
	run := &canariov1alpha1.CanarioRun{
		Status: canariov1alpha1.CanarioRunStatus{
			Workers: []canariov1alpha1.WorkerResult{{
				NodeName: "w1",
				Disks: []canariov1alpha1.DiskResult{
					{StorageClass: "local-sc", FioSeqWriteBW: "BW=300MiB/s"},
					{StorageClass: "ocs-rbd", FioSeqWriteBW: "BW=300MiB/s"},
				},
			}},
		},
	}
	_ = Apply(policy, run)
	ws := run.Status.Workers[0].Scores
	if ws == nil || len(ws.Disks) != 2 {
		t.Fatalf("disks=%v", ws)
	}
	// local 300 MiB/s with local bands (>=200 → 1); remote 300 with remote bands (>=200 → 0)
	var localPts, remotePts int32
	for _, d := range ws.Disks {
		switch d.Tier {
		case "local":
			localPts = d.Storage
		case "remote":
			remotePts = d.Storage
		}
	}
	if localPts != 1 || remotePts != 0 {
		t.Fatalf("local=%d remote=%d want 1 and 0", localPts, remotePts)
	}
	if ws.Storage != 1 { // worst disk
		t.Fatalf("worker storage=%d want 1", ws.Storage)
	}
}
