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
	"strconv"
	"strings"
)

const (
	envNodeUsageCeiling    = "CANARIO_NODE_USAGE_CEILING"
	envStorageUsageCeiling = "CANARIO_STORAGE_USAGE_CEILING"

	defaultNodeUsageCeiling    = 0.85
	defaultStorageUsageCeiling = 0.75 // align with Ceph nearfull-style caution
)

// OperatorThresholds are install-time placement limits (env / ConfigMap).
// A worker is eligible only if CPU, memory, and storage are all under ceiling.
type OperatorThresholds struct {
	// NodeUsageCeiling max CPU/mem request usage of allocatable after one CioVM.
	NodeUsageCeiling float64

	// StorageUsageCeiling max storage usage: Ceph used/total for ceph SCs, else
	// node ephemeral-storage requests/allocatable (local / non-ceph).
	StorageUsageCeiling float64
}

// ThresholdsFromEnv reads CANARIO_*_USAGE_CEILING (0.85, 85, or 85%).
func ThresholdsFromEnv() OperatorThresholds {
	return OperatorThresholds{
		NodeUsageCeiling:    parseFractionEnv(envNodeUsageCeiling, defaultNodeUsageCeiling),
		StorageUsageCeiling: parseFractionEnv(envStorageUsageCeiling, defaultStorageUsageCeiling),
	}
}

func parseFractionEnv(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	f, err := parseFraction(raw)
	if err != nil {
		return def
	}
	return f
}

// parseFraction accepts 0.85, .85, 85, or 85%.
func parseFraction(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "%")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if f > 1 {
		f = f / 100
	}
	if f <= 0 || f > 1 {
		return 0, strconv.ErrRange
	}
	return f, nil
}
