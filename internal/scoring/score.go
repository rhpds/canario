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
	"sort"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
	"github.com/rhpds/canario/internal/metrics"
)

// Apply scores a terminal CanarioRun using policy. Mutates workers' Scores and
// returns the run-level rollup (worst worker). Modules with no data contribute 0.
func Apply(policy *canariov1alpha1.CanarioScore, run *canariov1alpha1.CanarioRun) *canariov1alpha1.RunScores {
	if policy == nil || run == nil {
		return nil
	}
	var worst int32 = -1
	for i := range run.Status.Workers {
		ws := scoreWorker(&policy.Spec, &run.Status.Workers[i])
		run.Status.Workers[i].Scores = ws
		if ws != nil && (worst < 0 || ws.Total > worst) {
			worst = ws.Total
		}
	}
	if worst < 0 {
		worst = 0
	}
	return &canariov1alpha1.RunScores{
		ScoringRef: policy.Name,
		Total:      worst,
		Verdict:    verdictFor(&policy.Spec, worst),
	}
}

func scoreWorker(spec *canariov1alpha1.CanarioScoreSpec, w *canariov1alpha1.WorkerResult) *canariov1alpha1.WorkerScores {
	if w == nil {
		return nil
	}
	out := &canariov1alpha1.WorkerScores{}
	mods := spec.Modules

	if mods.Responsive != nil && w.Guest != nil {
		out.Responsive = scoreUpperMs(mods.Responsive.CmdLsEtcMs, w.Guest.CmdLsEtcMs)
	}
	if mods.Packages != nil && w.Guest != nil {
		out.Packages = scoreUpperMs(mods.Packages.DnfInstallMs, w.Guest.DnfInstallMs)
	}

	var fileopsWorst int32 = -1
	var storageWorst int32 = -1
	for _, d := range w.Disks {
		if mods.Fileops != nil {
			p := scoreUpperMs(mods.Fileops.Create1000Ms, d.FileopsCreateMs)
			if fileopsWorst < 0 || p > fileopsWorst {
				fileopsWorst = p
			}
		}
		if mods.Storage != nil {
			tier := resolveTier(mods.Storage, d.StorageClass)
			p := scoreDiskStorage(mods.Storage, tier, &d)
			ds := canariov1alpha1.DiskScore{
				StorageClass: d.StorageClass,
				Tier:         tier,
				Storage:      p,
			}
			out.Disks = append(out.Disks, ds)
			if storageWorst < 0 || p > storageWorst {
				storageWorst = p
			}
		}
	}
	if fileopsWorst >= 0 {
		out.Fileops = fileopsWorst
	}
	if storageWorst >= 0 {
		out.Storage = storageWorst
	}

	if mods.Network != nil && w.Guest != nil {
		out.Network = scoreNetwork(mods.Network, w.Guest)
	}

	out.Total = out.Responsive + out.Packages + out.Fileops + out.Storage + out.Network
	return out
}

func resolveTier(st *canariov1alpha1.StorageBands, sc string) string {
	if st == nil {
		return canariov1alpha1.StorageTierRemote
	}
	if sc != "" && st.StorageClassTiers != nil {
		if t, ok := st.StorageClassTiers[sc]; ok && t != "" {
			return t
		}
	}
	if st.DefaultTier != "" {
		return st.DefaultTier
	}
	return canariov1alpha1.StorageTierRemote
}

func scoreDiskStorage(st *canariov1alpha1.StorageBands, tier string, d *canariov1alpha1.DiskResult) int32 {
	if st == nil || d == nil {
		return 0
	}
	bands, ok := st.Tiers[tier]
	if !ok {
		return 0
	}
	var pts int32
	if bw, ok := metrics.ParseBandwidthBytesPerSec(d.FioSeqWriteBW); ok {
		pts += scoreLower(bands.FioSeqWriteMBps, bw/1024/1024)
	}
	if bw, ok := metrics.ParseBandwidthBytesPerSec(d.FioSeqReadBW); ok {
		pts += scoreLower(bands.FioSeqReadMBps, bw/1024/1024)
	}
	if iops, ok := metrics.ParseIOPS(d.FioRandReadIOPS); ok {
		pts += scoreLower(bands.FioRandReadIOPS, iops)
	}
	if iops, ok := metrics.ParseIOPS(d.FioRandWriteIOPS); ok {
		pts += scoreLower(bands.FioRandWriteIOPS, iops)
	}
	return pts
}

func scoreNetwork(nb *canariov1alpha1.NetworkBands, g *canariov1alpha1.GuestResult) int32 {
	if nb == nil || g == nil {
		return 0
	}
	failPts := nb.FailedCheckPoints
	if failPts == 0 {
		failPts = 1
	}
	dlFail := nb.DownloadFailedPoints
	if dlFail == 0 {
		dlFail = 2
	}
	var pts int32
	for _, st := range []string{g.NetPingStatus, g.NetDNSStatus, g.GitCloneStatus} {
		if st == "failed" {
			pts += failPts
		}
	}
	switch g.NetDownloadStatus {
	case "failed":
		pts += dlFail
	case "ok":
		if mbps, ok := metrics.ParseFloat(g.NetDownloadMbps); ok {
			pts += scoreLower(nb.DownloadMbps, mbps)
		}
	}
	return pts
}

func scoreUpperMs(bands []canariov1alpha1.UpperBoundBand, raw string) int32 {
	v, ok := metrics.ParseFloat(raw)
	if !ok {
		return 0
	}
	return scoreUpper(bands, int64(v))
}

func scoreUpper(bands []canariov1alpha1.UpperBoundBand, value int64) int32 {
	if len(bands) == 0 {
		return 0
	}
	sorted := append([]canariov1alpha1.UpperBoundBand(nil), bands...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ai, aj := sorted[i].Max, sorted[j].Max
		if ai == nil {
			return false
		}
		if aj == nil {
			return true
		}
		return *ai < *aj
	})
	for _, b := range sorted {
		if b.Max == nil || value < *b.Max {
			return b.Points
		}
	}
	return sorted[len(sorted)-1].Points
}

func scoreLower(bands []canariov1alpha1.LowerBoundBand, value float64) int32 {
	if len(bands) == 0 {
		return 0
	}
	sorted := append([]canariov1alpha1.LowerBoundBand(nil), bands...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ai, aj := sorted[i].Min, sorted[j].Min
		if ai == nil {
			return false
		}
		if aj == nil {
			return true
		}
		return *ai > *aj // descending Min
	})
	for _, b := range sorted {
		if b.Min == nil || value >= float64(*b.Min) {
			return b.Points
		}
	}
	return sorted[len(sorted)-1].Points
}

func verdictFor(spec *canariov1alpha1.CanarioScoreSpec, total int32) string {
	if spec == nil || len(spec.Verdicts) == 0 {
		return defaultVerdict(total)
	}
	sorted := append([]canariov1alpha1.VerdictBand(nil), spec.Verdicts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ai, aj := sorted[i].Max, sorted[j].Max
		if ai == nil {
			return false
		}
		if aj == nil {
			return true
		}
		return *ai < *aj
	})
	for _, b := range sorted {
		if b.Max == nil || total <= *b.Max {
			return b.Name
		}
	}
	return sorted[len(sorted)-1].Name
}

func defaultVerdict(total int32) string {
	switch {
	case total <= 1:
		return "excellent"
	case total <= 3:
		return "good"
	case total <= 5:
		return "fair"
	case total <= 7:
		return "below-average"
	default:
		return "poor"
	}
}
