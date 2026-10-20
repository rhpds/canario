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
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// MetricsFinalizer is added to CanarioRuns so series can be cleared on delete.
	MetricsFinalizer = "canario.rhpds.com/metrics"

	// shortUIDLen is the label length derived from metadata.uid (hex, no dashes).
	shortUIDLen = 8
)

var (
	registerOnce sync.Once

	// Common labels: run (CR name), uid (short CR uid), profile (spec.profile).
	// Guest/disk also use node; disk also uses storage_class.
	labelRun     = []string{"run", "uid", "profile"}
	labelRunNode = []string{"run", "uid", "profile", "node"}
	labelRunDisk = []string{"run", "uid", "profile", "node", "storage_class"}
	labelCheck   = []string{"run", "uid", "profile", "node", "check"}

	runInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_run_info",
		Help: "CanarioRun presence (1) with phase and profile labels.",
	}, append(labelRun, "phase"))

	runCompletion = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_run_completion_timestamp_seconds",
		Help: "Unix timestamp when the CanarioRun finished.",
	}, labelRun)

	guestCmdLsEtcMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_cmd_ls_etc_ms",
		Help: "Guest ls -laR /etc duration in milliseconds.",
	}, labelRunNode)

	guestCmdFindConfMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_cmd_find_conf_ms",
		Help: "Guest find *.conf duration in milliseconds.",
	}, labelRunNode)

	guestCmdPsMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_cmd_ps_ms",
		Help: "Guest ps auxww duration in milliseconds.",
	}, labelRunNode)

	guestDnfInstallMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_dnf_install_ms",
		Help: "Guest dnf install duration in milliseconds.",
	}, labelRunNode)

	guestCompileGccMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_compile_gcc_ms",
		Help: "Guest gcc compile duration in milliseconds.",
	}, labelRunNode)

	guestCompileRunMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_compile_run_ms",
		Help: "Guest compiled binary run duration in milliseconds.",
	}, labelRunNode)

	guestCPUMatrixBogoOps = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_cpu_matrix_bogo_ops",
		Help: "Guest stress-ng matrix bogo ops.",
	}, labelRunNode)

	guestMemMemcpyBogoOps = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_mem_memcpy_bogo_ops",
		Help: "Guest stress-ng memcpy bogo ops.",
	}, labelRunNode)

	guestNetPingAvgMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_net_ping_avg_ms",
		Help: "Guest ping average RTT in milliseconds.",
	}, labelRunNode)

	guestNetDNSMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_net_dns_ms",
		Help: "Guest DNS resolve duration in milliseconds.",
	}, labelRunNode)

	guestNetDownloadMbps = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_net_download_mbps",
		Help: "Guest download throughput in megabits per second.",
	}, labelRunNode)

	guestNetTTFBMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_net_ttfb_ms",
		Help: "Guest download time-to-first-byte in milliseconds.",
	}, labelRunNode)

	guestGitCloneMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_git_clone_ms",
		Help: "Guest git clone duration in milliseconds.",
	}, labelRunNode)

	guestCheckOK = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_guest_check_ok",
		Help: "Guest soft check result (1=ok, 0=failed). Omitted when skipped.",
	}, labelCheck)

	diskFioSeqWriteBps = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_disk_fio_seq_write_bytes_per_second",
		Help: "fio sequential write bandwidth in bytes per second.",
	}, labelRunDisk)

	diskFioSeqReadBps = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_disk_fio_seq_read_bytes_per_second",
		Help: "fio sequential read bandwidth in bytes per second.",
	}, labelRunDisk)

	diskFioRandReadIOPS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_disk_fio_rand_read_iops",
		Help: "fio random read IOPS.",
	}, labelRunDisk)

	diskFioRandWriteIOPS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_disk_fio_rand_write_iops",
		Help: "fio random write IOPS.",
	}, labelRunDisk)

	diskFileopsCreateMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_disk_fileops_create_1000_ms",
		Help: "Create 1000 files duration in milliseconds.",
	}, labelRunDisk)

	diskFileopsTarMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_disk_fileops_tar_create_ms",
		Help: "tar create duration in milliseconds.",
	}, labelRunDisk)

	diskFileopsExtractMs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "canario_disk_fileops_tar_extract_ms",
		Help: "tar extract duration in milliseconds.",
	}, labelRunDisk)

	allCollectors = []prometheus.Collector{
		runInfo,
		runCompletion,
		guestCmdLsEtcMs,
		guestCmdFindConfMs,
		guestCmdPsMs,
		guestDnfInstallMs,
		guestCompileGccMs,
		guestCompileRunMs,
		guestCPUMatrixBogoOps,
		guestMemMemcpyBogoOps,
		guestNetPingAvgMs,
		guestNetDNSMs,
		guestNetDownloadMbps,
		guestNetTTFBMs,
		guestGitCloneMs,
		guestCheckOK,
		diskFioSeqWriteBps,
		diskFioSeqReadBps,
		diskFioRandReadIOPS,
		diskFioRandWriteIOPS,
		diskFileopsCreateMs,
		diskFileopsTarMs,
		diskFileopsExtractMs,
	}
)

// RegisterWithRegistry registers canario gauges. Safe to call multiple times.
func RegisterWithRegistry(reg prometheus.Registerer) {
	registerOnce.Do(func() {
		reg.MustRegister(allCollectors...)
	})
}

func init() {
	RegisterWithRegistry(crmetrics.Registry)
}

// ShortUID returns a short hex id from a Kubernetes object UID (8 chars).
func ShortUID(uid string) string {
	s := strings.ToLower(strings.ReplaceAll(uid, "-", ""))
	if len(s) >= shortUIDLen {
		return s[:shortUIDLen]
	}
	if s == "" {
		return "00000000"
	}
	for len(s) < shortUIDLen {
		s += "0"
	}
	return s
}

// RecordRun publishes gauges for a terminal CanarioRun from its status.
// Empty or unparseable fields are skipped.
func RecordRun(run *canariov1alpha1.CanarioRun) {
	if run == nil || run.Name == "" {
		return
	}
	name := run.Name
	uid := ShortUID(string(run.UID))
	profile := run.Spec.Profile
	if profile == "" {
		profile = canariov1alpha1.ProfileSmall
	}
	phase := run.Status.Phase
	if phase == "" {
		phase = "Unknown"
	}

	DeleteRun(run)

	runInfo.WithLabelValues(name, uid, profile, phase).Set(1)
	if run.Status.CompletionTime != nil {
		runCompletion.WithLabelValues(name, uid, profile).Set(float64(run.Status.CompletionTime.Unix()))
	}

	for _, w := range run.Status.Workers {
		node := w.NodeName
		if node == "" {
			continue
		}
		if g := w.Guest; g != nil {
			setFloat(guestCmdLsEtcMs, name, uid, profile, node, g.CmdLsEtcMs)
			setFloat(guestCmdFindConfMs, name, uid, profile, node, g.CmdFindConfMs)
			setFloat(guestCmdPsMs, name, uid, profile, node, g.CmdPsMs)
			setFloat(guestDnfInstallMs, name, uid, profile, node, g.DnfInstallMs)
			setFloat(guestCompileGccMs, name, uid, profile, node, g.CompileGccMs)
			setFloat(guestCompileRunMs, name, uid, profile, node, g.CompileRunMs)
			setFloat(guestCPUMatrixBogoOps, name, uid, profile, node, g.CPUMatrixBogoOps)
			setFloat(guestMemMemcpyBogoOps, name, uid, profile, node, g.MemMemcpyBogoOps)
			setFloat(guestNetPingAvgMs, name, uid, profile, node, g.NetPingAvgMs)
			setFloat(guestNetDNSMs, name, uid, profile, node, g.NetDNSMs)
			setFloat(guestNetDownloadMbps, name, uid, profile, node, g.NetDownloadMbps)
			setFloat(guestNetTTFBMs, name, uid, profile, node, g.NetTTFBMs)
			setFloat(guestGitCloneMs, name, uid, profile, node, g.GitCloneMs)
			setCheckOK(name, uid, profile, node, "ping", g.NetPingStatus)
			setCheckOK(name, uid, profile, node, "dns", g.NetDNSStatus)
			setCheckOK(name, uid, profile, node, "download", g.NetDownloadStatus)
			setCheckOK(name, uid, profile, node, "git", g.GitCloneStatus)
		}
		for _, d := range w.Disks {
			sc := d.StorageClass
			if sc == "" {
				sc = "unknown"
			}
			diskNode := d.NodeName
			if diskNode == "" {
				diskNode = node
			}
			setBW(diskFioSeqWriteBps, name, uid, profile, diskNode, sc, d.FioSeqWriteBW)
			setBW(diskFioSeqReadBps, name, uid, profile, diskNode, sc, d.FioSeqReadBW)
			setIOPS(diskFioRandReadIOPS, name, uid, profile, diskNode, sc, d.FioRandReadIOPS)
			setIOPS(diskFioRandWriteIOPS, name, uid, profile, diskNode, sc, d.FioRandWriteIOPS)
			setFloatSC(diskFileopsCreateMs, name, uid, profile, diskNode, sc, d.FileopsCreateMs)
			setFloatSC(diskFileopsTarMs, name, uid, profile, diskNode, sc, d.FileopsTarMs)
			setFloatSC(diskFileopsExtractMs, name, uid, profile, diskNode, sc, d.FileopsExtractMs)
		}
	}
}

// DeleteRun removes series for this Run (matched by short uid).
func DeleteRun(run *canariov1alpha1.CanarioRun) {
	if run == nil {
		return
	}
	uid := ShortUID(string(run.UID))
	match := prometheus.Labels{"uid": uid}
	runInfo.DeletePartialMatch(match)
	runCompletion.DeletePartialMatch(match)
	guestCmdLsEtcMs.DeletePartialMatch(match)
	guestCmdFindConfMs.DeletePartialMatch(match)
	guestCmdPsMs.DeletePartialMatch(match)
	guestDnfInstallMs.DeletePartialMatch(match)
	guestCompileGccMs.DeletePartialMatch(match)
	guestCompileRunMs.DeletePartialMatch(match)
	guestCPUMatrixBogoOps.DeletePartialMatch(match)
	guestMemMemcpyBogoOps.DeletePartialMatch(match)
	guestNetPingAvgMs.DeletePartialMatch(match)
	guestNetDNSMs.DeletePartialMatch(match)
	guestNetDownloadMbps.DeletePartialMatch(match)
	guestNetTTFBMs.DeletePartialMatch(match)
	guestGitCloneMs.DeletePartialMatch(match)
	guestCheckOK.DeletePartialMatch(match)
	diskFioSeqWriteBps.DeletePartialMatch(match)
	diskFioSeqReadBps.DeletePartialMatch(match)
	diskFioRandReadIOPS.DeletePartialMatch(match)
	diskFioRandWriteIOPS.DeletePartialMatch(match)
	diskFileopsCreateMs.DeletePartialMatch(match)
	diskFileopsTarMs.DeletePartialMatch(match)
	diskFileopsExtractMs.DeletePartialMatch(match)
}

func setFloat(g *prometheus.GaugeVec, run, uid, profile, node, raw string) {
	if v, ok := ParseFloat(raw); ok {
		g.WithLabelValues(run, uid, profile, node).Set(v)
	}
}

func setFloatSC(g *prometheus.GaugeVec, run, uid, profile, node, sc, raw string) {
	if v, ok := ParseFloat(raw); ok {
		g.WithLabelValues(run, uid, profile, node, sc).Set(v)
	}
}

func setBW(g *prometheus.GaugeVec, run, uid, profile, node, sc, raw string) {
	if v, ok := ParseBandwidthBytesPerSec(raw); ok {
		g.WithLabelValues(run, uid, profile, node, sc).Set(v)
	}
}

func setIOPS(g *prometheus.GaugeVec, run, uid, profile, node, sc, raw string) {
	if v, ok := ParseIOPS(raw); ok {
		g.WithLabelValues(run, uid, profile, node, sc).Set(v)
	}
}

func setCheckOK(run, uid, profile, node, check, status string) {
	switch status {
	case "ok":
		guestCheckOK.WithLabelValues(run, uid, profile, node, check).Set(1)
	case "failed":
		guestCheckOK.WithLabelValues(run, uid, profile, node, check).Set(0)
	}
}
