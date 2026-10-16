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
	"os"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

const (
	envDefaultGuestDataSourceName = "CANARIO_DEFAULT_GUEST_DATASOURCE_NAME"
	envDefaultGuestDataSourceNS   = "CANARIO_DEFAULT_GUEST_DATASOURCE_NAMESPACE"
	envDefaultGuestRegistry       = "CANARIO_DEFAULT_GUEST_REGISTRY"
)

var dataSourceGVK = schema.GroupVersionKind{
	Group:   "cdi.kubevirt.io",
	Version: "v1beta1",
	Kind:    "DataSource",
}

// OperatorGuestDefaults are install-time boot defaults (env / ConfigMap).
// No built-in image or namespace — the installer sets these per cluster.
type OperatorGuestDefaults struct {
	DataSourceName      string
	DataSourceNamespace string
	Registry            string
}

// GuestDefaultsFromEnv reads CANARIO_DEFAULT_GUEST_* from the process environment.
func GuestDefaultsFromEnv() OperatorGuestDefaults {
	return OperatorGuestDefaults{
		DataSourceName:      strings.TrimSpace(os.Getenv(envDefaultGuestDataSourceName)),
		DataSourceNamespace: strings.TrimSpace(os.Getenv(envDefaultGuestDataSourceNS)),
		Registry:            strings.TrimSpace(os.Getenv(envDefaultGuestRegistry)),
	}
}

// bootSource is the resolved root-disk CDI source for a CioVM.
type bootSource struct {
	Kind string // "dataSource" | "registry"
	// DataSource
	DataSourceName      string
	DataSourceNamespace string
	// Registry
	RegistryURL string
}

func resolveBootSource(run *canariov1alpha1.CanarioRun, defaults OperatorGuestDefaults) (bootSource, error) {
	if run.Spec.GuestImage != nil {
		return bootSourceFromGuestImage(run.Spec.GuestImage)
	}
	return bootSourceFromDefaults(defaults)
}

func bootSourceFromGuestImage(gi *canariov1alpha1.GuestImage) (bootSource, error) {
	hasDS := gi.DataSource != nil && strings.TrimSpace(gi.DataSource.Name) != ""
	hasReg := strings.TrimSpace(gi.Registry) != ""
	if hasDS && hasReg {
		return bootSource{}, fmt.Errorf("guestImage: set only one of dataSource or registry")
	}
	if !hasDS && !hasReg {
		return bootSource{}, fmt.Errorf("guestImage: set dataSource (name+namespace) or registry")
	}
	if hasReg {
		return registryBoot(gi.Registry)
	}
	ns := strings.TrimSpace(gi.DataSource.Namespace)
	if ns == "" {
		return bootSource{}, fmt.Errorf("guestImage.dataSource.namespace is required")
	}
	return bootSource{
		Kind:                "dataSource",
		DataSourceName:      strings.TrimSpace(gi.DataSource.Name),
		DataSourceNamespace: ns,
	}, nil
}

func bootSourceFromDefaults(d OperatorGuestDefaults) (bootSource, error) {
	hasDS := d.DataSourceName != "" || d.DataSourceNamespace != ""
	hasReg := d.Registry != ""
	if hasDS && hasReg {
		return bootSource{}, fmt.Errorf("operator guest defaults: set DataSource name+namespace or registry, not both")
	}
	if hasReg {
		return registryBoot(d.Registry)
	}
	if d.DataSourceName == "" || d.DataSourceNamespace == "" {
		return bootSource{}, fmt.Errorf("guestImage unset and operator defaults missing: set spec.guestImage or %s+%s (or %s)",
			envDefaultGuestDataSourceName, envDefaultGuestDataSourceNS, envDefaultGuestRegistry)
	}
	return bootSource{
		Kind:                "dataSource",
		DataSourceName:      d.DataSourceName,
		DataSourceNamespace: d.DataSourceNamespace,
	}, nil
}

func registryBoot(url string) (bootSource, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return bootSource{}, fmt.Errorf("empty registry URL")
	}
	if !strings.Contains(url, "://") {
		url = "docker://" + url
	}
	return bootSource{Kind: "registry", RegistryURL: url}, nil
}

func (r *CanarioRunReconciler) ensureBootSourceReady(ctx context.Context, boot bootSource) error {
	if boot.Kind != "dataSource" {
		return nil
	}
	ds := &unstructured.Unstructured{}
	ds.SetGroupVersionKind(dataSourceGVK)
	key := client.ObjectKey{Name: boot.DataSourceName, Namespace: boot.DataSourceNamespace}
	if err := r.Get(ctx, key, ds); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("DataSource %s/%s not found", boot.DataSourceNamespace, boot.DataSourceName)
		}
		return err
	}
	conds, found, err := unstructured.NestedSlice(ds.Object, "status", "conditions")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("DataSource %s/%s has no status conditions", boot.DataSourceNamespace, boot.DataSourceName)
	}
	for _, raw := range conds {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "Ready" && m["status"] == "True" {
			return nil
		}
	}
	return fmt.Errorf("DataSource %s/%s is not Ready", boot.DataSourceNamespace, boot.DataSourceName)
}

func cioVMRootVolumeName(vmName string) string {
	return vmName + "-root"
}

// rootDataVolumeTemplate builds the boot-disk DataVolumeTemplate entry.
// storageClassName is used for the cloned/pulled PVC (typically first data SC or default).
func rootDataVolumeTemplate(vmName string, boot bootSource, size, storageClass string) map[string]interface{} {
	storage := map[string]interface{}{
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"storage": size,
			},
		},
		"accessModes": []interface{}{"ReadWriteOnce"},
	}
	if storageClass != "" {
		storage["storageClassName"] = storageClass
	}

	spec := map[string]interface{}{
		"storage": storage,
	}
	switch boot.Kind {
	case "dataSource":
		spec["sourceRef"] = map[string]interface{}{
			"kind":      "DataSource",
			"name":      boot.DataSourceName,
			"namespace": boot.DataSourceNamespace,
		}
	case "registry":
		spec["source"] = map[string]interface{}{
			"registry": map[string]interface{}{
				"url": boot.RegistryURL,
			},
		}
	}

	return map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": cioVMRootVolumeName(vmName),
		},
		"spec": spec,
	}
}
