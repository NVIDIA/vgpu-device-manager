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

package vgpu

import (
	"fmt"
	"strings"

	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	vgpulib "github.com/NVIDIA/vgpu-device-manager/internal/vgpu"
	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

// Manager represents a set of functions for managing vGPU configurations on a node
type Manager interface {
	GetVGPUConfig(gpu int) (types.VGPUConfig, error)
	SetVGPUConfig(gpu int, config types.VGPUConfig) error
	ClearVGPUConfig(gpu int) error
}

type nvlibVGPUConfigManager struct {
	vgpu  vgpulib.Interface
	nvpci nvpci.Interface
}

var _ Manager = (*nvlibVGPUConfigManager)(nil)

// NewNvlibVGPUConfigManager returns a new vGPU Config Manager which uses go-nvlib when creating / deleting vGPU devices
func NewNvlibVGPUConfigManager() (Manager, error) {
	vgpuInstance, err := vgpulib.New()
	if err != nil {
		return nil, fmt.Errorf("error creating vGPU manager: %v", err)
	}

	return &nvlibVGPUConfigManager{
		vgpu:  vgpuInstance,
		nvpci: nvpci.New(),
	}, nil
}

// GetVGPUConfig gets the 'VGPUConfig' currently applied to a GPU at a particular index
func (m *nvlibVGPUConfigManager) GetVGPUConfig(gpu int) (types.VGPUConfig, error) {
	device, err := m.nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return nil, fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to initialize NVML: %v", nvml.ErrorString(ret))
	}
	defer nvml.Shutdown()

	nvmlDevice, ret := nvml.DeviceGetHandleByPciBusId(device.Address)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get device handle: %v", nvml.ErrorString(ret))
	}

	vgpuInstances, ret := nvmlDevice.GetActiveVgpus()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to get active vGPUs: %v", nvml.ErrorString(ret))
	}

	vgpuConfig := types.VGPUConfig{}
	for _, vgpuInstance := range vgpuInstances {
		vgpuTypeId, ret := vgpuInstance.GetType()
		if ret != nvml.SUCCESS {
			continue
		}
		rawTypeName, ret := vgpuTypeId.GetName()
		if ret != nvml.SUCCESS {
			continue
		}
		typeName := parseVGPUTypeName(rawTypeName)
		vgpuConfig[typeName]++
	}
	return vgpuConfig, nil
}

// SetVGPUConfig applies the selected `VGPUConfig` to a GPU at a particular index if it is not already applied
func (m *nvlibVGPUConfigManager) SetVGPUConfig(gpu int, config types.VGPUConfig) error {
	device, err := m.nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to initialize NVML: %v", nvml.ErrorString(ret))
	}
	defer nvml.Shutdown()

	nvmlDevice, ret := nvml.DeviceGetHandleByPciBusId(device.Address)
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to get device handle: %v", nvml.ErrorString(ret))
	}

	supportedVGPUs, ret := nvmlDevice.GetSupportedVgpus()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to get supported vGPUs: %v", nvml.ErrorString(ret))
	}

	// Before deleting any existing vGPU devices, ensure all vGPU types specified in
	// the config are supported for the GPU we are applying the configuration to.
	//
	// For MIG-backed vGPU types, it may be required to strip the MIG attribute suffix
	// from the vGPU type name before creating the vGPU device. For example, for RTX Pro
	// 6000 Blackwell, all the MIG-backed vGPU types are only supported on MIG instances
	// created with the GFX attribute, but none of the vGPU type names contain the GFX
	// suffix. Taking the DC-1-24QGFX config as an example, the below code would first
	// check if DC-1-24QGFX is a valid vGPU type. Since it is not a valid type, it would
	// strip the GFX suffix and proceed to check if DC-1-24Q is a valid type.
	sanitizedConfig := types.VGPUConfig{}
	for key, val := range config {
		strippedKey := stripVGPUConfigSuffix(key)
		found := false
		for _, vgpuTypeId := range supportedVGPUs {
			rawName, ret := vgpuTypeId.GetName()
			if ret != nvml.SUCCESS {
				continue
			}
			name := parseVGPUTypeName(rawName)
			if name == key {
				found = true
				sanitizedConfig[key] = val
				break
			}
			if name == strippedKey {
				found = true
				sanitizedConfig[strippedKey] = val
				break
			}
		}
		if !found {
			return fmt.Errorf("vGPU type %s is not supported on GPU (index=%d, address=%s)", key, gpu, device.Address)
		}
	}

	err = m.ClearVGPUConfig(gpu)
	if err != nil {
		return fmt.Errorf("error clearing VGPUConfig: %v", err)
	}

	for key, val := range sanitizedConfig {
		creatableVGPUs, ret := nvmlDevice.GetCreatableVgpus()
		if ret != nvml.SUCCESS {
			return fmt.Errorf("failed to get creatable vGPUs: %v", nvml.ErrorString(ret))
		}
		found := false
		for _, vgpuTypeId := range creatableVGPUs {
			rawName, ret := vgpuTypeId.GetName()
			if ret != nvml.SUCCESS {
				continue
			}
			name := parseVGPUTypeName(rawName)
			if name == key {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("vGPU type %s is not creatable on GPU (index=%d, address=%s)", key, gpu, device.Address)
		}
		err = m.vgpu.CreateVGPUDevices(device, key, val)
		if err != nil {
			return fmt.Errorf("error creating vGPU devices: %v", err)
		}
	}
	return nil
}

// ClearVGPUConfig clears the 'VGPUConfig' for a GPU at a particular index by deleting all vGPU devices associated with it
func (m *nvlibVGPUConfigManager) ClearVGPUConfig(gpu int) error {
	device, err := m.nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	vgpuDevs, err := m.vgpu.GetAllDevices()
	if err != nil {
		return fmt.Errorf("error getting all vGPU devices: %v", err)
	}

	for _, vgpuDev := range vgpuDevs {
		pf := vgpuDev.GetPhysicalFunction()
		if device.Address == pf.Address {
			err = vgpuDev.Delete()
			if err != nil {
				return fmt.Errorf("error deleting vGPU device: %v", err)
			}
		}
	}

	return nil
}

// parseVGPUTypeName extracts the vGPU type name from a string that may contain
// product prefixes.
// Examples:
//   - "NVIDIA A100-4C" -> "A100-4C"
//   - "NVIDIA RTX Pro 6000 Blackwell DC-48C" -> "DC-48C"
func parseVGPUTypeName(rawName string) string {
	nameStr := strings.TrimSpace(rawName)
	nameSplit := strings.Split(nameStr, " ")
	typeName := nameSplit[len(nameSplit)-1]
	return typeName
}

// stripVGPUConfigSuffix removes MIG profile attribute suffixes (ME, NOME, MEALL, GFX) from vGPU config type names
func stripVGPUConfigSuffix(configType string) string {
	suffixes := []string{
		types.AttributeMediaExtensionsAll, // MEALL - check first as it contains ME
		types.AttributeNoMediaExtensions,  // NOME
		types.AttributeMediaExtensions,    // ME
		types.AttributeGraphics,           // GFX
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(configType, suffix) {
			return strings.TrimSuffix(configType, suffix)
		}
	}
	return configType
}
