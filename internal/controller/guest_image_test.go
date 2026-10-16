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

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

var _ = Describe("guest image boot source", func() {
	It("errors when Run and operator defaults are both empty", func() {
		_, err := resolveBootSource(&canariov1alpha1.CanarioRun{}, OperatorGuestDefaults{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("defaults missing"))
	})

	It("uses operator DataSource defaults when Run omits guestImage", func() {
		boot, err := resolveBootSource(&canariov1alpha1.CanarioRun{}, OperatorGuestDefaults{
			DataSourceName:      "rhel9",
			DataSourceNamespace: "openshift-virtualization-os-images",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(boot.DataSourceName).To(Equal("rhel9"))
		Expect(boot.DataSourceNamespace).To(Equal("openshift-virtualization-os-images"))
	})

	It("uses operator registry default", func() {
		boot, err := resolveBootSource(&canariov1alpha1.CanarioRun{}, OperatorGuestDefaults{
			Registry: "quay.io/containerdisks/fedora:40",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(boot.Kind).To(Equal("registry"))
		Expect(boot.RegistryURL).To(Equal("docker://quay.io/containerdisks/fedora:40"))
	})

	It("Run guestImage overrides operator defaults", func() {
		boot, err := resolveBootSource(&canariov1alpha1.CanarioRun{
			Spec: canariov1alpha1.CanarioRunSpec{
				GuestImage: &canariov1alpha1.GuestImage{
					DataSource: &canariov1alpha1.GuestDataSourceRef{
						Name:      "centos-stream9",
						Namespace: "custom-ns",
					},
				},
			},
		}, OperatorGuestDefaults{
			DataSourceName:      "rhel9",
			DataSourceNamespace: "openshift-virtualization-os-images",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(boot.DataSourceName).To(Equal("centos-stream9"))
		Expect(boot.DataSourceNamespace).To(Equal("custom-ns"))
	})

	It("requires dataSource namespace on Run", func() {
		_, err := resolveBootSource(&canariov1alpha1.CanarioRun{
			Spec: canariov1alpha1.CanarioRunSpec{
				GuestImage: &canariov1alpha1.GuestImage{
					DataSource: &canariov1alpha1.GuestDataSourceRef{Name: "fedora"},
				},
			},
		}, OperatorGuestDefaults{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("namespace"))
	})
})
