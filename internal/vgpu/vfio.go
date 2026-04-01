package vgpu

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
)

type nvvfio struct {
	nvpci nvpci.Interface
}

// vfioParentDevice is the VFIO implementation of ParentDevice for an NVIDIA SR-IOV VF sysfs path.
type vfioParentDevice struct {
	*nvpci.NvidiaPCIDevice
	VirtualFunctionPath string
}

// vfioDevice is the VFIO implementation of Device for an active vGPU on a VF path.
type vfioDevice struct {
	Path   string
	Parent *vfioParentDevice
}

// newNvvfio constructs an nvvfio manager using nvpci.New().
func newNvvfio() *nvvfio {
	return &nvvfio{
		nvpci: nvpci.New(),
	}
}

// GetAllParentDevices lists sysfs VF nvidia paths as vfioParentDevice values (implements Interface).
func (m *nvvfio) GetAllParentDevices() ([]ParentDevice, error) {
	nvdevices, err := m.nvpci.GetGPUs()
	if err != nil {
		return nil, fmt.Errorf("unable to get all NVIDIA GPU devices: %w", err)
	}
	parentDevices := []ParentDevice{}
	for _, device := range nvdevices {
		vfnum := 0
		numVF := int(device.SriovInfo.PhysicalFunction.NumVFs)
		for vfnum < numVF {
			vfAddr := filepath.Join(device.Path, "virtfn"+strconv.Itoa(vfnum), "nvidia")
			if _, err := os.Stat(vfAddr); err != nil {
				return nil, fmt.Errorf("virtual function %d at address %s does not exist", vfnum, vfAddr)
			}
			parentDevices = append(parentDevices, &vfioParentDevice{
				NvidiaPCIDevice:     device,
				VirtualFunctionPath: vfAddr,
			})
			vfnum++
		}
	}
	return parentDevices, nil
}

// GetAllDevices returns vfioDevice entries for VFs with non-zero current_vgpu_type (implements Interface).
func (m *nvvfio) GetAllDevices() ([]Device, error) {
	parentDevices, err := m.GetAllParentDevices()
	if err != nil {
		return nil, fmt.Errorf("unable to get all parent devices: %w", err)
	}
	devices := []Device{}
	for _, p := range parentDevices {
		parentDevice := p.(*vfioParentDevice) // type assert to access VirtualFunctionPath
		vgpuTypeNumberBytes, err := os.ReadFile(filepath.Join(parentDevice.VirtualFunctionPath, "current_vgpu_type"))
		if err != nil {
			return nil, fmt.Errorf("unable to read current vGPU type: %w", err)
		}
		vgpuTypeNumber, err := strconv.Atoi(strings.TrimSpace(string(vgpuTypeNumberBytes)))
		if err != nil {
			return nil, fmt.Errorf("unable to convert current vGPU type number to int: %w", err)
		}
		if vgpuTypeNumber != 0 {
			devices = append(devices, &vfioDevice{
				Path:   parentDevice.VirtualFunctionPath,
				Parent: parentDevice,
			})
		}
	}
	return devices, nil
}

// GetPhysicalFunction returns the physical function backing this vfioParentDevice (implements ParentDevice).
func (p *vfioParentDevice) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	if p.NvidiaPCIDevice.SriovInfo.IsVF() {
		return p.NvidiaPCIDevice.SriovInfo.VirtualFunction.PhysicalFunction
	}
	// Either it is an SRIOV physical function or a non-SRIOV device, so return the device itself
	return p.NvidiaPCIDevice
}

// GetPhysicalFunction delegates to the parent vfioParentDevice (implements Device).
func (d *vfioDevice) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	return d.Parent.GetPhysicalFunction()
}

// getIdForVGPUTypeName parses creatable_vgpu_types-style file at filePath for vgpuTypeName's numeric ID.
func (p *vfioParentDevice) getIdForVGPUTypeName(filePath string, vgpuTypeName string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("unable to open file %s: %w", filePath, err)
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

// isVFIOEnabled reports whether creatable_vgpu_types exists under virtfn0/nvidia for the GPU at index gpu.
func (m *nvvfio) isVFIOEnabled(gpu int) (bool, error) {
	nvdevice, err := m.nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return false, fmt.Errorf("unable to get GPU by index %d: %w", gpu, err)
	}
	// Check if vfio exists and has entries
	vfioPath := filepath.Join(nvdevice.Path, "virtfn0", "nvidia")
	creatableTypesFile := filepath.Join(vfioPath, "creatable_vgpu_types")

	_, statErr := os.Stat(creatableTypesFile)
	if statErr == nil {
		return true, nil
	}

	return false, nil
}

// IsVGPUTypeAvailable reports whether vgpuType appears in this VF's creatable_vgpu_types (implements ParentDevice).
func (p *vfioParentDevice) IsVGPUTypeAvailable(vgpuType string) (bool, error) {
	creatableTypesPath := filepath.Join(p.VirtualFunctionPath, "creatable_vgpu_types")
	file, err := os.Open(creatableTypesPath)
	if err != nil {
		return false, fmt.Errorf("unable to open file %s: %w", creatableTypesPath, err)
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
		if name == vgpuType {
			return true, nil
		}
	}
	return false, nil
}

// Delete clears current_vgpu_type on this vfioDevice (implements Device).
func (d *vfioDevice) Delete() error {
	currentVGPUTypePath := filepath.Join(d.Path, "current_vgpu_type")
	err := os.WriteFile(currentVGPUTypePath, []byte("0"), 0644)
	if err != nil {
		return fmt.Errorf("unable to write to %s: %w", currentVGPUTypePath, err)
	}
	return nil
}

// CreateVGPUDevice writes the vGPU type ID to current_vgpu_type for this VF (implements ParentDevice).
func (p *vfioParentDevice) CreateVGPUDevice(vgpuType string, vfPath string) error {
	currentVGPUTypePath := filepath.Join(vfPath, "current_vgpu_type")
	number, err := p.getIdForVGPUTypeName(filepath.Join(vfPath, "creatable_vgpu_types"), vgpuType)
	if err != nil {
		return fmt.Errorf("unable to get vGPU type id for vGPU type name %q: %w", vgpuType, err)
	}
	err = os.WriteFile(currentVGPUTypePath, []byte(strconv.Itoa(number)), 0644)
	if err != nil {
		return fmt.Errorf("error writing to file %q: %w", currentVGPUTypePath, err)
	}
	return nil
}

// GetAvailableVGPUInstances returns 1 if vgpuType is available on this VF, else 0 (implements ParentDevice).
func (p *vfioParentDevice) GetAvailableVGPUInstances(vgpuType string) (int, error) {
	available, err := p.IsVGPUTypeAvailable(vgpuType)
	if err != nil {
		return 0, fmt.Errorf("unable to check if vGPU type is available: %w", err)
	}
	if available {
		return 1, nil
	}
	return 0, nil
}

// CreateVGPUDevices creates up to count vGPUs of vgpuType on VFs of the given physical GPU (implements Interface).
func (m *nvvfio) CreateVGPUDevices(device *nvpci.NvidiaPCIDevice, vgpuType string, count int) error {
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
	i := 0
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
		vfioParent, ok := parent.(*vfioParentDevice)
		if !ok {
			return fmt.Errorf("internal error: expected *vfioParentDevice, got %T", parent)
		}
		err = vfioParent.CreateVGPUDevice(vgpuType, vfioParent.VirtualFunctionPath)
		if err != nil {
			return fmt.Errorf("unable to create %s vGPU device on parent device %s: %v", vgpuType, vfioParent.VirtualFunctionPath, err)
		}
		remainingToCreate--
		i++
	}
	if remainingToCreate > 0 {
		return fmt.Errorf("failed to create %[1]d %[2]s vGPU devices on the GPU. ensure '%[1]d' does not exceed the maximum supported instances for '%[2]s'", count, vgpuType)
	}
	return nil
}
