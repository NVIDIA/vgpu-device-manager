/*
 * Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package types

// Series represents the 'series' a vGPU type belongs to.
// vGPU types are grouped into series according to the
// different classes of workload for which they are
// optimized. Each series is identified by the last letter
// of the vGPU type name (i.e. A100-5C is a 'C-Series' vGPU type).
type Series byte

const (
	// Q series vGPU
	Q Series = iota + 81
	// A series vGPU
	A = iota + 64
	// B series vGPU
	B
	// C series vGPU
	C
)

// IsValid checks whether an ASCII character represents a valid series.
func (s Series) IsValid() bool {
	switch s {
	case A, B, C, Q:
		return true
	}
	return false
}
