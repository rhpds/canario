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
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
	"github.com/rhpds/canario/internal/controller/suitepayload"
)

// Naming: Cio = short handle for Canario. In Go we use CioVM.
// Docs/comments may say CanarioVirtualMachine. If we ever expose this as a
// product API/CRD (unlikely — today it is a KubeVirt VirtualMachine), use the
// full name CanarioVirtualMachine, not CioVM.
//
// CioVM object name: {canariorun}-{shortWorker}-{UTC timestamp}

const (
	labelCanarioRun = "canario.rhpds.com/canariorun"
	annoCioNode     = "canario.rhpds.com/node"
	// maxCioVMName leaves room for a "-data" DataVolume name (DNS-1123 ≤63).
	maxCioVMName = 58
	// cioVMStaggerInterval spaces CioVM creates so workers:all does not start every VM at t=0.
	cioVMStaggerInterval = 10 * time.Second
)

var (
	virtualMachineGVK = schema.GroupVersionKind{
		Group:   "kubevirt.io",
		Version: "v1",
		Kind:    "VirtualMachine",
	}
	virtualMachineListGVK = schema.GroupVersionKind{
		Group:   "kubevirt.io",
		Version: "v1",
		Kind:    "VirtualMachineList",
	}
	dnsLabelCleaner = regexp.MustCompile(`[^a-z0-9-]+`)
)

// cioVMPhase is the observed CanarioVirtualMachine (CioVM) lifecycle for status sync.
type cioVMPhase string

const (
	cioVMPhaseMissing cioVMPhase = "Missing"
	cioVMPhasePending cioVMPhase = "Pending"
	cioVMPhaseReady   cioVMPhase = "Ready"
	cioVMPhaseFailed  cioVMPhase = "Failed"
)

type cioVMStatus struct {
	Phase    cioVMPhase
	Message  string
	Name     string
	NodeName string
}

// sanitizeDNS1123 lowercases and strips to [a-z0-9-].
func sanitizeDNS1123(s string) string {
	n := dnsLabelCleaner.ReplaceAllString(strings.ToLower(s), "-")
	n = strings.Trim(n, "-")
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	return n
}

// nodeLabel keeps the real worker hostname (sanitized). Truncates from the
// left only when needed so the unique suffix (e.g. 2bzjd-1) remains.
func nodeLabel(node string, maxLen int) string {
	n := sanitizeDNS1123(node)
	if n == "" {
		return "node"
	}
	if maxLen < 1 {
		maxLen = 1
	}
	if len(n) > maxLen {
		n = strings.Trim(n[len(n)-maxLen:], "-")
	}
	if n == "" {
		return "node"
	}
	return n
}

// newCioVMName builds {canariorun}-{workernode}-{yyyymmddtHHMMSS} (DNS-1123, ≤58).
// Example: nightly-ceph-worker-cluster-2bzjd-1-20261016t081944
func newCioVMName(runName, node string, now time.Time) string {
	ts := now.UTC().Format("20060102t150405") // 16 chars
	run := sanitizeDNS1123(runName)
	if run == "" {
		run = "run"
	}

	// Prefer full worker name; shrink run then node only if over maxCioVMName.
	const sep = 2 // two hyphens
	budget := maxCioVMName - len(ts) - sep
	if budget < 2 {
		budget = 2
	}
	// Give node priority for readability; run keeps at least 8 chars when possible.
	maxRun := len(run)
	maxNode := budget - maxRun
	if maxNode < 8 {
		maxNode = 8
		maxRun = budget - maxNode
		if maxRun < 1 {
			maxRun = 1
			maxNode = budget - 1
		}
	}
	if len(run) > maxRun {
		run = strings.Trim(run[:maxRun], "-")
	}
	sn := nodeLabel(node, maxNode)
	name := fmt.Sprintf("%s-%s-%s", run, sn, ts)
	if len(name) > maxCioVMName {
		name = strings.Trim(name[:maxCioVMName], "-")
	}
	return name
}

func cioVMDataVolumeName(vmName string, index int) string {
	return fmt.Sprintf("%s-d%d", vmName, index)
}

func profileResources(profile string) (cpu string, memory string, disk string) {
	switch profile {
	case canariov1alpha1.ProfileDefault:
		return "2", "4Gi", "30Gi"
	default:
		return "1", "2Gi", "15Gi"
	}
}

func indentYAML(s string, spaces int) string {
	pad := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = pad + line
	}
	return strings.Join(lines, "\n")
}

func disksJSON(disks []resolvedDisk) string {
	type row struct {
		StorageClass string `json:"storage_class"`
		DataDev      string `json:"data_dev"`
		VolumeMode   string `json:"volume_mode"`
		AccessMode   string `json:"access_mode"`
		Provisioner  string `json:"provisioner"`
	}
	rows := make([]row, 0, len(disks))
	for _, d := range disks {
		rows = append(rows, row{
			StorageClass: d.StorageClass,
			DataDev:      d.Device,
			VolumeMode:   string(d.VolumeMode),
			AccessMode:   string(d.AccessMode),
			Provisioner:  d.Provisioner,
		})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// defaultSuiteSections runs the full modular suite (network auto-skips offline).
const defaultSuiteSections = "all"

// cloudInitUserData installs tools, drops the suite, and auto-runs selected sections.
func cloudInitUserData(sections, nodeName string, disks []resolvedDisk) string {
	if sections == "" {
		sections = defaultSuiteSections
	}
	suite := strings.TrimSpace(suitepayload.SuiteSH) + "\n"
	dj := disksJSON(disks)
	return fmt.Sprintf(`#cloud-config
package_update: true
packages:
  - fio
  - xfsprogs
  - gcc
  - stress-ng
  - git
  - bind-utils
  - iputils
  - curl
write_files:
  - path: /etc/canario/disks.json
    permissions: '0644'
    content: |
%s
  - path: /etc/canario/env
    permissions: '0644'
    content: |
      CANARIO_SUITE_SECTIONS=%s
      CANARIO_NETWORK=auto
      CANARIO_NODE_NAME=%s
      CANARIO_DISKS_JSON_FILE=/etc/canario/disks.json
      CANARIO_DATA_MOUNT=/mnt/canario-data
      CANARIO_RESULTS_DIR=/var/lib/canario
  - path: /usr/local/bin/canario-suite
    permissions: '0755'
    content: |
%s
runcmd:
  - [ bash, -lc, "set -a; source /etc/canario/env; /usr/local/bin/canario-suite >>/var/log/canario-suite.log 2>&1" ]
`, indentYAML(dj, 6), sections, nodeName, indentYAML(suite, 6))
}

// listCioVMs returns CioVMs owned by this CanarioRun (by label).
func (r *CanarioRunReconciler) listCioVMs(ctx context.Context, run *canariov1alpha1.CanarioRun) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(virtualMachineListGVK)
	if err := r.List(ctx, list,
		client.InNamespace(run.Namespace),
		client.MatchingLabels{labelCanarioRun: run.Name},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// cioVMsByNode maps annoCioNode → owned CioVM.
func (r *CanarioRunReconciler) cioVMsByNode(ctx context.Context, run *canariov1alpha1.CanarioRun) (map[string]*unstructured.Unstructured, error) {
	items, err := r.listCioVMs(ctx, run)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*unstructured.Unstructured, len(items))
	for i := range items {
		vm := &items[i]
		node := vm.GetAnnotations()[annoCioNode]
		if node == "" {
			continue
		}
		out[node] = vm
	}
	return out, nil
}

func cloudInitSecretName(vmName string) string {
	// vmName ≤58; "-cloudinit" is 10 chars → ≤68, trim if needed (Secret names ≤253).
	return vmName + "-cloudinit"
}

// ensureCloudInitSecret stores userdata in a Secret (KubeVirt inline userdata max 2048 bytes).
func (r *CanarioRunReconciler) ensureCloudInitSecret(ctx context.Context, run *canariov1alpha1.CanarioRun, vmName, sections, nodeName string, disks []resolvedDisk) (string, error) {
	name := cloudInitSecretName(vmName)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: run.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "canario",
				"app.kubernetes.io/component": "ciovm",
				labelCanarioRun:               run.Name,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"userdata": []byte(cloudInitUserData(sections, nodeName, disks)),
		},
	}
	if err := ctrl.SetControllerReference(run, secret, r.Scheme); err != nil {
		return "", err
	}
	if err := r.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return "", err
		}
	}
	return name, nil
}

// createCioVM creates a CanarioVirtualMachine: boot DV (DataSource/registry) + N blank data volumes + cloud-init suite.
func (r *CanarioRunReconciler) createCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun, nodeName string, disks []resolvedDisk, boot bootSource) (*unstructured.Unstructured, error) {
	log := logf.FromContext(ctx)
	cpu, memory, diskSize := profileResources(run.Spec.Profile)
	vmName := newCioVMName(run.Name, nodeName, time.Now())

	secretName, err := r.ensureCloudInitSecret(ctx, run, vmName, defaultSuiteSections, nodeName, disks)
	if err != nil {
		return nil, fmt.Errorf("cloud-init secret: %w", err)
	}

	// Root PVC SC: prefer first data disk SC so clones land on known virt storage.
	rootSC := ""
	if len(disks) > 0 {
		rootSC = disks[0].StorageClass
	}

	diskDevices := []interface{}{
		map[string]interface{}{
			"name": "rootdisk",
			"disk": map[string]interface{}{"bus": "virtio"},
		},
	}
	volumes := []interface{}{
		map[string]interface{}{
			"name": "rootdisk",
			"dataVolume": map[string]interface{}{
				"name": cioVMRootVolumeName(vmName),
			},
		},
	}
	dvTemplates := []interface{}{
		rootDataVolumeTemplate(vmName, boot, "30Gi", rootSC),
	}

	for i, d := range disks {
		volName := fmt.Sprintf("datadisk-%d", i)
		dvName := cioVMDataVolumeName(vmName, i)
		pvc := map[string]interface{}{
			"accessModes": []interface{}{string(d.AccessMode)},
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"storage": diskSize,
				},
			},
			"storageClassName": d.StorageClass,
		}
		if d.VolumeMode != "" {
			pvc["volumeMode"] = string(d.VolumeMode)
		}
		dvTemplates = append(dvTemplates, map[string]interface{}{
			"metadata": map[string]interface{}{"name": dvName},
			"spec": map[string]interface{}{
				"source": map[string]interface{}{"blank": map[string]interface{}{}},
				"pvc":    pvc,
			},
		})
		diskDevices = append(diskDevices, map[string]interface{}{
			"name": volName,
			"disk": map[string]interface{}{"bus": "virtio"},
		})
		volumes = append(volumes, map[string]interface{}{
			"name": volName,
			"dataVolume": map[string]interface{}{
				"name": dvName,
			},
		})
	}

	diskDevices = append(diskDevices, map[string]interface{}{
		"name": "cloudinitdisk",
		"disk": map[string]interface{}{"bus": "virtio"},
	})
	volumes = append(volumes, map[string]interface{}{
		"name": "cloudinitdisk",
		// Suite script is too large for inline userdata (KubeVirt 2048-byte limit).
		"cloudInitNoCloud": map[string]interface{}{
			"secretRef": map[string]interface{}{
				"name": secretName,
			},
		},
	})

	vmiSpec := map[string]interface{}{
		"domain": map[string]interface{}{
			"devices": map[string]interface{}{
				"disks": diskDevices,
			},
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"cpu":    cpu,
					"memory": memory,
				},
			},
		},
		"volumes": volumes,
	}
	if nodeName != "" {
		vmiSpec["nodeSelector"] = map[string]interface{}{
			"kubernetes.io/hostname": nodeName,
		}
	}

	vm := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kubevirt.io/v1",
			"kind":       "VirtualMachine",
			"metadata": map[string]interface{}{
				"name":      vmName,
				"namespace": run.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/name":      "canario",
					"app.kubernetes.io/component": "ciovm",
					labelCanarioRun:               run.Name,
				},
				"annotations": map[string]interface{}{
					annoCioNode: nodeName,
				},
			},
			"spec": map[string]interface{}{
				"runStrategy":         "Always",
				"dataVolumeTemplates": dvTemplates,
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app.kubernetes.io/name":      "canario",
							"app.kubernetes.io/component": "ciovm",
							labelCanarioRun:               run.Name,
						},
					},
					"spec": vmiSpec,
				},
			},
		},
	}
	vm.SetGroupVersionKind(virtualMachineGVK)

	if err := ctrl.SetControllerReference(run, vm, r.Scheme); err != nil {
		return nil, err
	}
	if err := r.Create(ctx, vm); err != nil {
		return nil, err
	}
	scs := make([]string, len(disks))
	for i, d := range disks {
		scs[i] = d.StorageClass
	}
	log.Info("created CioVM", "vm", vm.GetName(), "node", nodeName, "storageClasses", scs,
		"bootKind", boot.Kind, "bootDataSource", boot.DataSourceNamespace+"/"+boot.DataSourceName, "bootRegistry", boot.RegistryURL)
	return vm, nil
}

// deleteCioVM deletes all CioVMs (and cloud-init Secrets) for this Run.
func (r *CanarioRunReconciler) deleteCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun) error {
	log := logf.FromContext(ctx)
	items, err := r.listCioVMs(ctx, run)
	if err != nil {
		// envtest / clusters without KubeVirt: nothing to delete
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			log.Info("skip CioVM delete: VirtualMachine API unavailable")
		} else {
			return err
		}
	} else {
		for i := range items {
			vm := &items[i]
			if err := r.Delete(ctx, vm); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			log.Info("deleted CioVM", "vm", vm.GetName())
		}
	}

	secrets := &corev1.SecretList{}
	if err := r.List(ctx, secrets,
		client.InNamespace(run.Namespace),
		client.MatchingLabels{labelCanarioRun: run.Name},
	); err != nil {
		return err
	}
	for i := range secrets.Items {
		s := &secrets.Items[i]
		if err := r.Delete(ctx, s); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		log.Info("deleted cloud-init Secret", "secret", s.Name)
	}
	return nil
}

func observeCioVM(vm *unstructured.Unstructured) cioVMStatus {
	if vm == nil {
		return cioVMStatus{Phase: cioVMPhaseMissing, Message: "CioVM missing"}
	}
	name := vm.GetName()
	node := vm.GetAnnotations()[annoCioNode]
	ready, found, err := unstructured.NestedBool(vm.Object, "status", "ready")
	if err == nil && found && ready {
		return cioVMStatus{
			Phase:    cioVMPhaseReady,
			Message:  "CioVM ready; waiting for suite",
			Name:     name,
			NodeName: node,
		}
	}
	printable, _, _ := unstructured.NestedString(vm.Object, "status", "printableStatus")
	if printable == "ErrorUnschedulable" || printable == "CrashLoopBackOff" || printable == "ErrImagePull" {
		return cioVMStatus{Phase: cioVMPhaseFailed, Message: "CioVM failed: " + printable, Name: name, NodeName: node}
	}
	if printable != "" {
		return cioVMStatus{Phase: cioVMPhasePending, Message: "CioVM " + printable, Name: name, NodeName: node}
	}
	return cioVMStatus{Phase: cioVMPhasePending, Message: "CioVM pending", Name: name, NodeName: node}
}

// ensureCioVMsStaggered creates missing CioVMs spaced by cioVMStaggerInterval.
// It does not wait for Ready or suite completion — only delays the next create.
// Returns a requeue hint when the next create is still within the stagger window.
func (r *CanarioRunReconciler) ensureCioVMsStaggered(ctx context.Context, run *canariov1alpha1.CanarioRun, targets []string, disks []resolvedDisk, boot bootSource) (time.Duration, error) {
	byNode, err := r.cioVMsByNode(ctx, run)
	if err != nil {
		return 0, err
	}

	for _, node := range targets {
		if _, ok := byNode[node]; ok {
			continue
		}
		if wait := staggerWait(byNode, time.Now()); wait > 0 {
			return wait, nil
		}
		if _, err := r.createCioVM(ctx, run, node, disks, boot); err != nil {
			return 0, err
		}
		// Space the following create; callers requeue on this hint.
		return cioVMStaggerInterval, nil
	}
	return 0, nil
}

// staggerWait is how long to wait before creating another CioVM, based on the
// newest existing VM's creationTimestamp. Zero means create is allowed now.
func staggerWait(byNode map[string]*unstructured.Unstructured, now time.Time) time.Duration {
	if len(byNode) == 0 {
		return 0
	}
	var latest time.Time
	for _, vm := range byNode {
		t := vm.GetCreationTimestamp().Time
		if t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return 0
	}
	elapsed := now.Sub(latest)
	if elapsed >= cioVMStaggerInterval {
		return 0
	}
	return cioVMStaggerInterval - elapsed
}

// isWorkerNode reports whether a node is a valid Canario placement target for
// workers=all|busiest. CioVMs have no tolerations, so any NoSchedule/NoExecute
// taint means we cannot deploy there (dedicated ODF storage/infra, cordon
// pressure taints, control-plane, etc.).
//
// Hyperconverged "storage + worker" nodes are kept when they only carry storage
// labels (e.g. cluster.ocs.openshift.io/openshift-storage) and are not tainted
// — those are real compute workers. Explicit Spec.Workers=<node> bypasses this.
func isWorkerNode(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		return false
	}
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		return false
	}
	for _, t := range node.Spec.Taints {
		if t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute {
			return false
		}
	}
	return true
}

func newVirtualMachineForWatch() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(virtualMachineGVK)
	return u
}
