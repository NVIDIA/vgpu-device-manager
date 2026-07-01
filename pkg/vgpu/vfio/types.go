/*
 * Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

package vfio

import (
	"strconv"
	"strings"
)

// vgpuType represents a single vGPU type as reported by the vendor-specific
// VFIO sysfs interface. Each entry pairs the numeric vGPU type ID used by the
// driver with the full type name it reports (e.g. "NVIDIA H200X-1-18C").
type vgpuType struct {
	ID   int
	Name string
}

// shortName returns the last whitespace-separated token of the full type
// name (e.g. "NVIDIA H200X-1-18C" becomes "H200X-1-18C"). This matches the
// convention used for mdev type names, where configuration files reference
// the short name without the product prefix.
func (v vgpuType) shortName() string {
	fields := strings.Fields(v.Name)
	if len(fields) == 0 {
		return v.Name
	}
	return fields[len(fields)-1]
}

// parseCreatableVGPUTypes parses the contents of a 'creatable_vgpu_types'
// file from the vendor-specific VFIO sysfs interface. Each line maps a
// numeric vGPU type ID to a type name:
//
//	1414 : NVIDIA H200X-1-18C
//	1428 : NVIDIA H200X-141C
//
// The parser is intentionally lenient: it accepts both "ID : Name" and
// "ID Name" forms, tolerates extra whitespace, and skips blank lines as well
// as lines that do not start with a numeric type ID (e.g. header lines).
func parseCreatableVGPUTypes(content string) []vgpuType {
	var vgpuTypes []vgpuType
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		idStr, name, found := strings.Cut(line, ":")
		if !found {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			idStr = fields[0]
			name = strings.Join(fields[1:], " ")
		}

		id, err := strconv.Atoi(strings.TrimSpace(idStr))
		if err != nil {
			continue
		}

		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		vgpuTypes = append(vgpuTypes, vgpuType{ID: id, Name: name})
	}
	return vgpuTypes
}
