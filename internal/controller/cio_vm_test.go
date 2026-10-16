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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
	"github.com/rhpds/canario/internal/controller/suitepayload"
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

	It("builds CioVM names as run-workernode-timestamp", func() {
		now := time.Date(2026, 10, 16, 8, 3, 21, 0, time.UTC)
		name := newCioVMName("nightly-ceph", "worker-cluster-2bzjd-1", now)
		Expect(name).To(Equal("nightly-ceph-worker-cluster-2bzjd-1-20261016t080321"))
		Expect(len(name)).To(BeNumerically("<=", maxCioVMName))
		Expect(len(cioVMDataVolumeName(name, 0))).To(BeNumerically("<=", 63))
	})

	It("embeds a modular suite script with disks.json", func() {
		Expect(suitepayload.SuiteSH).To(ContainSubstring("run_one_disk"))
		Expect(suitepayload.SuiteSH).To(ContainSubstring("CANARIO_SUITE_SECTIONS"))
		disks := []resolvedDisk{{
			StorageClass: "ocs-rbd",
			Device:       "/dev/vdb",
			VolumeMode:   corev1.PersistentVolumeBlock,
			AccessMode:   corev1.ReadWriteOnce,
			Provisioner:  "openshift-storage.rbd.csi.ceph.com",
		}}
		ud := cloudInitUserData("storage,fileops", "worker-1", disks)
		Expect(ud).To(HavePrefix("#cloud-config"))
		Expect(ud).To(ContainSubstring("CANARIO_SUITE_SECTIONS=storage,fileops"))
		Expect(ud).To(ContainSubstring("CANARIO_DISKS_JSON_FILE=/etc/canario/disks.json"))
		Expect(ud).To(ContainSubstring("ocs-rbd"))
		Expect(ud).To(ContainSubstring("/dev/vdb"))
		Expect(ud).To(ContainSubstring("CANARIO_NODE_NAME=worker-1"))
		Expect(ud).To(ContainSubstring("/usr/local/bin/canario-suite"))
		Expect(strings.Count(ud, "fio")).To(BeNumerically(">", 0))
	})

	It("observes CioVM ready/failed/pending", func() {
		ready := &unstructured.Unstructured{Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "x"},
			"status":   map[string]interface{}{"ready": true},
		}}
		st := observeCioVM(ready)
		Expect(st.Phase).To(Equal(cioVMPhaseReady))
		Expect(st.Message).To(ContainSubstring("waiting for suite"))

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
