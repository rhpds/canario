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
	"testing"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func cioVMScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := canariov1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateCioVMSetsEvictionStrategyNone(t *testing.T) {
	scheme := cioVMScheme(t)
	run := &canariov1alpha1.CanarioRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "run-hostpath",
			Namespace: "canario-operator",
			UID:       "run-uid",
		},
		Spec: canariov1alpha1.CanarioRunSpec{
			Type:    canariov1alpha1.RunTypeGuest,
			Profile: canariov1alpha1.ProfileSmall,
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(run).Build()
	r := &CanarioRunReconciler{Client: c, Scheme: scheme}

	disks := []resolvedDisk{{
		StorageClass: "hostpath-csi",
		AccessMode:   corev1.ReadWriteOnce,
		VolumeMode:   corev1.PersistentVolumeFilesystem,
	}}
	boot := bootSource{
		Kind:                 "dataSource",
		DataSourceName:       "fedora",
		DataSourceNamespace:  "openshift-virtualization-os-images",
	}

	vm, err := r.createCioVM(context.Background(), run, "worker-1", disks, boot)
	if err != nil {
		t.Fatalf("createCioVM: %v", err)
	}

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(virtualMachineGVK)
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(vm), got); err != nil {
		t.Fatalf("get VM: %v", err)
	}
	eviction, ok, err := unstructured.NestedString(got.Object, "spec", "template", "spec", "evictionStrategy")
	if err != nil {
		t.Fatalf("read evictionStrategy: %v", err)
	}
	if !ok || eviction != "None" {
		t.Fatalf("evictionStrategy=%q ok=%v; want None (RWO/hostpath CioVMs must not inherit cluster LiveMigrate)", eviction, ok)
	}
}
