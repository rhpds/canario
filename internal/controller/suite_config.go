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
	"os"
	"strings"
	"time"

	canariov1alpha1 "github.com/rhpds/canario/api/v1alpha1"
)

const (
	envDefaultSuiteSections     = "CANARIO_DEFAULT_SUITE_SECTIONS"
	envDefaultNetworkMode       = "CANARIO_DEFAULT_NETWORK_MODE"
	envDefaultPingTarget        = "CANARIO_DEFAULT_PING_TARGET"
	envDefaultDNSName           = "CANARIO_DEFAULT_DNS_NAME"
	envDefaultDownloadURL       = "CANARIO_DEFAULT_DOWNLOAD_URL"
	envDefaultGitURL            = "CANARIO_DEFAULT_GIT_URL"
	envWorkerSuiteTimeout       = "CANARIO_WORKER_SUITE_TIMEOUT"

	builtinSuiteSections = "all"
	builtinNetworkMode   = canariov1alpha1.NetworkAuto
	builtinPingTarget    = "1.1.1.1"
	builtinDNSName       = "dns.google"
	builtinDownloadURL   = "http://speedtest.tele2.net/1MB.zip"
	builtinWorkerTimeout = 30 * time.Minute
)

// OperatorSuiteDefaults are install-time suite/network defaults (ConfigMap / env).
type OperatorSuiteDefaults struct {
	Sections           string
	NetworkMode        string
	PingTarget         string
	DNSName            string
	DownloadURL        string
	GitURL             string
	WorkerSuiteTimeout time.Duration // 0 means unset (use builtin when resolving)
}

// SuiteDefaultsFromEnv reads CANARIO_DEFAULT_SUITE_* / CANARIO_DEFAULT_NETWORK_* / URL envs.
func SuiteDefaultsFromEnv() OperatorSuiteDefaults {
	return OperatorSuiteDefaults{
		Sections:           strings.TrimSpace(os.Getenv(envDefaultSuiteSections)),
		NetworkMode:        strings.TrimSpace(os.Getenv(envDefaultNetworkMode)),
		PingTarget:         strings.TrimSpace(os.Getenv(envDefaultPingTarget)),
		DNSName:            strings.TrimSpace(os.Getenv(envDefaultDNSName)),
		DownloadURL:        strings.TrimSpace(os.Getenv(envDefaultDownloadURL)),
		GitURL:             strings.TrimSpace(os.Getenv(envDefaultGitURL)),
		WorkerSuiteTimeout: parseDurationEnv(envWorkerSuiteTimeout),
	}
}

// resolvedSuiteConfig is the final guest env for one Run (Run > operator > builtin).
type resolvedSuiteConfig struct {
	Sections           string
	NetworkMode        string
	PingTarget         string
	DNSName            string
	DownloadURL        string
	GitURL             string
	WorkerSuiteTimeout time.Duration
}

func resolveSuiteConfig(run *canariov1alpha1.CanarioRun, defaults OperatorSuiteDefaults) resolvedSuiteConfig {
	cfg := resolvedSuiteConfig{
		Sections:    firstNonEmpty(specSections(run), defaults.Sections, builtinSuiteSections),
		NetworkMode: firstNonEmpty(specNetworkMode(run), defaults.NetworkMode, builtinNetworkMode),
		PingTarget:  firstNonEmpty(specPingTarget(run), defaults.PingTarget, builtinPingTarget),
		DNSName:     firstNonEmpty(specDNSName(run), defaults.DNSName, builtinDNSName),
		DownloadURL: firstNonEmpty(specDownloadURL(run), defaults.DownloadURL, builtinDownloadURL),
		GitURL:      firstNonEmpty(specGitURL(run), defaults.GitURL, ""),
	}
	switch strings.ToLower(cfg.NetworkMode) {
	case canariov1alpha1.NetworkOn, canariov1alpha1.NetworkOff, canariov1alpha1.NetworkAuto:
		cfg.NetworkMode = strings.ToLower(cfg.NetworkMode)
	default:
		cfg.NetworkMode = builtinNetworkMode
	}
	cfg.WorkerSuiteTimeout = resolveWorkerSuiteTimeout(specSuiteTimeout(run), defaults.WorkerSuiteTimeout)
	return cfg
}

func resolveWorkerSuiteTimeout(specTimeout string, operatorTimeout time.Duration) time.Duration {
	if d, ok := parsePositiveDuration(specTimeout); ok {
		return d
	}
	if operatorTimeout > 0 {
		return operatorTimeout
	}
	return builtinWorkerTimeout
}

// parseDurationEnv returns 0 if unset or invalid (caller falls back to builtin).
func parseDurationEnv(key string) time.Duration {
	d, ok := parsePositiveDuration(os.Getenv(key))
	if !ok {
		return 0
	}
	return d
}

func parsePositiveDuration(raw string) (time.Duration, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

func specSuiteTimeout(run *canariov1alpha1.CanarioRun) string {
	if run.Spec.Suite == nil {
		return ""
	}
	return run.Spec.Suite.Timeout
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func specSections(run *canariov1alpha1.CanarioRun) string {
	if run.Spec.Suite == nil {
		return ""
	}
	return run.Spec.Suite.Sections
}

func specNetworkMode(run *canariov1alpha1.CanarioRun) string {
	if run.Spec.Network == nil {
		return ""
	}
	return run.Spec.Network.Mode
}

func specPingTarget(run *canariov1alpha1.CanarioRun) string {
	if run.Spec.Network == nil {
		return ""
	}
	return run.Spec.Network.PingTarget
}

func specDNSName(run *canariov1alpha1.CanarioRun) string {
	if run.Spec.Network == nil {
		return ""
	}
	return run.Spec.Network.DNSName
}

func specDownloadURL(run *canariov1alpha1.CanarioRun) string {
	if run.Spec.Network == nil {
		return ""
	}
	return run.Spec.Network.DownloadURL
}

func specGitURL(run *canariov1alpha1.CanarioRun) string {
	if run.Spec.Network == nil {
		return ""
	}
	return run.Spec.Network.GitURL
}
