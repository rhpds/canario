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

var _ = Describe("suite config resolution", func() {
	It("uses built-in defaults when Run and operator are empty", func() {
		cfg := resolveSuiteConfig(&canariov1alpha1.CanarioRun{}, OperatorSuiteDefaults{})
		Expect(cfg.Sections).To(Equal(builtinSuiteSections))
		Expect(cfg.NetworkMode).To(Equal(builtinNetworkMode))
		Expect(cfg.DownloadURL).To(Equal(builtinDownloadURL))
		Expect(cfg.GitURL).To(Equal(""))
	})

	It("prefers Run.spec over operator defaults", func() {
		run := &canariov1alpha1.CanarioRun{
			Spec: canariov1alpha1.CanarioRunSpec{
				Suite: &canariov1alpha1.SuiteSpec{Sections: "storage,network"},
				Network: &canariov1alpha1.NetworkSpec{
					Mode:        canariov1alpha1.NetworkOn,
					PingTarget:  "8.8.8.8",
					DownloadURL: "http://example.com/1mb.bin",
					GitURL:      "https://example.com/repo.git",
				},
			},
		}
		defaults := OperatorSuiteDefaults{
			Sections:    "all",
			NetworkMode: canariov1alpha1.NetworkAuto,
			PingTarget:  "1.1.1.1",
			DownloadURL: "http://speedtest.tele2.net/1MB.zip",
		}
		cfg := resolveSuiteConfig(run, defaults)
		Expect(cfg.Sections).To(Equal("storage,network"))
		Expect(cfg.NetworkMode).To(Equal(canariov1alpha1.NetworkOn))
		Expect(cfg.PingTarget).To(Equal("8.8.8.8"))
		Expect(cfg.DownloadURL).To(Equal("http://example.com/1mb.bin"))
		Expect(cfg.GitURL).To(Equal("https://example.com/repo.git"))
	})

	It("uses operator defaults when Run fields are empty", func() {
		run := &canariov1alpha1.CanarioRun{
			Spec: canariov1alpha1.CanarioRunSpec{
				Network: &canariov1alpha1.NetworkSpec{},
			},
		}
		defaults := OperatorSuiteDefaults{
			Sections:    "storage,fileops",
			NetworkMode: canariov1alpha1.NetworkOff,
			DNSName:     "example.org",
		}
		cfg := resolveSuiteConfig(run, defaults)
		Expect(cfg.Sections).To(Equal("storage,fileops"))
		Expect(cfg.NetworkMode).To(Equal(canariov1alpha1.NetworkOff))
		Expect(cfg.DNSName).To(Equal("example.org"))
		Expect(cfg.PingTarget).To(Equal(builtinPingTarget))
	})
})
