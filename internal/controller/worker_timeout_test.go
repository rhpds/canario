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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

var _ = Describe("worker suite timeout helpers", func() {
	It("sets SuiteStartedTime once", func() {
		wr := canariov1alpha1.WorkerResult{}
		t1 := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		ensureWorkerSuiteStarted(&wr, t1)
		Expect(wr.SuiteStartedTime).NotTo(BeNil())
		Expect(wr.SuiteStartedTime.Time).To(Equal(t1))

		ensureWorkerSuiteStarted(&wr, t1.Add(time.Hour))
		Expect(wr.SuiteStartedTime.Time).To(Equal(t1))
	})

	It("does not time out before deadline", func() {
		start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		st := metav1.NewTime(start)
		wr := canariov1alpha1.WorkerResult{SuiteStartedTime: &st}
		obs := cioVMStatus{Phase: cioVMPhasePending, Message: "CioVM Provisioning"}
		timedOut, _ := workerSuiteTimedOut(wr, obs, 30*time.Minute, start.Add(29*time.Minute))
		Expect(timedOut).To(BeFalse())
	})

	It("times out when not Ready", func() {
		start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		st := metav1.NewTime(start)
		wr := canariov1alpha1.WorkerResult{SuiteStartedTime: &st}
		obs := cioVMStatus{Phase: cioVMPhasePending, Message: "CioVM Provisioning"}
		timedOut, msg := workerSuiteTimedOut(wr, obs, 30*time.Minute, start.Add(30*time.Minute+time.Second))
		Expect(timedOut).To(BeTrue())
		Expect(msg).To(ContainSubstring("CioVM not ready before timeout"))
		Expect(msg).To(ContainSubstring("Provisioning"))
	})

	It("times out when Ready but suite unfinished", func() {
		start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		st := metav1.NewTime(start)
		wr := canariov1alpha1.WorkerResult{SuiteStartedTime: &st}
		obs := cioVMStatus{Phase: cioVMPhaseReady}
		timedOut, msg := workerSuiteTimedOut(wr, obs, 30*time.Minute, start.Add(31*time.Minute))
		Expect(timedOut).To(BeTrue())
		Expect(msg).To(Equal("suite timeout"))
	})
})

var _ = Describe("aggregateRunOutcome", func() {
	It("maps mix to Degraded", func() {
		phase, suite, msg := aggregateRunOutcome(true, true)
		Expect(phase).To(Equal(canariov1alpha1.PhaseDegraded))
		Expect(suite).To(Equal("degraded"))
		Expect(msg).To(ContainSubstring("some workers failed"))
	})

	It("maps all failed to Failed", func() {
		phase, suite, _ := aggregateRunOutcome(false, true)
		Expect(phase).To(Equal(canariov1alpha1.PhaseFailed))
		Expect(suite).To(Equal("failed"))
	})

	It("maps all succeeded to Succeeded", func() {
		phase, suite, _ := aggregateRunOutcome(true, false)
		Expect(phase).To(Equal(canariov1alpha1.PhaseSucceeded))
		Expect(suite).To(Equal("succeeded"))
	})

	It("treats Degraded as terminal", func() {
		Expect(isTerminal(canariov1alpha1.PhaseDegraded)).To(BeTrue())
	})
})
