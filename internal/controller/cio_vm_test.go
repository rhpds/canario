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

		// Dedicated ODF storage/infra: tainted NoSchedule — skip.
		dedicated := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "storage-1",
				Labels: map[string]string{
					"node-role.kubernetes.io/worker":               "",
					"node-role.kubernetes.io/infra":                "",
					"cluster.ocs.openshift.io/openshift-storage":   "",
				},
			},
			Spec: corev1.NodeSpec{
				Taints: []corev1.Taint{{
					Key:    "node.ocs.openshift.io/storage",
					Value:  "true",
					Effect: corev1.TaintEffectNoSchedule,
				}},
			},
		}
		Expect(isWorkerNode(dedicated)).To(BeFalse())

		// Hyperconverged: storage label, no taint — still a valid worker.
		hc := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "hc-1",
				Labels: map[string]string{
					"node-role.kubernetes.io/worker":             "",
					"cluster.ocs.openshift.io/openshift-storage": "",
				},
			},
		}
		Expect(isWorkerNode(hc)).To(BeTrue())
	})

	It("aggregates multi-worker terminal phases", func() {
		workers := []canariov1alpha1.WorkerResult{
			{NodeName: "w1", Phase: canariov1alpha1.PhaseSucceeded},
			{NodeName: "w2", Phase: canariov1alpha1.PhaseFailed},
		}
		by := workerByNode(workers)
		Expect(by).To(HaveLen(2))
		Expect(isTerminal(by["w1"].Phase)).To(BeTrue())
		Expect(isTerminal(by["w2"].Phase)).To(BeTrue())
	})

	It("selects all vs busiest worker targets", func() {
		nodes := []scoredNode{
			{Name: "w1", CPUUtil: 0.2, MemUtil: 0.2},
			{Name: "w2", CPUUtil: 0.7, MemUtil: 0.5},
			{Name: "w3", CPUUtil: 0.4, MemUtil: 0.4},
		}
		all, err := selectWorkerTargets(canariov1alpha1.WorkersAll, nodes, 0.85, 0.75)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(Equal([]string{"w1", "w2", "w3"}))

		one, err := selectWorkerTargets(canariov1alpha1.WorkersBusiest, nodes, 0.85, 0.75)
		Expect(err).NotTo(HaveOccurred())
		Expect(one).To(Equal([]string{"w2"}))

		_, err = selectWorkerTargets(canariov1alpha1.WorkersAll, nil, 0.85, 0.75)
		Expect(err).To(HaveOccurred())
	})

	It("staggers creates by wall-clock interval, not Ready", func() {
		Expect(staggerWait(nil, time.Now())).To(Equal(time.Duration(0)))

		created := metav1.NewTime(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
		vm := &unstructured.Unstructured{}
		vm.SetCreationTimestamp(created)
		byNode := map[string]*unstructured.Unstructured{"w1": vm}

		now := created.Time.Add(3 * time.Second)
		Expect(staggerWait(byNode, now)).To(Equal(7 * time.Second))

		now = created.Time.Add(cioVMStaggerInterval)
		Expect(staggerWait(byNode, now)).To(Equal(time.Duration(0)))
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
		Expect(suitepayload.SuiteSH).To(ContainSubstring("run_guest_sections"))
		Expect(suitepayload.SuiteSH).To(ContainSubstring("probe_internet"))
		Expect(suitepayload.SuiteSH).To(ContainSubstring("CANARIO_SUITE_SECTIONS"))
		disks := []resolvedDisk{{
			StorageClass: "ocs-rbd",
			Device:       "/dev/vdb",
			VolumeMode:   corev1.PersistentVolumeBlock,
			AccessMode:   corev1.ReadWriteOnce,
			Provisioner:  "openshift-storage.rbd.csi.ceph.com",
		}}
		cfg := resolveSuiteConfig(&canariov1alpha1.CanarioRun{}, OperatorSuiteDefaults{})
		ud := cloudInitUserData(cfg, "worker-1", disks)
		Expect(ud).To(HavePrefix("#cloud-config"))
		Expect(ud).To(ContainSubstring("CANARIO_SUITE_SECTIONS=all"))
		Expect(ud).To(ContainSubstring("CANARIO_NETWORK=auto"))
		Expect(ud).To(ContainSubstring("CANARIO_DOWNLOAD_URL=http://speedtest.tele2.net/1MB.zip"))
		Expect(ud).To(ContainSubstring("stress-ng"))
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
