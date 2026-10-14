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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

// Naming: Cio = short handle for Canario. In Go we use CioVM.
// Docs/comments may say CanarioVirtualMachine. If we ever expose this as a
// product API/CRD (unlikely — today it is a KubeVirt VirtualMachine), use the
// full name CanarioVirtualMachine, not CioVM.

var virtualMachineGVK = schema.GroupVersionKind{
	Group:   "kubevirt.io",
	Version: "v1",
	Kind:    "VirtualMachine",
}

// cioVMPhase is the observed CanarioVirtualMachine (CioVM) lifecycle for status sync.
type cioVMPhase string

const (
	cioVMPhaseMissing cioVMPhase = "Missing"
	cioVMPhasePending cioVMPhase = "Pending"
	cioVMPhaseReady   cioVMPhase = "Ready"
	cioVMPhaseFailed  cioVMPhase = "Failed"
)

type cioVMStatus struct {
	Phase   cioVMPhase
	Message string
}

func cioVMName(run *canariov1alpha1.CanarioRun) string {
	return run.Name
}

func cioVMDataVolumeName(run *canariov1alpha1.CanarioRun) string {
	return run.Name + "-data"
}

func profileResources(profile string) (cpu string, memory string, disk string) {
	switch profile {
	case canariov1alpha1.ProfileDefault:
		return "2", "4Gi", "30Gi"
	default:
		// small and unknown → small
		return "1", "2Gi", "15Gi"
	}
}

// getCioVM fetches the owned KubeVirt VirtualMachine (CanarioVirtualMachine), if present.
func (r *CanarioRunReconciler) getCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun) (*unstructured.Unstructured, error) {
	vm := &unstructured.Unstructured{}
	vm.SetGroupVersionKind(virtualMachineGVK)
	err := r.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: cioVMName(run)}, vm)
	if err != nil {
		return nil, err
	}
	return vm, nil
}

// createCioVM creates a CanarioVirtualMachine: boot containerDisk + blank data volume on the requested StorageClass.
func (r *CanarioRunReconciler) createCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun, nodeName string) (*unstructured.Unstructured, error) {
	log := logf.FromContext(ctx)
	cpu, memory, disk := profileResources(run.Spec.Profile)
	dvName := cioVMDataVolumeName(run)

	dvSpec := map[string]interface{}{
		"source": map[string]interface{}{
			"blank": map[string]interface{}{},
		},
		"pvc": map[string]interface{}{
			"accessModes": []interface{}{"ReadWriteOnce"},
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"storage": disk,
				},
			},
		},
	}
	if run.Spec.StorageClass != "" {
		pvc := dvSpec["pvc"].(map[string]interface{})
		pvc["storageClassName"] = run.Spec.StorageClass
	}

	vmiSpec := map[string]interface{}{
		"domain": map[string]interface{}{
			"devices": map[string]interface{}{
				"disks": []interface{}{
					map[string]interface{}{
						"name": "rootdisk",
						"disk": map[string]interface{}{"bus": "virtio"},
					},
					map[string]interface{}{
						"name": "datadisk",
						"disk": map[string]interface{}{"bus": "virtio"},
					},
				},
			},
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"cpu":    cpu,
					"memory": memory,
				},
			},
		},
		"volumes": []interface{}{
			map[string]interface{}{
				"name": "rootdisk",
				"containerDisk": map[string]interface{}{
					// Placeholder boot image; suite wiring comes later.
					"image": "quay.io/containerdisks/fedora:40",
				},
			},
			map[string]interface{}{
				"name": "datadisk",
				"dataVolume": map[string]interface{}{
					"name": dvName,
				},
			},
		},
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
				"name":      cioVMName(run),
				"namespace": run.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/name":       "canario",
					"app.kubernetes.io/component":  "ciovm",
					"canario.rhpds.com/canariorun": run.Name,
				},
			},
			"spec": map[string]interface{}{
				"running": true,
				"dataVolumeTemplates": []interface{}{
					map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": dvName,
						},
						"spec": dvSpec,
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app.kubernetes.io/name":       "canario",
							"app.kubernetes.io/component":  "ciovm",
							"canario.rhpds.com/canariorun": run.Name,
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
	log.Info("created CioVM", "vm", vm.GetName(), "node", nodeName, "storageClass", run.Spec.StorageClass)
	return vm, nil
}

// deleteCioVM deletes the CanarioVirtualMachine if it exists (DV/PVC follow owner refs when set by KubeVirt/CDI).
func (r *CanarioRunReconciler) deleteCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun) error {
	log := logf.FromContext(ctx)
	vm, err := r.getCioVM(ctx, run)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := r.Delete(ctx, vm); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	log.Info("deleted CioVM", "vm", vm.GetName())
	return nil
}

func observeCioVM(vm *unstructured.Unstructured) cioVMStatus {
	if vm == nil {
		return cioVMStatus{Phase: cioVMPhaseMissing, Message: "CioVM missing"}
	}
	ready, found, err := unstructured.NestedBool(vm.Object, "status", "ready")
	if err == nil && found && ready {
		return cioVMStatus{Phase: cioVMPhaseReady, Message: "CioVM ready; suite not wired"}
	}
	printable, _, _ := unstructured.NestedString(vm.Object, "status", "printableStatus")
	if printable == "ErrorUnschedulable" || printable == "CrashLoopBackOff" || printable == "ErrImagePull" {
		return cioVMStatus{Phase: cioVMPhaseFailed, Message: "CioVM failed: " + printable}
	}
	if printable != "" {
		return cioVMStatus{Phase: cioVMPhasePending, Message: "CioVM " + printable}
	}
	return cioVMStatus{Phase: cioVMPhasePending, Message: "CioVM pending"}
}

// ensureCioVM gets or creates the CanarioVirtualMachine and returns its observed status.
func (r *CanarioRunReconciler) ensureCioVM(ctx context.Context, run *canariov1alpha1.CanarioRun) (cioVMStatus, error) {
	vm, err := r.getCioVM(ctx, run)
	if err == nil {
		return observeCioVM(vm), nil
	}
	if !apierrors.IsNotFound(err) {
		return cioVMStatus{}, err
	}

	nodeName, err := r.pickWorkerNode(ctx, run)
	if err != nil {
		return cioVMStatus{}, err
	}

	vm, err = r.createCioVM(ctx, run, nodeName)
	if err != nil {
		return cioVMStatus{}, err
	}
	return observeCioVM(vm), nil
}

func (r *CanarioRunReconciler) pickWorkerNode(ctx context.Context, run *canariov1alpha1.CanarioRun) (string, error) {
	workers := run.Spec.Workers
	if workers == "" {
		workers = canariov1alpha1.WorkersAll
	}

	// Explicit node name (not all/busiest).
	if workers != canariov1alpha1.WorkersAll && workers != canariov1alpha1.WorkersBusiest {
		return workers, nil
	}

	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}

	var names []string
	for i := range nodeList.Items {
		if isWorkerNode(&nodeList.Items[i]) {
			names = append(names, nodeList.Items[i].Name)
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no worker nodes found")
	}

	// v1 slice: one VM. all/busiest → first worker (load-based busiest later).
	return names[0], nil
}

func isWorkerNode(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, t := range node.Spec.Taints {
		if t.Key == "node-role.kubernetes.io/control-plane" || t.Key == "node-role.kubernetes.io/master" {
			if t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute {
				return false
			}
		}
	}
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		return false
	}
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		return false
	}
	return true
}

func newVirtualMachineForWatch() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(virtualMachineGVK)
	return u
}
