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
)

var _ = Describe("storage helpers", func() {
	It("denies object/NooBaa provisioners", func() {
		Expect(isDeniedProvisioner("openshift-storage.noobaa.io/obc")).To(BeTrue())
		Expect(isDeniedProvisioner("noobaa.io")).To(BeTrue())
		Expect(isDeniedProvisioner("openshift-storage.rbd.csi.ceph.com")).To(BeFalse())
		Expect(isDeniedProvisioner("openshift-storage.cephfs.csi.ceph.com")).To(BeFalse())
	})
})
