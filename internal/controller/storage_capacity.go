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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var cephClusterListGVK = schema.GroupVersionKind{
	Group:   "ceph.rook.io",
	Version: "v1",
	Kind:    "CephClusterList",
}

// storageSignal is the storage utilization input for "can I run here?".
// Shared backends (e.g. Ceph/ODF) use one cluster-wide util for every worker.
// Otherwise we use the node's ephemeral-storage requests vs allocatable
// (covers local / node-disk pressure without assuming a product).
type storageSignal struct {
	SharedUtil       *float64 // set for shared capacity backends
	UseNodeEphemeral bool
}

func (r *CanarioRunReconciler) storageUsageCeiling() float64 {
	if r.Thresholds.StorageUsageCeiling > 0 && r.Thresholds.StorageUsageCeiling <= 1 {
		return r.Thresholds.StorageUsageCeiling
	}
	return defaultStorageUsageCeiling
}

func (r *CanarioRunReconciler) nodeUsageCeiling() float64 {
	if r.Thresholds.NodeUsageCeiling > 0 && r.Thresholds.NodeUsageCeiling <= 1 {
		return r.Thresholds.NodeUsageCeiling
	}
	return defaultNodeUsageCeiling
}

// resolveStorageSignal picks how to measure storage for the Run's disks.
func (r *CanarioRunReconciler) resolveStorageSignal(ctx context.Context, disks []resolvedDisk) (storageSignal, error) {
	if disksNeedCephHeadroom(disks) {
		util, err := r.maxCephUsage(ctx)
		if err != nil {
			return storageSignal{}, err
		}
		u := util
		return storageSignal{SharedUtil: &u}, nil
	}
	return storageSignal{UseNodeEphemeral: true}, nil
}

func disksNeedCephHeadroom(disks []resolvedDisk) bool {
	for _, d := range disks {
		if isCephProvisioner(d.Provisioner) {
			return true
		}
	}
	return false
}

func isCephProvisioner(provisioner string) bool {
	p := strings.ToLower(provisioner)
	return strings.Contains(p, "rbd.csi.ceph") ||
		strings.Contains(p, "cephfs.csi.ceph") ||
		strings.Contains(p, "ceph.com")
}

func (r *CanarioRunReconciler) maxCephUsage(ctx context.Context) (float64, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(cephClusterListGVK)
	if err := r.List(ctx, list); err != nil {
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			return 0, fmt.Errorf("ceph StorageClass in use but CephCluster API unavailable")
		}
		return 0, fmt.Errorf("list CephCluster: %w", err)
	}
	if len(list.Items) == 0 {
		return 0, fmt.Errorf("ceph StorageClass in use but no CephCluster found")
	}

	maxUtil := 0.0
	for i := range list.Items {
		util, err := cephClusterUsage(&list.Items[i])
		if err != nil {
			return 0, err
		}
		if util > maxUtil {
			maxUtil = util
		}
	}
	return maxUtil, nil
}

func cephClusterUsage(cluster *unstructured.Unstructured) (float64, error) {
	name := cluster.GetNamespace() + "/" + cluster.GetName()
	health, _, _ := unstructured.NestedString(cluster.Object, "status", "ceph", "health")
	if health == "HEALTH_ERR" {
		return 1, fmt.Errorf("CephCluster %s is HEALTH_ERR", name)
	}

	used, foundUsed, err := unstructured.NestedInt64(cluster.Object, "status", "ceph", "capacity", "bytesUsed")
	if err != nil || !foundUsed {
		return 0, fmt.Errorf("CephCluster %s: missing status.ceph.capacity.bytesUsed", name)
	}
	total, foundTotal, err := unstructured.NestedInt64(cluster.Object, "status", "ceph", "capacity", "bytesTotal")
	if err != nil || !foundTotal || total <= 0 {
		return 0, fmt.Errorf("CephCluster %s: missing status.ceph.capacity.bytesTotal", name)
	}
	return float64(used) / float64(total), nil
}

func nodeEphemeralUtil(node *corev1.Node, used resource.Quantity) float64 {
	alloc := node.Status.Allocatable.StorageEphemeral()
	if alloc == nil || alloc.IsZero() {
		return 0
	}
	return frac(used, *alloc)
}
