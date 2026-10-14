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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

var _ = Describe("CioVM helpers", func() {
	It("maps profiles to resources", func() {
		cpu, mem, disk := profileResources(canariov1alpha1.ProfileSmall)
		Expect(cpu).To(Equal("1"))
		Expect(mem).To(Equal("2Gi"))
		Expect(disk).To(Equal("15Gi"))

		cpu, mem, disk = profileResources(canariov1alpha1.ProfileDefault)
		Expect(cpu).To(Equal("2"))
		Expect(mem).To(Equal("4Gi"))
		Expect(disk).To(Equal("30Gi"))
	})

	It("detects worker nodes", func() {
		worker := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w1", Labels: map[string]string{"node-role.kubernetes.io/worker": ""}}}
		Expect(isWorkerNode(worker)).To(BeTrue())

		cp := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "m1", Labels: map[string]string{"node-role.kubernetes.io/control-plane": ""}}}
		Expect(isWorkerNode(cp)).To(BeFalse())
	})

	It("observes CioVM ready/failed/pending", func() {
		ready := &unstructured.Unstructured{Object: map[string]interface{}{
			"status": map[string]interface{}{"ready": true},
		}}
		Expect(observeCioVM(ready).Phase).To(Equal(cioVMPhaseReady))

		failed := &unstructured.Unstructured{Object: map[string]interface{}{
			"status": map[string]interface{}{"printableStatus": "CrashLoopBackOff"},
		}}
		Expect(observeCioVM(failed).Phase).To(Equal(cioVMPhaseFailed))

		pending := &unstructured.Unstructured{Object: map[string]interface{}{
			"status": map[string]interface{}{"printableStatus": "Starting"},
		}}
		Expect(observeCioVM(pending).Phase).To(Equal(cioVMPhasePending))
	})
})
