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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

// suiteSnapshot is the in-guest suite publish dir (/tmp/canario).
type suiteSnapshot struct {
	Status  string // running|succeeded|failed|""
	Done    string // RFC3339 or empty if not finished
	Guest   *canariov1alpha1.GuestResult
	Disks   []canariov1alpha1.DiskResult
	RawJSON string
}

// guestResultsFile is JSON written by suite.sh (snake_case keys).
type guestResultsFile struct {
	Sections string              `json:"sections"`
	NodeName string              `json:"node_name"`
	Guest    *guestGuestResult   `json:"guest"`
	Disks    []guestDiskResult   `json:"disks"`
}

type guestGuestResult struct {
	CmdLsEtcMs        string `json:"cmd_ls_etc_ms"`
	CmdFindConfMs     string `json:"cmd_find_conf_ms"`
	CmdPsMs           string `json:"cmd_ps_ms"`
	DnfInstallMs      string `json:"dnf_install_ms"`
	DnfInstallStatus  string `json:"dnf_install_status"`
	CompileGccMs      string `json:"compile_gcc_ms"`
	CompileRunMs      string `json:"compile_run_ms"`
	CPUMatrixBogoOps  string `json:"cpu_matrix_bogo_ops"`
	MemMemcpyBogoOps  string `json:"mem_memcpy_bogo_ops"`
	NetworkStatus     string `json:"network_status"`
	NetPingAvgMs      string `json:"net_ping_avg_ms"`
	NetPingStatus     string `json:"net_ping_status"`
	NetDNSMs          string `json:"net_dns_ms"`
	NetDNSStatus      string `json:"net_dns_status"`
	NetDownloadMbps   string `json:"net_download_mbps"`
	NetTTFBMs         string `json:"net_ttfb_ms"`
	NetDownloadStatus string `json:"net_download_status"`
	GitCloneMs        string `json:"git_clone_ms"`
	GitCloneStatus    string `json:"git_clone_status"`
}

type guestDiskResult struct {
	StorageClass        string `json:"storage_class"`
	DataDev             string `json:"data_dev"`
	VolumeMode          string `json:"volume_mode"`
	AccessMode          string `json:"access_mode"`
	Provisioner         string `json:"provisioner"`
	SuiteStatus         string `json:"suite_status"`
	Sections            string `json:"sections"`
	NodeName            string `json:"node_name"`
	FioSeqWriteBW       string `json:"fio_seqwrite_bw"`
	FioSeqReadBW        string `json:"fio_seqread_bw"`
	FioRandReadIOPS     string `json:"fio_randread_iops"`
	FioRandWriteIOPS    string `json:"fio_randwrite_iops"`
	FileopsCreate1000Ms string `json:"fileops_create_1000_ms"`
	FileopsTarCreateMs  string `json:"fileops_tar_create_ms"`
	FileopsTarExtractMs string `json:"fileops_tar_extract_ms"`
	Message             string `json:"message"`
}

func mapGuestResult(g *guestGuestResult) *canariov1alpha1.GuestResult {
	if g == nil {
		return nil
	}
	return &canariov1alpha1.GuestResult{
		CmdLsEtcMs:        g.CmdLsEtcMs,
		CmdFindConfMs:     g.CmdFindConfMs,
		CmdPsMs:           g.CmdPsMs,
		DnfInstallMs:      g.DnfInstallMs,
		DnfInstallStatus:  g.DnfInstallStatus,
		CompileGccMs:      g.CompileGccMs,
		CompileRunMs:      g.CompileRunMs,
		CPUMatrixBogoOps:  g.CPUMatrixBogoOps,
		MemMemcpyBogoOps:  g.MemMemcpyBogoOps,
		NetworkStatus:     g.NetworkStatus,
		NetPingAvgMs:      g.NetPingAvgMs,
		NetPingStatus:     g.NetPingStatus,
		NetDNSMs:          g.NetDNSMs,
		NetDNSStatus:      g.NetDNSStatus,
		NetDownloadMbps:   g.NetDownloadMbps,
		NetTTFBMs:         g.NetTTFBMs,
		NetDownloadStatus: g.NetDownloadStatus,
		GitCloneMs:        g.GitCloneMs,
		GitCloneStatus:    g.GitCloneStatus,
	}
}

func (r *CanarioRunReconciler) readSuite(ctx context.Context, namespace, vmName string) (*suiteSnapshot, error) {
	if r.ReadSuite != nil {
		return r.ReadSuite(ctx, namespace, vmName)
	}
	return r.readSuiteViaGuestAgent(ctx, namespace, vmName)
}

func (r *CanarioRunReconciler) readSuiteViaGuestAgent(ctx context.Context, namespace, vmName string) (*suiteSnapshot, error) {
	out, err := r.guestExec(ctx, namespace, vmName,
		`cat /tmp/canario/status 2>/dev/null; echo '---'; cat /tmp/canario/done 2>/dev/null; echo '---'; cat /tmp/canario/results.json 2>/dev/null`)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(out, "---")
	snap := &suiteSnapshot{}
	if len(parts) > 0 {
		snap.Status = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		snap.Done = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		raw := strings.TrimSpace(parts[2])
		snap.RawJSON = raw
		if raw != "" {
			var gr guestResultsFile
			if err := json.Unmarshal([]byte(raw), &gr); err != nil {
				return snap, fmt.Errorf("parse results.json: %w", err)
			}
			snap.Guest = mapGuestResult(gr.Guest)
			for _, d := range gr.Disks {
				sections := d.Sections
				if sections == "" {
					sections = gr.Sections
				}
				node := d.NodeName
				if node == "" {
					node = gr.NodeName
				}
				snap.Disks = append(snap.Disks, canariov1alpha1.DiskResult{
					StorageClass:     d.StorageClass,
					DataDev:          d.DataDev,
					VolumeMode:       d.VolumeMode,
					AccessMode:       d.AccessMode,
					Provisioner:      d.Provisioner,
					SuiteStatus:      d.SuiteStatus,
					Sections:         sections,
					NodeName:         node,
					FioSeqWriteBW:    d.FioSeqWriteBW,
					FioSeqReadBW:     d.FioSeqReadBW,
					FioRandReadIOPS:  d.FioRandReadIOPS,
					FioRandWriteIOPS: d.FioRandWriteIOPS,
					FileopsCreateMs:  d.FileopsCreate1000Ms,
					FileopsTarMs:     d.FileopsTarCreateMs,
					FileopsExtractMs: d.FileopsTarExtractMs,
					Message:          d.Message,
				})
			}
		}
	}
	return snap, nil
}

func (r *CanarioRunReconciler) guestExec(ctx context.Context, namespace, vmName, script string) (string, error) {
	if r.Config == nil {
		return "", fmt.Errorf("rest.Config not set on reconciler")
	}

	podName, err := r.findVirtLauncher(ctx, namespace, vmName)
	if err != nil {
		return "", err
	}
	domain := namespace + "_" + vmName

	startPayload, err := json.Marshal(map[string]interface{}{
		"execute": "guest-exec",
		"arguments": map[string]interface{}{
			"path":           "/bin/sh",
			"arg":            []string{"-c", script},
			"capture-output": true,
		},
	})
	if err != nil {
		return "", err
	}
	startOut, err := r.execInVirtLauncher(ctx, namespace, podName, []string{
		"virsh", "qemu-agent-command", domain, string(startPayload),
	})
	if err != nil {
		return "", fmt.Errorf("guest-exec start: %w (%s)", err, startOut)
	}
	var startResp struct {
		Return struct {
			PID int `json:"pid"`
		} `json:"return"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(startOut)), &startResp); err != nil {
		return "", fmt.Errorf("parse guest-exec pid: %w (out=%q)", err, startOut)
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
		}
		statusPayload, err := json.Marshal(map[string]interface{}{
			"execute": "guest-exec-status",
			"arguments": map[string]interface{}{
				"pid": startResp.Return.PID,
			},
		})
		if err != nil {
			return "", err
		}
		statusOut, err := r.execInVirtLauncher(ctx, namespace, podName, []string{
			"virsh", "qemu-agent-command", domain, string(statusPayload),
		})
		if err != nil {
			continue
		}
		var statusResp struct {
			Return struct {
				Exited   bool   `json:"exited"`
				ExitCode int    `json:"exitcode"`
				OutData  string `json:"out-data"`
				ErrData  string `json:"err-data"`
			} `json:"return"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(statusOut)), &statusResp); err != nil {
			continue
		}
		if !statusResp.Return.Exited {
			continue
		}
		var stdout, stderr string
		if statusResp.Return.OutData != "" {
			b, err := base64.StdEncoding.DecodeString(statusResp.Return.OutData)
			if err != nil {
				return "", err
			}
			stdout = string(b)
		}
		if statusResp.Return.ErrData != "" {
			b, _ := base64.StdEncoding.DecodeString(statusResp.Return.ErrData)
			stderr = string(b)
		}
		if statusResp.Return.ExitCode != 0 && stdout == "" {
			return "", fmt.Errorf("guest-exec exit %d: %s", statusResp.Return.ExitCode, stderr)
		}
		return stdout, nil
	}
	return "", fmt.Errorf("guest-exec timed out")
}

func (r *CanarioRunReconciler) findVirtLauncher(ctx context.Context, namespace, vmName string) (string, error) {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods,
		client.InNamespace(namespace),
		client.MatchingLabels{"vm.kubevirt.io/name": vmName},
	); err != nil {
		return "", err
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		if p.Status.Phase == corev1.PodRunning {
			return p.Name, nil
		}
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("virt-launcher for vm %s not found", vmName)
	}
	return "", fmt.Errorf("virt-launcher for vm %s not running", vmName)
}

func (r *CanarioRunReconciler) execInVirtLauncher(ctx context.Context, namespace, podName string, command []string) (string, error) {
	cfg := rest.CopyConfig(r.Config)
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", err
	}
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "compute",
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, stderr.String())
	}
	return stdout.String(), nil
}
