/*
Copyright 2026 Red Hat Demo Platform.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

    10|Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	bwRe   = regexp.MustCompile(`(?i)(?:BW=)?([0-9.]+)\s*([GMKT])i?B/s`)
	iopsRe = regexp.MustCompile(`(?i)(?:IOPS=)?([0-9.]+)([kKmM]?)`)
)

// ParseFloat returns a finite float from a plain numeric string, or false if empty/N/A/invalid.
func ParseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "N/A") || strings.EqualFold(s, "na") {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// ParseBandwidthBytesPerSec parses fio BW strings like "BW=12.5MiB/s" or "512KiB/s".
func ParseBandwidthBytesPerSec(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "N/A") {
		return 0, false
	}
	m := bwRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	mult := 1.0
	switch strings.ToUpper(m[2]) {
	case "K":
		mult = 1024
	case "M":
		mult = 1024 * 1024
	case "G":
		mult = 1024 * 1024 * 1024
	case "T":
		mult = 1024 * 1024 * 1024 * 1024
	}
	return v * mult, true
}

// ParseIOPS parses fio IOPS strings like "IOPS=1.2k", "IOPS=850", or "12.5M".
func ParseIOPS(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "N/A") {
		return 0, false
	}
	m := iopsRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	switch m[2] {
	case "k", "K":
		v *= 1_000
	case "m", "M":
		v *= 1_000_000
	}
	return v, true
}
