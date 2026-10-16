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
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

// scoredNode is a worker that passed CPU + memory + storage ceilings.
type scoredNode struct {
	Name         string
	CPUUtil      float64
	MemUtil      float64
	StorageUtil  float64
}

// util returns max(cpu, mem, storage) for busiest ranking.
func (s scoredNode) util() float64 {
	u := s.CPUUtil
	if s.MemUtil > u {
		u = s.MemUtil
	}
	if s.StorageUtil > u {
		u = s.StorageUtil
	}
	return u
}

// resolveWorkerNodes returns target workers for this Run.
// all → eligible workers; busiest → highest util among eligible;
// explicit Spec.Workers=<node> bypasses thresholds.
func (r *CanarioRunReconciler) resolveWorkerNodes(ctx context.Context, run *canariov1alpha1.CanarioRun, disks []resolvedDisk) ([]string, error) {
	workers := run.Spec.Workers
	if workers == "" {
		workers = canariov1alpha1.WorkersAll
	}

	if workers != canariov1alpha1.WorkersAll && workers != canariov1alpha1.WorkersBusiest {
		return []string{workers}, nil
	}

	cpuStr, memStr, _ := profileResources(run.Spec.Profile)
	cpuReq, err := resource.ParseQuantity(cpuStr)
	if err != nil {
		return nil, fmt.Errorf("profile cpu: %w", err)
	}
	memReq, err := resource.ParseQuantity(memStr)
	if err != nil {
		return nil, fmt.Errorf("profile memory: %w", err)
	}

	nodeCeiling := r.nodeUsageCeiling()
	storageCeiling := r.storageUsageCeiling()
	eligible, err := r.listEligibleWorkers(ctx, disks, cpuReq, memReq, nodeCeiling, storageCeiling)
	if err != nil {
		return nil, err
	}
	return selectWorkerTargets(workers, eligible, nodeCeiling, storageCeiling)
}

// selectWorkerTargets applies workers=all|busiest to an eligible scored list.
func selectWorkerTargets(mode string, nodes []scoredNode, nodeCeiling, storageCeiling float64) ([]string, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no eligible worker nodes (need CPU/mem under %.0f%% and storage under %.0f%%)", nodeCeiling*100, storageCeiling*100)
	}
	if mode == canariov1alpha1.WorkersBusiest {
		best := nodes[0]
		for i := 1; i < len(nodes); i++ {
			if nodes[i].util() > best.util() {
				best = nodes[i]
			} else if nodes[i].util() == best.util() && nodes[i].Name < best.Name {
				best = nodes[i]
			}
		}
		return []string{best.Name}, nil
	}
	names := make([]string, len(nodes))
	for i := range nodes {
		names[i] = nodes[i].Name
	}
	sort.Strings(names)
	return names, nil
}

func (r *CanarioRunReconciler) listEligibleWorkers(ctx context.Context, disks []resolvedDisk, cpuReq, memReq resource.Quantity, nodeCeiling, storageCeiling float64) ([]scoredNode, error) {
	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	usage, err := r.nodeRequestUsage(ctx)
	if err != nil {
		return nil, err
	}

	storage, err := r.resolveStorageSignal(ctx, disks)
	if err != nil {
		return nil, err
	}

	var out []scoredNode
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if !isWorkerNode(node) {
			continue
		}
		if nodeHasPressure(node) {
			continue
		}
		u := usage[node.Name]
		storageUtil := storageUtilForNode(node, u.Ephemeral, storage)
		score, ok := scoreIfEligible(node, u, cpuReq, memReq, storageUtil, nodeCeiling, storageCeiling)
		if !ok {
			continue
		}
		out = append(out, score)
	}
	return out, nil
}

func storageUtilForNode(node *corev1.Node, ephemeralUsed resource.Quantity, sig storageSignal) float64 {
	if sig.SharedUtil != nil {
		return *sig.SharedUtil
	}
	if sig.UseNodeEphemeral {
		return nodeEphemeralUtil(node, ephemeralUsed)
	}
	return 0
}

// nodeRequestUsage sums pod requests per node (Running/Pending only).
func (r *CanarioRunReconciler) nodeRequestUsage(ctx context.Context) (map[string]resourcePair, error) {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods); err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	out := make(map[string]resourcePair)
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Spec.NodeName == "" {
			continue
		}
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		u := out[p.Spec.NodeName]
		u.CPU.Add(podRequestCPU(p))
		u.Memory.Add(podRequestMemory(p))
		u.Ephemeral.Add(podRequestEphemeral(p))
		out[p.Spec.NodeName] = u
	}
	return out, nil
}

type resourcePair struct {
	CPU       resource.Quantity
	Memory    resource.Quantity
	Ephemeral resource.Quantity
}

func podRequestCPU(p *corev1.Pod) resource.Quantity {
	total := resource.Quantity{}
	for _, c := range p.Spec.Containers {
		if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
			total.Add(q)
		}
	}
	return total
}

func podRequestMemory(p *corev1.Pod) resource.Quantity {
	total := resource.Quantity{}
	for _, c := range p.Spec.Containers {
		if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
			total.Add(q)
		}
	}
	return total
}

func podRequestEphemeral(p *corev1.Pod) resource.Quantity {
	total := resource.Quantity{}
	for _, c := range p.Spec.Containers {
		if q, ok := c.Resources.Requests[corev1.ResourceEphemeralStorage]; ok {
			total.Add(q)
		}
	}
	return total
}

func nodeHasPressure(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		switch c.Type {
		case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure:
			if c.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}

// scoreIfEligible requires CPU, memory, and storage all under their ceilings.
func scoreIfEligible(node *corev1.Node, used resourcePair, cpuReq, memReq resource.Quantity, storageUtil, nodeCeiling, storageCeiling float64) (scoredNode, bool) {
	if storageUtil >= storageCeiling {
		return scoredNode{}, false
	}

	allocCPU := node.Status.Allocatable.Cpu()
	allocMem := node.Status.Allocatable.Memory()
	if allocCPU == nil || allocMem == nil || allocCPU.IsZero() || allocMem.IsZero() {
		return scoredNode{}, false
	}

	freeCPU := allocCPU.DeepCopy()
	freeCPU.Sub(used.CPU)
	freeMem := allocMem.DeepCopy()
	freeMem.Sub(used.Memory)
	if freeCPU.Cmp(cpuReq) < 0 || freeMem.Cmp(memReq) < 0 {
		return scoredNode{}, false
	}

	usedCPUAfter := used.CPU.DeepCopy()
	usedCPUAfter.Add(cpuReq)
	usedMemAfter := used.Memory.DeepCopy()
	usedMemAfter.Add(memReq)
	if frac(usedCPUAfter, *allocCPU) >= nodeCeiling {
		return scoredNode{}, false
	}
	if frac(usedMemAfter, *allocMem) >= nodeCeiling {
		return scoredNode{}, false
	}

	return scoredNode{
		Name:        node.Name,
		CPUUtil:     frac(used.CPU, *allocCPU),
		MemUtil:     frac(used.Memory, *allocMem),
		StorageUtil: storageUtil,
	}, true
}

func frac(used, alloc resource.Quantity) float64 {
	if alloc.IsZero() {
		return 1
	}
	return float64(used.MilliValue()) / float64(alloc.MilliValue())
}
