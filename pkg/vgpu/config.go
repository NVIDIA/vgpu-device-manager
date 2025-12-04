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
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvlib/pkg/nvmdev"
	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
	"github.com/google/uuid"

	"github.com/NVIDIA/vgpu-device-manager/internal/nvlib"
	"github.com/NVIDIA/vgpu-device-manager/internal/vfio"
	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

const (
	HostPCIDevicesRoot = "/host/sys/bus/pci/devices"
)

// ParentDeviceInterface represents a common interface for both VFIO and MDEV parent devices
type ParentDeviceInterface interface {
	GetPhysicalFunction() *nvpci.NvidiaPCIDevice
	IsVGPUTypeAvailable(string) (bool, error)
	CreateVGPUDevice(string, string) error
	GetAvailableVGPUInstances(string) (int, error)
}

// DeviceInterface represents a common interface for both VFIO and MDEV vGPU device instances
type DeviceInterface interface {
	GetPhysicalFunction() *nvpci.NvidiaPCIDevice
	Delete() error
}

type mdevParentAdapter struct {
	*nvmdev.ParentDevice
}

func (a *mdevParentAdapter) IsVGPUTypeAvailable(mdevType string) (bool, error) {
	return a.ParentDevice.IsMDEVTypeAvailable(mdevType)
}

func (a *mdevParentAdapter) CreateVGPUDevice(mdevType string, id string) error {
	return a.ParentDevice.CreateMDEVDevice(mdevType, id)
}

func (a *mdevParentAdapter) GetAvailableVGPUInstances(mdevType string) (int, error) {
	return a.ParentDevice.GetAvailableMDEVInstances(mdevType)
}

// Manager represents a set of functions for managing vGPU configurations on a node
type Manager interface {
	GetVGPUConfig(gpu int) (types.VGPUConfig, error)
	SetVGPUConfig(gpu int, config types.VGPUConfig) error
	ClearVGPUConfig(gpu int) error
}

type nvlibVGPUConfigManager struct {
	nvlib      nvlib.Interface
	vfio       *vfio.VFIOManager
	isVFIOMode bool
}

var _ Manager = (*nvlibVGPUConfigManager)(nil)

// NewNvlibVGPUConfigManager returns a new vGPU Config Manager which uses go-nvlib when creating / deleting vGPU devices
func NewNvlibVGPUConfigManager() (Manager, error) {
	nvlibInstance := nvlib.New()
	vfioManager := vfio.NewVFIOManager(nvlibInstance)

	// Determine mode once at initialization
	isVFIOMode, err := vfioManager.IsVFIOEnabled(0)
	if err != nil {
		return nil, fmt.Errorf("error checking if VFIO is enabled: %v", err)
	}

	return &nvlibVGPUConfigManager{
		nvlib:      nvlibInstance,
		vfio:       vfioManager,
		isVFIOMode: isVFIOMode,
	}, nil
}

// GetVGPUConfig gets the 'VGPUConfig' currently applied to a GPU at a particular index
func (m *nvlibVGPUConfigManager) GetVGPUConfig(gpu int) (types.VGPUConfig, error) {
	if m.isVFIOMode {
		return types.VGPUConfig{}, nil
	}
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

	allParents, err := m.GetAllParentDevices()
	if err != nil {
		return fmt.Errorf("error getting all parent devices: %v", err)
	}

	// Filter for 'parent' devices that are backed by the physical function
	parents := []ParentDeviceInterface{}
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
		if keyAvailable, err := parents[0].IsVGPUTypeAvailable(key); err == nil && keyAvailable {
			sanitizedConfig[key] = val
		} else if strippedKeyAvailable, err := parents[0].IsVGPUTypeAvailable(strippedKey); err == nil && strippedKeyAvailable {
			sanitizedConfig[strippedKey] = val
		} else {
			return fmt.Errorf("vGPU type %s is not supported on GPU (index=%d, address=%s)", key, gpu, device.Address)
		}
	}

	err = m.ClearVGPUConfig(gpu)
	if err != nil {
		return fmt.Errorf("error clearing VGPUConfig: %v", err)
	}

	for key, val := range sanitizedConfig {
		remainingToCreate := val
		for _, parent := range parents {
			if remainingToCreate == 0 {
				break
			}

			supported, err := parent.IsVGPUTypeAvailable(key)
			if err != nil {
				return fmt.Errorf("error checking if vGPU type %s is available: %v", key, err)
			}
			if !supported {
				return fmt.Errorf("vGPU type %s is not supported on GPU %s", key, device.Address)
			}
			available, err := parent.GetAvailableVGPUInstances(key)
			if err != nil {
				return fmt.Errorf("error getting available vGPU instances: %v", err)
			}

			if available <= 0 {
				continue
			}

			numToCreate := min(remainingToCreate, available)
			for i := 0; i < numToCreate; i++ {
				if m.isVFIOMode {
					err = parent.CreateVGPUDevice(key, strconv.Itoa(i))
					if err != nil {
						return fmt.Errorf("unable to create %s vGPU device on parent device %s: %v", key, parent.GetPhysicalFunction().Address, err)
					}
				} else {
					err = parent.CreateVGPUDevice(key, uuid.New().String())
					if err != nil {
						return fmt.Errorf("unable to create %s vGPU device on parent device %s: %v", key, parent.GetPhysicalFunction().Address, err)
					}
				}
			}
			remainingToCreate -= numToCreate
		}

		if remainingToCreate > 0 {
			return fmt.Errorf("failed to create %[1]d %[2]s vGPU devices on the GPU. ensure '%[1]d' does not exceed the maximum supported instances for '%[2]s'", val, key)
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

	vgpuDevs, err := m.GetAllDevices()
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

// IsVFIOMode returns true if the manager is running in VFIO mode, false for MDEV mode
func (m *nvlibVGPUConfigManager) IsVFIOMode() bool {
	return m.isVFIOMode
}

// GetAllParentDevices returns all parent devices as a common interface type
func (m *nvlibVGPUConfigManager) GetAllParentDevices() ([]ParentDeviceInterface, error) {
	if m.isVFIOMode {
		vfioDevices, err := m.vfio.GetAllParentDevices()
		if err != nil {
			return nil, err
		}
		result := make([]ParentDeviceInterface, len(vfioDevices))
		for i, d := range vfioDevices {
			result[i] = d
		}
		return result, nil
	}
	mdevDevices, err := m.nvlib.Nvmdev.GetAllParentDevices()
	if err != nil {
		return nil, err
	}
	result := make([]ParentDeviceInterface, len(mdevDevices))
	for i, d := range mdevDevices {
		result[i] = &mdevParentAdapter{ParentDevice: d}
	}
	return result, nil
}

// GetAllDevices returns all vGPU device instances as a common interface type
func (m *nvlibVGPUConfigManager) GetAllDevices() ([]DeviceInterface, error) {
	if m.isVFIOMode {
		vfioDevices, err := m.vfio.GetAllDevices()
		if err != nil {
			return nil, err
		}
		result := make([]DeviceInterface, len(vfioDevices))
		for i, d := range vfioDevices {
			result[i] = d
		}
		return result, nil
	}
	mdevDevices, err := m.nvlib.Nvmdev.GetAllDevices()
	if err != nil {
		return nil, err
	}
	result := make([]DeviceInterface, len(mdevDevices))
	for i, d := range mdevDevices {
		result[i] = d
	}
	return result, nil
}
