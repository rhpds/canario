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

// Package suitepayload holds the in-guest Canario suite script embedded in the operator.
// Sections are modular (CANARIO_SUITE_SECTIONS); expand without changing the product API.
package suitepayload

import _ "embed"

// SuiteSH is the default in-guest suite (storage/fileops today; more sections later).
//
//go:embed suite.sh
var SuiteSH string
