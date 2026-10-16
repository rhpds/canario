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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("storage capacity", func() {
	It("detects ceph provisioners", func() {
		Expect(isCephProvisioner("openshift-storage.rbd.csi.ceph.com")).To(BeTrue())
		Expect(isCephProvisioner("kubernetes.io/no-provisioner")).To(BeFalse())
		Expect(disksNeedCephHeadroom([]resolvedDisk{{Provisioner: "openshift-storage.rbd.csi.ceph.com"}})).To(BeTrue())
		Expect(disksNeedCephHeadroom([]resolvedDisk{{Provisioner: "pd.csi.storage.gke.io"}})).To(BeFalse())
	})

	It("reads CephCluster usage fraction", func() {
		cluster := &unstructured.Unstructured{Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "ocs", "namespace": "openshift-storage"},
			"status": map[string]interface{}{
				"ceph": map[string]interface{}{
					"health": "HEALTH_OK",
					"capacity": map[string]interface{}{
						"bytesUsed":  int64(80),
						"bytesTotal": int64(100),
					},
				},
			},
		}}
		util, err := cephClusterUsage(cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(util).To(BeNumerically("~", 0.8, 0.001))
	})

	It("errors on HEALTH_ERR", func() {
		cluster := &unstructured.Unstructured{Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "ocs", "namespace": "openshift-storage"},
			"status": map[string]interface{}{
				"ceph": map[string]interface{}{"health": "HEALTH_ERR"},
			},
		}}
		_, err := cephClusterUsage(cluster)
		Expect(err).To(MatchError(ContainSubstring("HEALTH_ERR")))
	})

	It("uses shared util for ceph and ephemeral for local", func() {
		shared := 0.7
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "w1"},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
				},
			},
		}
		Expect(storageUtilForNode(node, resource.MustParse("10Gi"), storageSignal{SharedUtil: &shared})).To(Equal(0.7))
		Expect(storageUtilForNode(node, resource.MustParse("40Gi"), storageSignal{UseNodeEphemeral: true})).To(BeNumerically("~", 0.4, 0.01))
	})

	It("parses fraction env forms", func() {
		Expect(parseFraction("0.85")).To(Equal(0.85))
		Expect(parseFraction("85")).To(Equal(0.85))
		Expect(parseFraction("85%")).To(Equal(0.85))
	})
})
