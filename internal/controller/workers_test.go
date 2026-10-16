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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func workerNode(name, cpuAlloc, memAlloc string, usedCPU, usedMem string) (*corev1.Node, resourcePair) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"node-role.kubernetes.io/worker": ""},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuAlloc),
				corev1.ResourceMemory: resource.MustParse(memAlloc),
			},
		},
	}
	return n, resourcePair{
		CPU:    resource.MustParse(usedCPU),
		Memory: resource.MustParse(usedMem),
	}
}

var _ = Describe("worker eligibility", func() {
	cpuReq := resource.MustParse("1")
	memReq := resource.MustParse("2Gi")

	It("accepts a node with headroom under the ceiling", func() {
		// 8 CPU / 16Gi; used 4 / 8Gi; after +1/2Gi → 5/8=0.625, 10/16=0.625
		node, used := workerNode("w1", "8", "16Gi", "4", "8Gi")
		score, ok := scoreIfEligible(node, used, cpuReq, memReq, 0.2, 0.85, 0.75)
		Expect(ok).To(BeTrue())
		Expect(score.Name).To(Equal("w1"))
		Expect(score.CPUUtil).To(BeNumerically("~", 0.5, 0.01))
		Expect(score.MemUtil).To(BeNumerically("~", 0.5, 0.01))
		Expect(score.StorageUtil).To(Equal(0.2))
	})

	It("rejects when free resources cannot fit the profile", func() {
		node, used := workerNode("w1", "8", "16Gi", "7.5", "15Gi")
		_, ok := scoreIfEligible(node, used, cpuReq, memReq, 0.2, 0.85, 0.75)
		Expect(ok).To(BeFalse())
	})

	It("rejects when placing the CioVM would hit the usage ceiling", func() {
		// 10 CPU; used 7.6; +1 → 8.6/10 = 0.86 >= 0.85
		node, used := workerNode("w1", "10", "32Gi", "7600m", "8Gi")
		_, ok := scoreIfEligible(node, used, cpuReq, memReq, 0.2, 0.85, 0.75)
		Expect(ok).To(BeFalse())
	})

	It("rejects when storage is over the storage ceiling", func() {
		node, used := workerNode("w1", "8", "16Gi", "1", "1Gi")
		_, ok := scoreIfEligible(node, used, cpuReq, memReq, 0.80, 0.85, 0.75)
		Expect(ok).To(BeFalse())
	})

	It("rejects MemoryPressure nodes", func() {
		node, _ := workerNode("w1", "8", "16Gi", "1", "1Gi")
		node.Status.Conditions = []corev1.NodeCondition{{
			Type:   corev1.NodeMemoryPressure,
			Status: corev1.ConditionTrue,
		}}
		Expect(nodeHasPressure(node)).To(BeTrue())
	})

	It("picks busiest among eligible and sorts all", func() {
		nodes := []scoredNode{
			{Name: "a", CPUUtil: 0.3, MemUtil: 0.3},
			{Name: "b", CPUUtil: 0.1, MemUtil: 0.8},
			{Name: "c", CPUUtil: 0.5, MemUtil: 0.5},
		}
		busy, err := selectWorkerTargets("busiest", nodes, 0.85, 0.75)
		Expect(err).NotTo(HaveOccurred())
		Expect(busy).To(Equal([]string{"b"}))

		all, err := selectWorkerTargets("all", nodes, 0.85, 0.75)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(Equal([]string{"a", "b", "c"}))
	})
})
