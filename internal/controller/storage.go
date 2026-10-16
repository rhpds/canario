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
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

var storageProfileGVK = schema.GroupVersionKind{
	Group:   "cdi.kubevirt.io",
	Version: "v1beta1",
	Kind:    "StorageProfile",
}

// resolvedDisk is a validated StorageClass ready to attach as a guest data disk.
type resolvedDisk struct {
	StorageClass string
	VolumeMode   corev1.PersistentVolumeMode
	AccessMode   corev1.PersistentVolumeAccessMode
	Provisioner  string
	Device       string // guest path, e.g. /dev/vdb
}

// provisionerDenySubstrings match object/OBC-style provisioners (not VM disks).
var provisionerDenySubstrings = []string{
	"noobaa",
	"openshift-storage.noobaa.io",
	"obc",
	"bucket",
	"rgw",
}

func isDeniedProvisioner(provisioner string) bool {
	p := strings.ToLower(provisioner)
	for _, d := range provisionerDenySubstrings {
		if strings.Contains(p, d) {
			return true
		}
	}
	return false
}

// resolveDataDisks returns validated disks for the Run (default SC or explicit list).
func (r *CanarioRunReconciler) resolveDataDisks(ctx context.Context, run *canariov1alpha1.CanarioRun) ([]resolvedDisk, error) {
	mode := run.Spec.Storage
	if mode == "" {
		mode = canariov1alpha1.StorageDefault
	}

	var names []string
	switch mode {
	case canariov1alpha1.StorageDefault:
		sc, err := r.defaultStorageClass(ctx)
		if err != nil {
			return nil, err
		}
		names = []string{sc}
	case canariov1alpha1.StorageList:
		if len(run.Spec.StorageClasses) == 0 {
			return nil, fmt.Errorf("storage=list requires spec.storageClasses")
		}
		names = append([]string{}, run.Spec.StorageClasses...)
	default:
		return nil, fmt.Errorf("unsupported storage mode %q", mode)
	}

	out := make([]resolvedDisk, 0, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("empty storageClass name in list")
		}
		d, err := r.validateStorageClass(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("storageClass %q: %w", name, err)
		}
		d.Device = fmt.Sprintf("/dev/vd%c", 'b'+rune(i))
		if i > 24 {
			return nil, fmt.Errorf("too many data disks (max 25)")
		}
		out = append(out, d)
	}
	return out, nil
}

func (r *CanarioRunReconciler) defaultStorageClass(ctx context.Context) (string, error) {
	list := &storagev1.StorageClassList{}
	if err := r.List(ctx, list); err != nil {
		return "", fmt.Errorf("list StorageClasses: %w", err)
	}
	for i := range list.Items {
		sc := &list.Items[i]
		if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			return sc.Name, nil
		}
	}
	return "", fmt.Errorf("no default StorageClass in cluster")
}

func (r *CanarioRunReconciler) validateStorageClass(ctx context.Context, name string) (resolvedDisk, error) {
	sc := &storagev1.StorageClass{}
	if err := r.Get(ctx, client.ObjectKey{Name: name}, sc); err != nil {
		return resolvedDisk{}, err
	}
	if isDeniedProvisioner(sc.Provisioner) {
		return resolvedDisk{}, fmt.Errorf("provisioner %q is not a VM data disk (denied)", sc.Provisioner)
	}

	volumeMode := corev1.PersistentVolumeFilesystem
	accessMode := corev1.ReadWriteOnce

	profile := &unstructured.Unstructured{}
	profile.SetGroupVersionKind(storageProfileGVK)
	if err := r.Get(ctx, client.ObjectKey{Name: name}, profile); err != nil {
		if apierrors.IsNotFound(err) {
			return resolvedDisk{}, fmt.Errorf("StorageProfile missing (CDI incomplete)")
		}
		// API missing in envtest — fall back to RWO Filesystem if SC exists and not denied.
		if metaIsNoMatch(err) {
			return resolvedDisk{
				StorageClass: name,
				VolumeMode:   volumeMode,
				AccessMode:   accessMode,
				Provisioner:  sc.Provisioner,
			}, nil
		}
		return resolvedDisk{}, err
	}

	sets, found, err := unstructured.NestedSlice(profile.Object, "status", "claimPropertySets")
	if err != nil {
		return resolvedDisk{}, err
	}
	if !found || len(sets) == 0 {
		return resolvedDisk{}, fmt.Errorf("StorageProfile has empty claimPropertySets")
	}

	rwoFound := false
	for _, raw := range sets {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		modes, _, _ := unstructured.NestedStringSlice(m, "accessModes")
		hasRWO := false
		for _, am := range modes {
			if am == string(corev1.ReadWriteOnce) {
				hasRWO = true
				break
			}
		}
		if !hasRWO {
			continue
		}
		rwoFound = true
		accessMode = corev1.ReadWriteOnce
		if vm, ok, _ := unstructured.NestedString(m, "volumeMode"); ok && vm != "" {
			volumeMode = corev1.PersistentVolumeMode(vm)
		}
		break
	}
	if !rwoFound {
		return resolvedDisk{}, fmt.Errorf("StorageProfile has no ReadWriteOnce claimPropertySet")
	}

	return resolvedDisk{
		StorageClass: name,
		VolumeMode:   volumeMode,
		AccessMode:   accessMode,
		Provisioner:  sc.Provisioner,
	}, nil
}

func metaIsNoMatch(err error) bool {
	if err == nil {
		return false
	}
	return meta.IsNoMatchError(err) ||
		strings.Contains(err.Error(), "no matches for kind") ||
		strings.Contains(err.Error(), "no kind is registered") ||
		strings.Contains(err.Error(), "could not find the requested resource")
}
