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

	"github.com/NVIDIA/go-nvlib/pkg/nvmdev"
	"github.com/google/uuid"

	"github.com/NVIDIA/vgpu-device-manager/internal/nvlib"
	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

// Manager represents a set of functions for managing vGPU configurations on a node
type Manager interface {
	GetVGPUConfig(gpu int) (types.VGPUConfig, error)
	SetVGPUConfig(gpu int, config types.VGPUConfig) error
	ClearVGPUConfig(gpu int) error
}

type nvlibVGPUConfigManager struct {
	nvlib nvlib.Interface
}

var _ Manager = (*nvlibVGPUConfigManager)(nil)

// NewNvlibVGPUConfigManager returns a new vGPU Config Manager which uses go-nvlib when creating / deleting vGPU devices
func NewNvlibVGPUConfigManager() Manager {
	return &nvlibVGPUConfigManager{nvlib.New()}
}

// GetVGPUConfig gets the 'VGPUConfig' currently applied to a GPU at a particular index
func (m *nvlibVGPUConfigManager) GetVGPUConfig(gpu int) (types.VGPUConfig, error) {
	device, err := m.nvlib.Nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return nil, fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	vgpuDevs, err := m.nvlib.Nvmdev.GetAllDevices()
	if err != nil {
		return nil, fmt.Errorf("error getting all vGPU devices: %v", err)
	}
	vgpuConfig := types.VGPUConfig{}
	for _, vgpuDev := range vgpuDevs {
		pf := vgpuDev.GetPhysicalFunction()
		if device.Address == pf.Address {
			vgpuConfig[vgpuDev.MDEVType]++
		}
	}

	return vgpuConfig, nil

}

// SetVGPUConfig applies the selected `VGPUConfig` to a GPU at a particular index if it is not already applied
func (m *nvlibVGPUConfigManager) SetVGPUConfig(gpu int, config types.VGPUConfig) error {
	device, err := m.nvlib.Nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	allParents, err := m.nvlib.Nvmdev.GetAllParentDevices()
	if err != nil {
		return fmt.Errorf("error getting all parent devices: %v", err)
	}

	// Filter for 'parent' devices that are backed by the physical function
	parents := []*nvmdev.ParentDevice{}
	for _, p := range allParents {
		pf := p.GetPhysicalFunction()
		if pf.Address == device.Address {
			parents = append(parents, p)
		}
	}

	if len(parents) == 0 {
		return fmt.Errorf("no parent devices found for GPU at index '%d'", gpu)
	}

	// Before deleting any existing vGPU devices, ensure all vGPU types specified in
	// the config are supported for the GPU we are applying the configuration to.
	for key := range config {
		strippedKey := stripVGPUConfigSuffix(key)
		if !parents[0].IsMDEVTypeSupported(strippedKey) {
			return fmt.Errorf("vGPU type %s is not supported on GPU (index=%d, address=%s)", strippedKey, gpu, device.Address)
		}
	}

	err = m.ClearVGPUConfig(gpu)
	if err != nil {
		return fmt.Errorf("error clearing VGPUConfig: %v", err)
	}

	for key, val := range config {
		strippedKey := stripVGPUConfigSuffix(key)
		remainingToCreate := val
		for _, parent := range parents {
			if remainingToCreate == 0 {
				break
			}

			supported := parent.IsMDEVTypeSupported(strippedKey)
			if !supported {
				return fmt.Errorf("vGPU type %s is not supported on GPU %s", strippedKey, device.Address)
			}

			available, err := parent.GetAvailableMDEVInstances(strippedKey)
			if err != nil {
				return fmt.Errorf("error getting available vGPU instances: %v", err)
			}

			if available <= 0 {
				continue
			}

			numToCreate := min(remainingToCreate, available)
			for i := 0; i < numToCreate; i++ {
				err = parent.CreateMDEVDevice(strippedKey, uuid.New().String())
				if err != nil {
					return fmt.Errorf("unable to create %s vGPU device on parent device %s: %w", strippedKey, parent.Address, err)
				}
			}
			remainingToCreate -= numToCreate
		}

		if remainingToCreate > 0 {
			return fmt.Errorf("failed to create %[1]d %[2]s vGPU devices on the GPU. ensure '%[1]d' does not exceed the maximum supported instances for '%[2]s'", val, strippedKey)
		}
	}
	return nil
}

// ClearVGPUConfig clears the 'VGPUConfig' for a GPU at a particular index by deleting all vGPU devices associated with it
func (m *nvlibVGPUConfigManager) ClearVGPUConfig(gpu int) error {
	device, err := m.nvlib.Nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	vgpuDevs, err := m.nvlib.Nvmdev.GetAllDevices()
	if err != nil {
		return fmt.Errorf("error getting all vGPU devices: %v", err)
	}

	for _, vgpuDev := range vgpuDevs {
		pf := vgpuDev.GetPhysicalFunction()
		if device.Address == pf.Address {
			err = vgpuDev.Delete()
			if err != nil {
				return fmt.Errorf("error deleting %s vGPU device with id %s: %v", vgpuDev.MDEVType, vgpuDev.UUID, err)
			}
		}
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
