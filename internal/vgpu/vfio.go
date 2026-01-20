package vgpu

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
	"github.com/NVIDIA/vgpu-device-manager/internal/nvlib"
)

type Nvvfio struct {
	nvlib nvlib.Interface
}

// VFIOParentDevice represents an NVIDIA parent PCI device for VFIO.
type VFIOParentDevice struct {
	*nvpci.NvidiaPCIDevice
	VirtualFunctionPath string
}

// VFIODevice represents an NVIDIA (vGPU) device for VFIO.
type VFIODevice struct {
	Path   string
	Parent *VFIOParentDevice
}

func NewNvvfio() *Nvvfio {
	return &Nvvfio{
		nvlib: nvlib.New(),
	}
}

// GetAllParentDevices returns all parent devices as ParentDevice interface slice
func (m *Nvvfio) GetAllParentDevices() ([]ParentDevice, error) {
	nvdevices, err := m.nvlib.Nvpci.GetGPUs()
	if err != nil {
		return nil, fmt.Errorf("unable to get all NVIDIA GPU devices: %v", err)
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
			parentDevices = append(parentDevices, &VFIOParentDevice{
				NvidiaPCIDevice:     device,
				VirtualFunctionPath: vfAddr,
			})
			vfnum++
		}
	}
	return parentDevices, nil
}

// GetAllDevices returns all vGPU devices as Device interface slice
func (m *Nvvfio) GetAllDevices() ([]Device, error) {
	parentDevices, err := m.GetAllParentDevices()
	if err != nil {
		return nil, fmt.Errorf("unable to get all parent devices: %v", err)
	}
	devices := []Device{}
	for _, p := range parentDevices {
		parentDevice := p.(*VFIOParentDevice) // type assert to access VirtualFunctionPath
		vgpuTypeNumberBytes, err := os.ReadFile(filepath.Join(parentDevice.VirtualFunctionPath, "current_vgpu_type"))
		if err != nil {
			return nil, fmt.Errorf("unable to read current vGPU type: %v", err)
		}
		vgpuTypeNumber, err := strconv.Atoi(strings.TrimSpace(string(vgpuTypeNumberBytes)))
		if err != nil {
			return nil, fmt.Errorf("unable to convert current vGPU type number to int: %v", err)
		}
		if vgpuTypeNumber != 0 {
			devices = append(devices, &VFIODevice{
				Path:   parentDevice.VirtualFunctionPath,
				Parent: parentDevice,
			})
		}
	}
	return devices, nil
}

// GetPhysicalFunction gets the physical PCI device backing a 'parent' device.
func (p *VFIOParentDevice) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	if p.NvidiaPCIDevice.SriovInfo.IsVF() {
		return p.NvidiaPCIDevice.SriovInfo.VirtualFunction.PhysicalFunction
	}
	// Either it is an SRIOV physical function or a non-SRIOV device, so return the device itself
	return p.NvidiaPCIDevice
}

// GetPhysicalFunction gets the physical PCI device that a vGPU is created on.
func (d *VFIODevice) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	return d.Parent.GetPhysicalFunction()
}

// GetIdForVGPUTypeName returns the vGPU type ID for a given type name
func (p *VFIOParentDevice) GetIdForVGPUTypeName(filePath string, vgpuTypeName string) (int, error) {
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

// IsVFIOEnabled checks if VFIO is enabled for a specific GPU
func (m *Nvvfio) IsVFIOEnabled(gpu int) (bool, error) {
	nvdevice, err := m.nvlib.Nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return false, fmt.Errorf("unable to get GPU by index %d: %v", gpu, err)
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

// IsVGPUTypeAvailable checks if the vgpuType is supported by this parent GPU
func (p *VFIOParentDevice) IsVGPUTypeAvailable(vgpuType string) (bool, error) {
	creatableTypesPath := filepath.Join(p.VirtualFunctionPath, "creatable_vgpu_types")
	file, err := os.Open(creatableTypesPath)
	if err != nil {
		return false, fmt.Errorf("unable to open file %s: %v", creatableTypesPath, err)
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

// Delete deletes a vGPU type from a specific GPU
func (d *VFIODevice) Delete() error {
	currentVGPUTypePath := filepath.Join(d.Path, "current_vgpu_type")
	err := os.WriteFile(currentVGPUTypePath, []byte("0"), 0644)
	if err != nil {
		return fmt.Errorf("unable to write to %s: %v", currentVGPUTypePath, err)
	}
	return nil
}

func (p *VFIOParentDevice) CreateVGPUDevice(vgpuType string, vfnum string) error {
	vfPath := p.VirtualFunctionPath
	currentVGPUTypePath := filepath.Join(vfPath, "current_vgpu_type")
	number, err := p.GetIdForVGPUTypeName(filepath.Join(vfPath, "creatable_vgpu_types"), vgpuType)
	if err != nil {
		return fmt.Errorf("unable to get vGPU type number: %v", err)
	}
	err = os.WriteFile(currentVGPUTypePath, []byte(strconv.Itoa(number)), 0644)
	if err != nil {
		return fmt.Errorf("unable to write current vGPU type: %v", err)
	}
	return nil
}

func (p *VFIOParentDevice) GetAvailableVGPUInstances(vgpuType string) (int, error) {
	available, err := p.IsVGPUTypeAvailable(vgpuType)
	if err != nil {
		return 0, fmt.Errorf("unable to check if vGPU type is available: %v", err)
	}
	if available {
		return 1, nil
	}
	return 0, nil
}

func (m *Nvvfio) CreateVGPUDevices(device *nvpci.NvidiaPCIDevice, vgpuType string, count int) error {
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
			err = parent.CreateVGPUDevice(vgpuType, strconv.Itoa(i))
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
