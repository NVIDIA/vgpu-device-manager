/*
 * Copyright (c) NVIDIA CORPORATION.  All rights reserved.
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

// nvmdev implements Interface for sysfs mediated-device (mdev) vGPUs.
type nvmdev struct {
	mdevParentsRoot string
	mdevDevicesRoot string
	nvpci           nvpci.Interface
}

// mdevParentDevice is the MDEV implementation of ParentDevice for an NVIDIA GPU with mdev_supported_types.
type mdevParentDevice struct {
	*nvpci.NvidiaPCIDevice
	mdevPaths map[string]string
}

// mdevDevice is the MDEV implementation of Device for a /sys/bus/mdev/devices entry.
type mdevDevice struct {
	Path       string
	UUID       string
	MDEVType   string
	Driver     string
	IommuGroup int
	Parent     *mdevParentDevice
}

// Option configures newNvmdev.
type Option func(*nvmdev)

// WithNvpciLib returns an Option that sets the nvpci.Interface on *nvmdev.
func WithNvpciLib(nvpciLib nvpci.Interface) Option {
	return func(n *nvmdev) {
		n.nvpci = nvpciLib
	}
}

// newNvmdev constructs *nvmdev with default sysfs roots; nvpci defaults to nvpci.New().
func newNvmdev(opts ...Option) *nvmdev {
	n := &nvmdev{mdevParentsRoot: mdevParentsRoot, mdevDevicesRoot: mdevDevicesRoot}
	for _, opt := range opts {
		opt(n)
	}
	if n.nvpci == nil {
		n.nvpci = nvpci.New()
	}
	return n
}

// GetAllParentDevices lists mdev_bus parents as *mdevParentDevice (implements Interface).
func (m *nvmdev) GetAllParentDevices() ([]ParentDevice, error) {
	deviceDirs, err := os.ReadDir(m.mdevParentsRoot)
	if err != nil {
		return nil, fmt.Errorf("unable to read PCI bus devices: %w", err)
	}

	var nvdevices []ParentDevice
	for _, deviceDir := range deviceDirs {
		devicePath := path.Join(m.mdevParentsRoot, deviceDir.Name())
		nvdevice, err := m.newParentDevice(devicePath)
		if err != nil {
			return nil, fmt.Errorf("error constructing NVIDIA parent device: %w", err)
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

// GetAllDevices lists /sys/bus/mdev/devices entries as *mdevDevice (implements Interface).
func (m *nvmdev) GetAllDevices() ([]Device, error) {
	deviceDirs, err := os.ReadDir(m.mdevDevicesRoot)
	if err != nil {
		return nil, fmt.Errorf("unable to read MDEV devices directory: %w", err)
	}

	var nvdevices []Device
	for _, deviceDir := range deviceDirs {
		nvdevice, err := m.newDevice(m.mdevDevicesRoot, deviceDir.Name())
		if err != nil {
			return nil, fmt.Errorf("error constructing MDEV device: %w", err)
		}
		if nvdevice == nil {
			continue
		}
		nvdevices = append(nvdevices, nvdevice)
	}

	return nvdevices, nil
}

// newDevice builds an *mdevDevice for one mdev sysfs path under root/uuid.
func (n *nvmdev) newDevice(root string, uuid string) (*mdevDevice, error) {
	path := path.Join(root, uuid)

	m, err := newMdev(path)
	if err != nil {
		return nil, err
	}

	parent, err := n.newParentDevice(m.parentDevicePath())
	if err != nil {
		return nil, fmt.Errorf("error constructing NVIDIA PCI device: %w", err)
	}

	if parent == nil {
		return nil, nil
	}

	mdevType, err := m.Type()
	if err != nil {
		return nil, fmt.Errorf("error getting mdev type: %w", err)
	}

	driver, err := m.driver()
	if err != nil {
		return nil, fmt.Errorf("error detecting driver: %w", err)
	}

	iommuGroup, err := m.iommuGroup()
	if err != nil {
		return nil, fmt.Errorf("error getting iommu_group: %w", err)
	}

	device := mdevDevice{
		Path:       path,
		UUID:       uuid,
		MDEVType:   mdevType,
		Driver:     driver,
		IommuGroup: iommuGroup,
		Parent:     parent,
	}

	return &device, nil
}

// mdev is the resolved sysfs path to one mdev device directory.
type mdev string

// newMdev returns mdev after EvalSymlinks on devicePath.
func newMdev(devicePath string) (mdev, error) {
	mdevDir, err := filepath.EvalSymlinks(devicePath)
	if err != nil {
		return "", fmt.Errorf("error resolving symlink for %s: %w", devicePath, err)
	}

	return mdev(mdevDir), nil
}

func (m mdev) string() string {
	return string(m)
}

func (m mdev) resolve(target string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path.Join(string(m), target))
	if err != nil {
		return "", fmt.Errorf("error resolving %q: %w", target, err)
	}

	return resolved, nil
}

func (m mdev) parentDevicePath() string {
	// /sys/bus/pci/devices/<addr>/<uuid>
	return path.Dir(string(m))
}

// Type reads the mdev_type name symlink target for this mdev path.
func (m mdev) Type() (string, error) {
	mdevTypeDir, err := m.resolve("mdev_type")
	if err != nil {
		return "", err
	}

	mdevType, err := os.ReadFile(path.Join(mdevTypeDir, "name"))
	if err != nil {
		return "", fmt.Errorf("unable to read mdev_type name for mdev %s: %w", m, err)
	}
	typeName, err := parseMdevTypeName(string(mdevType))
	if err != nil {
		return "", fmt.Errorf("unable to parse mdev_type name for mdev %s: %w", m, err)
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
		return "", fmt.Errorf("unable to parse mdev_type name from: %w", rawName)
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
		return -1, fmt.Errorf("unable to convert iommu_group string to int64: %w", iommuGroupStr)
	}

	return int(iommuGroup), nil
}

// newParentDevice builds *mdevParentDevice when devicePath is an NVIDIA GPU under mdev_bus.
func (m *nvmdev) newParentDevice(devicePath string) (*mdevParentDevice, error) {
	address := filepath.Base(devicePath)
	nvdevice, err := m.nvpci.GetGPUByPciBusID(address)
	if err != nil {
		return nil, fmt.Errorf("failed to construct NVIDIA PCI device: %w", err)
	}
	if nvdevice == nil {
		// not a NVIDIA device.
		return nil, err
	}

	paths, err := filepath.Glob(fmt.Sprintf("%s/mdev_supported_types/nvidia-*/name", nvdevice.Path))
	if err != nil {
		return nil, fmt.Errorf("unable to get files in mdev_supported_types directory: %w", err)
	}
	mdevTypesMap := make(map[string]string)
	for _, path := range paths {
		name, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("unable to read file %s: %w", path, err)
		}
		nameStr, err := parseMdevTypeName(string(name))
		if err != nil {
			return nil, fmt.Errorf("unable to parse mdev_type name at path %s: %w", path, err)
		}

		mdevTypesMap[nameStr] = filepath.Dir(path)
	}

	return &mdevParentDevice{nvdevice, mdevTypesMap}, err
}

// GetPhysicalFunction returns the physical function for this mdevParentDevice (implements ParentDevice).
func (p *mdevParentDevice) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	if p.SriovInfo.IsVF() {
		return p.SriovInfo.VirtualFunction.PhysicalFunction
	}
	// Either it is an SRIOV physical function or a non-SRIOV device, so return the device itself
	return p.NvidiaPCIDevice
}

// GetPhysicalFunction delegates to Parent *mdevParentDevice (implements Device).
func (d *mdevDevice) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	return d.Parent.GetPhysicalFunction()
}

// IsVGPUTypeAvailable reports whether GetAvailableVGPUInstances(mdevType) > 0 (implements ParentDevice).
func (p *mdevParentDevice) IsVGPUTypeAvailable(mdevType string) (bool, error) {
	availableInstances, err := p.GetAvailableVGPUInstances(mdevType)
	if err != nil {
		return false, fmt.Errorf("failed to get available instances for mdev type %s: %w", mdevType, err)
	}

	return (availableInstances > 0), nil
}

// CreateVGPUDevice writes id to the mdev type's create file (implements ParentDevice).
func (p *mdevParentDevice) CreateVGPUDevice(mdevType string, id string) error {
	mdevPath, ok := p.mdevPaths[mdevType]
	if !ok {
		return fmt.Errorf("unable to create mdev %s: mdev not supported by parent device %s", mdevType, p.Address)
	}
	f, err := os.OpenFile(filepath.Join(mdevPath, "create"), os.O_WRONLY|os.O_SYNC, 0200)
	if err != nil {
		return fmt.Errorf("unable to open create file: %w", err)
	}
	defer f.Close()
	_, err = f.WriteString(id)
	if err != nil {
		return fmt.Errorf("unable to create mdev: %w", err)
	}
	return nil
}

// GetAvailableVGPUInstances reads available_instances for mdevType, or -1 if unsupported (implements ParentDevice).
func (p *mdevParentDevice) GetAvailableVGPUInstances(mdevType string) (int, error) {
	mdevPath, ok := p.mdevPaths[mdevType]
	if !ok {
		return -1, nil
	}

	available, err := os.ReadFile(filepath.Join(mdevPath, "available_instances"))
	if err != nil {
		return -1, fmt.Errorf("unable to read available_instances file: %w", err)
	}

	availableInstances, err := strconv.Atoi(strings.TrimSpace(string(available)))
	if err != nil {
		return -1, fmt.Errorf("unable to convert available_instances to an int: %w", err)
	}

	return availableInstances, nil
}

// Delete writes to this mdev instance's remove file (implements Device).
func (d *mdevDevice) Delete() error {
	removeFile, err := os.OpenFile(filepath.Join(d.Path, "remove"), os.O_WRONLY|os.O_SYNC, 0200)
	if err != nil {
		return fmt.Errorf("unable to open remove file: %w", err)
	}
	defer removeFile.Close()
	_, err = removeFile.WriteString("1")
	if err != nil {
		return fmt.Errorf("unable to delete mdev: %w", err)
	}

	return nil
}

// isMDEVTypeSupported reports whether mdevType is listed in mdevPaths.
func (p *mdevParentDevice) isMDEVTypeSupported(mdevType string) bool {
	_, found := p.mdevPaths[mdevType]
	return found
}

// deleteMDEVDevice removes the mdev child id under this parent's PCI sysfs path.
func (p *mdevParentDevice) deleteMDEVDevice(id string) error {
	removeFile, err := os.OpenFile(filepath.Join(p.Path, id, "remove"), os.O_WRONLY|os.O_SYNC, 0200)
	if err != nil {
		return fmt.Errorf("unable to open remove file: %w", err)
	}
	defer removeFile.Close()
	_, err = removeFile.WriteString("1")
	if err != nil {
		return fmt.Errorf("unable to delete mdev: %w", err)
	}

	return nil
}

// CreateVGPUDevices creates up to count mdevs of vgpuType on parents matching device (implements Interface).
func (m *nvmdev) CreateVGPUDevices(device *nvpci.NvidiaPCIDevice, vgpuType string, count int) error {
	allParents, err := m.GetAllParentDevices()
	if err != nil {
		return fmt.Errorf("error getting all parent devices: %w", err)
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
			return fmt.Errorf("error getting available vGPU instances: %w", err)
		}
		if available <= 0 {
			continue
		}

		numToCreate := min(remainingToCreate, available)
		for i := 0; i < numToCreate; i++ {
			err = parent.CreateVGPUDevice(vgpuType, uuid.New().String())
			if err != nil {
				return fmt.Errorf("unable to create %s vGPU device on parent device %s: %w", vgpuType, parent.GetPhysicalFunction().Address, err)
			}
		}
		remainingToCreate -= numToCreate
	}
	if remainingToCreate > 0 {
		return fmt.Errorf("failed to create %[1]d %[2]s vGPU devices on the GPU. ensure '%[1]d' does not exceed the maximum supported instances for '%[2]s'", count, vgpuType)
	}
	return nil
}
