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
	"os"
	"strconv"
	"bufio"
	"os/exec"

	"github.com/NVIDIA/go-nvlib/pkg/nvmdev"
	"github.com/google/uuid"

	"github.com/NVIDIA/vgpu-device-manager/internal/nvlib"
	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

const (
	PCIDevicesRoot = "/sys/bus/pci/devices"
)

// Manager represents a set of functions for managing vGPU configurations on a node
type Manager interface {
	GetVGPUConfig(gpu int) (types.VGPUConfig, error)
	SetVGPUConfig(gpu int, config types.VGPUConfig) error
	ClearVGPUConfig(gpu int) error
	IsUbuntu2404() (bool, error)
	GetVGPUConfigforVFIO(gpu int) (types.VGPUConfig, error)
	SetVGPUConfigforVFIO(gpu int, config types.VGPUConfig) error
}

type nvlibVGPUConfigManager struct {
	nvlib nvlib.Interface
}

var _ Manager = (*nvlibVGPUConfigManager)(nil)

// NewNvlibVGPUConfigManager returns a new vGPU Config Manager which uses go-nvlib when creating / deleting vGPU devices
func NewNvlibVGPUConfigManager() Manager {
	return &nvlibVGPUConfigManager{nvlib.New()}
}

func (m *nvlibVGPUConfigManager) GetAllNvidiaGPUDevices() ([]*nvpci.NvidiaPCIDevice, error) {
	var nvdevices []*nvpci.NvidiaPCIDevice
	deviceDirs, err := os.ReadDir(PCIDevicesRoot)
	if err != nil {
		return nil, fmt.Errorf("unable to read parent PCI bus devices: %v", err)
	}
	for _, deviceDir := range deviceDirs {
		deviceAddress := deviceDir.Name()
		nvdevice, err := m.nvlib.Nvpci.GetGPUByPciBusID(deviceAddress)
		if err != nil || nvdevice == nil {
			continue
		}
		if nvdevice.IsGPU() {
			nvdevices = append(nvdevices, nvdevice)
		}
	}
	return nvdevices, nil
}

func (m *nvlibVGPUConfigManager) GetVGPUConfigforVFIO(gpu int) (types.VGPUConfig, error) {
	nvdevice, err := m.nvlib.Nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return nil, fmt.Errorf("unable to get GPU by index %d: %v", gpu, err)
	}
	GPUDevices, err := m.nvlib.Nvpci.GetGPUs()
	if err != nil {
		return nil, fmt.Errorf("unable to get all NVIDIA GPU devices: %v", err)
	}

	vgpuConfig := types.VGPUConfig{}
	for _, device := range GPUDevices {
		if device.Address == nvdevice.Address {
			VFnum := 0
			totalVF := int(nvdevice.SriovInfo.PhysicalFunction.TotalVFs)
			for VFnum < totalVF {
				VFAddr := PCIDevicesRoot + "/" + device.Address + "/virtfn" + strconv.Itoa(VFnum) + "/nvidia"
				if _, err := os.Stat(VFAddr); err == nil {
					VGPUTypeNumberBytes, err := os.ReadFile(VFAddr + "/current_vgpu_type")
					if err != nil {
						return nil, fmt.Errorf("unable to read current vGPU type: %v", err)
					}
					VGPUTypeNumber, err := strconv.Atoi(string(VGPUTypeNumberBytes))
					if err != nil {
						return nil, fmt.Errorf("unable to convert current vGPU type to int: %v", err)
					}
					VGPUTypeName, err := m.getVGPUTypeNameforVFIO(VFAddr + "/creatable_vgpu_types", VGPUTypeNumber)
					if err != nil {
						return nil, fmt.Errorf("unable to get vGPU type name: %v", err)
					}
					vgpuConfig[VGPUTypeName]++
				}
				VFnum++
			}
		}
	}
	
	return vgpuConfig, nil
}

//// Set the vGPU config for each GPU if it is in nvdevices 
func (m *nvlibVGPUConfigManager) SetVGPUConfigforVFIO(gpu int, config types.VGPUConfig) error {
	nvdevice, err := m.nvlib.Nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return fmt.Errorf("unable to get GPU by index %d: %v", gpu, err)
	}
	
	GPUDevices, err := m.nvlib.Nvpci.GetGPUs()
	if err != nil {
		return fmt.Errorf("unable to get all NVIDIA GPU devices: %v", err)
	}
	
	deviceFound := false
	for _, device := range GPUDevices {
		if device.Address == nvdevice.Address {
			deviceFound = true
			break
		}
	}
	if !deviceFound {
		return fmt.Errorf("GPU at index %d not found in available NVIDIA devices", gpu)
	}

	err = m.ClearVGPUConfig(gpu)
	if err != nil {
		return fmt.Errorf("error clearing VGPUConfig: %v", err)
	}

	cmd := exec.Command("chroot", "/host", "/run/nvidia/driver/usr/lib/nvidia/sriov-manage", "-e", nvdevice.Address)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unable to execute sriov-manage: %v, output: %s", err, string(output))
	}

	for key, val := range config {
		remainingToCreate := val
		VFnum := 0
		for remainingToCreate > 0 {
			VFAddr := PCIDevicesRoot + "/" + nvdevice.Address + "/virtfn" + strconv.Itoa(VFnum) + "/nvidia"
			number, err := m.getVGPUTypeNumberforVFIO(VFAddr + "/creatable_vgpu_types", key)
			if err != nil {
				return fmt.Errorf("unable to get vGPU type number: %v", err)
			}
			err = os.WriteFile(VFAddr + "/current_vgpu_type", []byte(strconv.Itoa(number)), 0644)
			if err != nil {
				return fmt.Errorf("unable to write current vGPU type: %v", err)
			}
			VFnum++
			remainingToCreate--
		}
	}
	return nil
}

func (m *nvlibVGPUConfigManager) getVGPUTypeNameforVFIO(filePath string, vgpuTypeNumber int) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("unable to open file %s: %v", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[len(fields)-1]
		numInt, err := strconv.Atoi(fields[0])
		if err == nil && numInt == vgpuTypeNumber {
			return name, nil
		}
	}
	return "", fmt.Errorf("vGPU type %d not found in file %s", vgpuTypeNumber, filePath)
}

func (m *nvlibVGPUConfigManager) getVGPUTypeNumberforVFIO(filePath string, vgpuTypeName string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("unable to open file %s: %v", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[len(fields)-1]
		numInt, err := strconv.Atoi(fields[0])
		if err == nil && name == vgpuTypeName {
			return numInt, nil
		}
	}
	return 0, fmt.Errorf("vGPU type %s not found in file %s", vgpuTypeName, filePath)
}

func (m *nvlibVGPUConfigManager) IsUbuntu2404() (bool, error) {
    // Read from the host's /etc/os-release (mounted at /host in the container)
    data, err := os.ReadFile("/host/etc/os-release")
    if err != nil {
        return false, fmt.Errorf("unable to read host OS release info: %v", err)
    }
    
    content := string(data)
    isUbuntu := strings.Contains(content, "ID=ubuntu")
    is2404 := strings.Contains(content, `VERSION_ID="24.04"`)
    
    return isUbuntu && is2404, nil
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
		//nolint:gocritic // using if-else for clarity instead of switch
		if parents[0].IsMDEVTypeSupported(key) {
			sanitizedConfig[key] = val
		} else if parents[0].IsMDEVTypeSupported(strippedKey) {
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

			supported := parent.IsMDEVTypeSupported(key)
			if !supported {
				return fmt.Errorf("vGPU type %s is not supported on GPU %s", key, device.Address)
			}

			available, err := parent.GetAvailableMDEVInstances(key)
			if err != nil {
				return fmt.Errorf("error getting available vGPU instances: %v", err)
			}

			if available <= 0 {
				continue
			}

			numToCreate := min(remainingToCreate, available)
			for i := 0; i < numToCreate; i++ {
				err = parent.CreateMDEVDevice(key, uuid.New().String())
				if err != nil {
					return fmt.Errorf("unable to create %s vGPU device on parent device %s: %w", key, parent.Address, err)
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
