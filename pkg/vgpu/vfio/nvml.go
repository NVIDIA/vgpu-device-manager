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
	"fmt"
	"reflect"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// nvmlTypeResolver resolves the vGPU types supported by a GPU using NVML.
// Unlike the per-VF 'creatable_vgpu_types' sysfs files, the set of supported
// vGPU types reported by NVML does not depend on the vGPU devices currently
// allocated on the GPU.
type nvmlTypeResolver struct {
	nvmllib nvml.Interface
}

var _ TypeResolver = (*nvmlTypeResolver)(nil)

// newNVMLTypeResolver returns a TypeResolver backed by NVML.
func newNVMLTypeResolver() TypeResolver {
	return &nvmlTypeResolver{nvmllib: nvml.New()}
}

// SupportedVGPUTypes returns a map of the numeric IDs of all vGPU types
// supported by the GPU at the provided PCI address to their type names.
func (r *nvmlTypeResolver) SupportedVGPUTypes(pfAddress string) (map[int]string, error) {
	ret := r.nvmllib.Init()
	if ret != nvml.SUCCESS && ret != nvml.ERROR_ALREADY_INITIALIZED {
		return nil, fmt.Errorf("error initializing NVML: %v", ret)
	}
	defer func() {
		_ = r.nvmllib.Shutdown()
	}()

	dev, ret := r.nvmllib.DeviceGetHandleByPciBusId(pfAddress)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting device handle for PCI address %s: %v", pfAddress, ret)
	}

	typeIDs, ret := dev.GetSupportedVgpus()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting supported vGPU types: %v", ret)
	}

	supported := make(map[int]string, len(typeIDs))
	for _, typeID := range typeIDs {
		id, err := numericVGPUTypeID(typeID)
		if err != nil {
			return nil, err
		}
		name, ret := typeID.GetName()
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("error getting name of vGPU type %d: %v", id, ret)
		}
		supported[id] = name
	}
	return supported, nil
}

// numericVGPUTypeID extracts the numeric value of an nvml.VgpuTypeId. The
// VgpuTypeId interface does not expose the underlying numeric type ID, but
// its concrete type is defined as an unsigned integer, so the value is
// recovered via reflection.
func numericVGPUTypeID(typeID nvml.VgpuTypeId) (int, error) {
	if v := reflect.ValueOf(typeID); v.CanUint() {
		// nolint:gosec // vGPU type IDs are small positive numbers well within the int range.
		return int(v.Uint()), nil
	}
	return 0, fmt.Errorf("unable to determine the numeric vGPU type ID of %T", typeID)
}
