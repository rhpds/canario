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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

var _ = Describe("CanarioRun Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		namespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			run := &canariov1alpha1.CanarioRun{}
			err := k8sClient.Get(ctx, namespacedName, run)
			if err != nil && errors.IsNotFound(err) {
				resource := &canariov1alpha1.CanarioRun{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: canariov1alpha1.CanarioRunSpec{
						Type:    canariov1alpha1.RunTypeGuest,
						Profile: canariov1alpha1.ProfileSmall,
						Workers: canariov1alpha1.WorkersAll,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &canariov1alpha1.CanarioRun{}
			err := k8sClient.Get(ctx, namespacedName, resource)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should set Running when the CioVM is pending", func() {
			reconciler := &CanarioRunReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				VirtAvailable: virtAvailable(true),
				SyncCioVM: func(context.Context, *canariov1alpha1.CanarioRun) (cioVMStatus, error) {
					return cioVMStatus{Phase: cioVMPhasePending, Message: "CioVM Starting"}, nil
				},
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			updated := &canariov1alpha1.CanarioRun{}
			Expect(k8sClient.Get(ctx, namespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(canariov1alpha1.PhaseRunning))
			Expect(updated.Status.Message).To(Equal("CioVM Starting"))
		})

		It("should set Running when the CioVM is ready (suite not wired)", func() {
			reconciler := &CanarioRunReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				VirtAvailable: virtAvailable(true),
				SyncCioVM: func(context.Context, *canariov1alpha1.CanarioRun) (cioVMStatus, error) {
					return cioVMStatus{Phase: cioVMPhaseReady, Message: "CioVM ready; suite not wired"}, nil
				},
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			updated := &canariov1alpha1.CanarioRun{}
			Expect(k8sClient.Get(ctx, namespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(canariov1alpha1.PhaseRunning))
			Expect(updated.Status.Message).To(Equal("CioVM ready; suite not wired"))
		})

		It("should fail without syncing CioVM when virt is unavailable", func() {
			synced := false
			reconciler := &CanarioRunReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				VirtAvailable: virtAvailable(false),
				SyncCioVM: func(context.Context, *canariov1alpha1.CanarioRun) (cioVMStatus, error) {
					synced = true
					return cioVMStatus{}, nil
				},
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			updated := &canariov1alpha1.CanarioRun{}
			Expect(k8sClient.Get(ctx, namespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(canariov1alpha1.PhaseFailed))
			Expect(updated.Status.Message).To(Equal(virtUnavailableMessage))
			Expect(synced).To(BeFalse())
		})
	})
})

func virtAvailable(ok bool) func(context.Context) (bool, error) {
	return func(context.Context) (bool, error) { return ok, nil }
}
