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
	"testing"
)

func TestParseFloat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"123.4", 123.4, true},
		{"  0 ", 0, true},
		{"", 0, false},
		{"N/A", 0, false},
		{"nope", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseFloat(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("ParseFloat(%q)=%v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseBandwidthBytesPerSec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"BW=1MiB/s", 1024 * 1024, true},
		{"12.5MiB/s", 12.5 * 1024 * 1024, true},
		{"BW=512KiB/s", 512 * 1024, true},
		{"BW=2GiB/s", 2 * 1024 * 1024 * 1024, true},
		{"N/A", 0, false},
		{"", 0, false},
		{"garbage", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseBandwidthBytesPerSec(tc.in)
		if ok != tc.ok {
			t.Fatalf("ParseBandwidthBytesPerSec(%q) ok=%v want %v", tc.in, ok, tc.ok)
		}
		if ok && math.Abs(got-tc.want) > 0.01 {
			t.Fatalf("ParseBandwidthBytesPerSec(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseIOPS(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"IOPS=850", 850, true},
		{"IOPS=1.2k", 1200, true},
		{"IOPS=1.5M", 1_500_000, true},
		{"12.5k", 12_500, true},
		{"N/A", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseIOPS(tc.in)
		if ok != tc.ok || (ok && math.Abs(got-tc.want) > 0.01) {
			t.Fatalf("ParseIOPS(%q)=%v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
