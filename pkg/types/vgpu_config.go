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

import (
	"fmt"
)

// VGPUConfig holds a map of strings representing a vGPU type to a
// count of that type. It is meant to represent the set of vGPU types
// (and how many of a particular type) should be instantiated on the GPU.
type VGPUConfig map[string]int

// AssertValid checks if all the vGPU types making up a 'VGPUConfig' are valid
func (v VGPUConfig) AssertValid() error {
	if len(v) == 0 {
		return nil
	}

	idx := 0
	migBacked := false
	for key, val := range v {
		vgpuType, err := ParseVGPUType(key)
		if err != nil {
			return fmt.Errorf("invalid format for '%v': %v", key, err)
		}
		if val <= 0 {
			return fmt.Errorf("invalid count for '%v': %v", val, err)
		}
		if vgpuType.G > 0 {
			if idx > 0 && !migBacked {
				return fmt.Errorf("cannot mix time-sliced and MIG-backed vGPU devices on the same GPU")
			}
			migBacked = true
		} else {
			if idx > 0 && migBacked {
				return fmt.Errorf("cannot mix time-sliced and MIG-backed vGPU devices on the same GPU")
			}
		}
		idx++
	}

	for _, val := range v {
		if val > 0 {
			return nil
		}
	}

	return fmt.Errorf("all counts for all vGPU types are 0")
}

// Contains checks if the provided 'vgpuType' is part of the 'VGPUConfig'.
func (v VGPUConfig) Contains(vgpuType string) bool {
	if _, exists := v[vgpuType]; !exists {
		return false
	}
	return v[vgpuType] > 0
}

// Equals checks if two 'VGPUConfig's are equal.
// Equality is determined by comparing the vGPU types contained in each 'VGPUConfig'.
func (v VGPUConfig) Equals(config VGPUConfig) bool {
	if len(v) != len(config) {
		return false
	}
	for k, v := range v {
		if !config.Contains(k) {
			return false
		}
		if v != config[k] {
			return false
		}
	}
	return true
}
