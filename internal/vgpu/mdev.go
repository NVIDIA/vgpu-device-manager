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
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
	"github.com/google/uuid"
)

const (
	mdevParentsRoot = "/sys/class/mdev_bus"
	mdevDevicesRoot = "/sys/bus/mdev/devices"
)

// Nvmdev implements the Interface for MDEV-based vGPUs
type Nvmdev struct {
	mdevParentsRoot string
	mdevDevicesRoot string
	nvpci           nvpci.Interface
}

// MDEVParentDevice represents an NVIDIA parent PCI device for MDEV.
type MDEVParentDevice struct {
	*nvpci.NvidiaPCIDevice
	mdevPaths map[string]string
}

// MDEVDevice represents an NVIDIA MDEV (vGPU) device.
type MDEVDevice struct {
	Path       string
	UUID       string
	MDEVType   string
	Driver     string
	IommuGroup int
	Parent     *MDEVParentDevice
}

// Option defines a function for passing options to the NewNvmdev() call.
type Option func(*Nvmdev)

// WithNvpciLib provides an Option to set the nvpci library.
func WithNvpciLib(nvpciLib nvpci.Interface) Option {
	return func(n *Nvmdev) {
		n.nvpci = nvpciLib
	}
}

// NewNvmdev creates a new MDEV manager that implements Interface.
func NewNvmdev(opts ...Option) *Nvmdev {
	n := &Nvmdev{mdevParentsRoot: mdevParentsRoot, mdevDevicesRoot: mdevDevicesRoot}
	for _, opt := range opts {
		opt(n)
	}
	if n.nvpci == nil {
		n.nvpci = nvpci.New()
	}
	return n
}

// GetAllParentDevices returns all NVIDIA Parent PCI devices on the system.
func (m *Nvmdev) GetAllParentDevices() ([]ParentDevice, error) {
	deviceDirs, err := os.ReadDir(m.mdevParentsRoot)
	if err != nil {
		return nil, fmt.Errorf("unable to read PCI bus devices: %v", err)
	}

	var nvdevices []ParentDevice
	for _, deviceDir := range deviceDirs {
		devicePath := path.Join(m.mdevParentsRoot, deviceDir.Name())
		nvdevice, err := m.NewParentDevice(devicePath)
		if err != nil {
			return nil, fmt.Errorf("error constructing NVIDIA parent device: %v", err)
		}
		if nvdevice == nil {
			continue
		}
		nvdevices = append(nvdevices, nvdevice)
	}

	addressToID := func(address string) uint64 {
		address = strings.ReplaceAll(address, ":", "")
		address = strings.ReplaceAll(address, ".", "")
		id, _ := strconv.ParseUint(address, 16, 64)
		return id
	}

	sort.Slice(nvdevices, func(i, j int) bool {
		return addressToID(nvdevices[i].GetPhysicalFunction().Address) < addressToID(nvdevices[j].GetPhysicalFunction().Address)
	})

	return nvdevices, nil
}

// GetAllDevices returns all NVIDIA mdev (vGPU) devices on the system.
func (m *Nvmdev) GetAllDevices() ([]Device, error) {
	deviceDirs, err := os.ReadDir(m.mdevDevicesRoot)
	if err != nil {
		return nil, fmt.Errorf("unable to read MDEV devices directory: %v", err)
	}

	var nvdevices []Device
	for _, deviceDir := range deviceDirs {
		nvdevice, err := m.NewDevice(m.mdevDevicesRoot, deviceDir.Name())
		if err != nil {
			return nil, fmt.Errorf("error constructing MDEV device: %v", err)
		}
		if nvdevice == nil {
			continue
		}
		nvdevices = append(nvdevices, nvdevice)
	}

	return nvdevices, nil
}

// NewDevice constructs a MDEVDevice, which represents an NVIDIA mdev (vGPU) device.
func (n *Nvmdev) NewDevice(root string, uuid string) (*MDEVDevice, error) {
	path := path.Join(root, uuid)

	m, err := newMdev(path)
	if err != nil {
		return nil, err
	}

	parent, err := n.NewParentDevice(m.parentDevicePath())
	if err != nil {
		return nil, fmt.Errorf("error constructing NVIDIA PCI device: %v", err)
	}

	if parent == nil {
		return nil, nil
	}

	mdevType, err := m.Type()
	if err != nil {
		return nil, fmt.Errorf("error getting mdev type: %v", err)
	}

	driver, err := m.driver()
	if err != nil {
		return nil, fmt.Errorf("error detecting driver: %v", err)
	}

	iommuGroup, err := m.iommuGroup()
	if err != nil {
		return nil, fmt.Errorf("error getting iommu_group: %v", err)
	}

	device := MDEVDevice{
		Path:       path,
		UUID:       uuid,
		MDEVType:   mdevType,
		Driver:     driver,
		IommuGroup: iommuGroup,
		Parent:     parent,
	}

	return &device, nil
}

// mdev represents the path to an NVIDIA mdev (vGPU) device.
type mdev string

func newMdev(devicePath string) (mdev, error) {
	mdevDir, err := filepath.EvalSymlinks(devicePath)
	if err != nil {
		return "", fmt.Errorf("error resolving symlink for %s: %v", devicePath, err)
	}

	return mdev(mdevDir), nil
}

func (m mdev) String() string {
	return string(m)
}

func (m mdev) resolve(target string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path.Join(string(m), target))
	if err != nil {
		return "", fmt.Errorf("error resolving %q: %v", target, err)
	}

	return resolved, nil
}

func (m mdev) parentDevicePath() string {
	// /sys/bus/pci/devices/<addr>/<uuid>
	return path.Dir(string(m))
}

func (m mdev) Type() (string, error) {
	mdevTypeDir, err := m.resolve("mdev_type")
	if err != nil {
		return "", err
	}

	mdevType, err := os.ReadFile(path.Join(mdevTypeDir, "name"))
	if err != nil {
		return "", fmt.Errorf("unable to read mdev_type name for mdev %s: %v", m, err)
	}
	typeName, err := parseMdevTypeName(string(mdevType))
	if err != nil {
		return "", fmt.Errorf("unable to parse mdev_type name for mdev %s: %v", m, err)
	}

	return typeName, nil
}

// parseMdevTypeName extracts the vGPU type name from a string that may contain
// product prefixes.
// Examples:
//   - "NVIDIA A100-4C" -> "A100-4C".
//   - "NVIDIA RTX Pro 6000 Blackwell DC-48C" -> "DC-48C"
func parseMdevTypeName(rawName string) (string, error) {
	nameStr := strings.TrimSpace(rawName)
	nameSplit := strings.Split(nameStr, " ")
	typeName := nameSplit[len(nameSplit)-1]
	if typeName == "" {
		return "", fmt.Errorf("unable to parse mdev_type name from: %s", rawName)
	}
	return typeName, nil
}

func (m mdev) driver() (string, error) {
	driver, err := m.resolve("driver")
	if err != nil {
		return "", err
	}
	return filepath.Base(driver), nil
}

func (m mdev) iommuGroup() (int, error) {
	iommu, err := m.resolve("iommu_group")
	if err != nil {
		return -1, err
	}
	iommuGroupStr := strings.TrimSpace(filepath.Base(iommu))
	iommuGroup, err := strconv.ParseInt(iommuGroupStr, 0, 64)
	if err != nil {
		return -1, fmt.Errorf("unable to convert iommu_group string to int64: %v", iommuGroupStr)
	}

	return int(iommuGroup), nil
}

// NewParentDevice constructs a MDEVParentDevice.
func (m *Nvmdev) NewParentDevice(devicePath string) (*MDEVParentDevice, error) {
	address := filepath.Base(devicePath)
	nvdevice, err := m.nvpci.GetGPUByPciBusID(address)
	if err != nil {
		return nil, fmt.Errorf("failed to construct NVIDIA PCI device: %v", err)
	}
	if nvdevice == nil {
		// not a NVIDIA device.
		return nil, err
	}

	paths, err := filepath.Glob(fmt.Sprintf("%s/mdev_supported_types/nvidia-*/name", nvdevice.Path))
	if err != nil {
		return nil, fmt.Errorf("unable to get files in mdev_supported_types directory: %v", err)
	}
	mdevTypesMap := make(map[string]string)
	for _, path := range paths {
		name, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("unable to read file %s: %v", path, err)
		}
		nameStr, err := parseMdevTypeName(string(name))
		if err != nil {
			return nil, fmt.Errorf("unable to parse mdev_type name at path %s: %v", path, err)
		}

		mdevTypesMap[nameStr] = filepath.Dir(path)
	}

	return &MDEVParentDevice{nvdevice, mdevTypesMap}, err
}

// GetPhysicalFunction gets the physical PCI device backing a 'parent' device.
func (p *MDEVParentDevice) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	if p.SriovInfo.IsVF() {
		return p.SriovInfo.VirtualFunction.PhysicalFunction
	}
	// Either it is an SRIOV physical function or a non-SRIOV device, so return the device itself
	return p.NvidiaPCIDevice
}

// GetPhysicalFunction gets the physical PCI device that a vGPU is created on.
func (d *MDEVDevice) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	return d.Parent.GetPhysicalFunction()
}

// IsVGPUTypeAvailable checks if a vGPU instance of mdevType can be created on the parent GPU.
// Implements the ParentDevice interface.
func (p *MDEVParentDevice) IsVGPUTypeAvailable(mdevType string) (bool, error) {
	availableInstances, err := p.GetAvailableVGPUInstances(mdevType)
	if err != nil {
		return false, fmt.Errorf("failed to get available instances for mdev type %s: %v", mdevType, err)
	}

	return (availableInstances > 0), nil
}

// CreateVGPUDevice creates a mediated device (vGPU) on the parent GPU.
// Implements the ParentDevice interface.
func (p *MDEVParentDevice) CreateVGPUDevice(mdevType string, id string) error {
	mdevPath, ok := p.mdevPaths[mdevType]
	if !ok {
		return fmt.Errorf("unable to create mdev %s: mdev not supported by parent device %s", mdevType, p.Address)
	}
	f, err := os.OpenFile(filepath.Join(mdevPath, "create"), os.O_WRONLY|os.O_SYNC, 0200)
	if err != nil {
		return fmt.Errorf("unable to open create file: %v", err)
	}
	defer f.Close()
	_, err = f.WriteString(id)
	if err != nil {
		return fmt.Errorf("unable to create mdev: %v", err)
	}
	return nil
}

// GetAvailableVGPUInstances returns the available instances for mdevType.
// Returns -1 if mdevType is not supported for the device.
// Implements the ParentDevice interface.
func (p *MDEVParentDevice) GetAvailableVGPUInstances(mdevType string) (int, error) {
	mdevPath, ok := p.mdevPaths[mdevType]
	if !ok {
		return -1, nil
	}

	available, err := os.ReadFile(filepath.Join(mdevPath, "available_instances"))
	if err != nil {
		return -1, fmt.Errorf("unable to read available_instances file: %v", err)
	}

	availableInstances, err := strconv.Atoi(strings.TrimSpace(string(available)))
	if err != nil {
		return -1, fmt.Errorf("unable to convert available_instances to an int: %v", err)
	}

	return availableInstances, nil
}

// Delete deletes a mediated device (vGPU).
// Implements the Device interface.
func (d *MDEVDevice) Delete() error {
	removeFile, err := os.OpenFile(filepath.Join(d.Path, "remove"), os.O_WRONLY|os.O_SYNC, 0200)
	if err != nil {
		return fmt.Errorf("unable to open remove file: %v", err)
	}
	defer removeFile.Close()
	_, err = removeFile.WriteString("1")
	if err != nil {
		return fmt.Errorf("unable to delete mdev: %v", err)
	}

	return nil
}

// IsMDEVTypeSupported checks if the mdevType is supported by the GPU.
func (p *MDEVParentDevice) IsMDEVTypeSupported(mdevType string) bool {
	_, found := p.mdevPaths[mdevType]
	return found
}

// DeleteMDEVDevice deletes a mediated device (vGPU) by ID.
func (p *MDEVParentDevice) DeleteMDEVDevice(id string) error {
	removeFile, err := os.OpenFile(filepath.Join(p.Path, id, "remove"), os.O_WRONLY|os.O_SYNC, 0200)
	if err != nil {
		return fmt.Errorf("unable to open remove file: %v", err)
	}
	defer removeFile.Close()
	_, err = removeFile.WriteString("1")
	if err != nil {
		return fmt.Errorf("unable to delete mdev: %v", err)
	}

	return nil
}

func (m *Nvmdev) CreateVGPUDevices(device *nvpci.NvidiaPCIDevice, vgpuType string, count int) error {
	allParents, err := m.GetAllParentDevices()
	if err != nil {
		return fmt.Errorf("error getting all parent devices: %v", err)
	}

	// Filter for 'parent' devices that are backed by the physical function
	parents := []ParentDevice{}
	for _, p := range allParents {
		pf := p.GetPhysicalFunction()
		if pf.Address == device.Address {
			parents = append(parents, p)
		}
	}
	if len(parents) == 0 {
		return fmt.Errorf("no parent devices found for GPU at address '%s'", device.Address)
	}

	remainingToCreate := count
	for _, parent := range parents {
		if remainingToCreate == 0 {
			break
		}
		available, err := parent.GetAvailableVGPUInstances(vgpuType)
		if err != nil {
			return fmt.Errorf("error getting available vGPU instances: %v", err)
		}
		if available <= 0 {
			continue
		}

		numToCreate := min(remainingToCreate, available)
		for i := 0; i < numToCreate; i++ {
			err = parent.CreateVGPUDevice(vgpuType, uuid.New().String())
			if err != nil {
				return fmt.Errorf("unable to create %s vGPU device on parent device %s: %v", vgpuType, parent.GetPhysicalFunction().Address, err)
			}
		}
		remainingToCreate -= numToCreate
	}
	if remainingToCreate > 0 {
		return fmt.Errorf("failed to create %[1]d %[2]s vGPU devices on the GPU. ensure '%[1]d' does not exceed the maximum supported instances for '%[2]s'", count, vgpuType)
	}
	return nil
}

