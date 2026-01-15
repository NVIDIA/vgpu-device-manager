package vfio

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
	"github.com/NVIDIA/vgpu-device-manager/internal/nvlib"
)

const (
	HostPCIDevicesRoot = "/host/sys/bus/pci/devices"
)

type VFIOManager struct {
	nvlib nvlib.Interface
}

func NewVFIOManager(nvlibInstance nvlib.Interface) *VFIOManager {
	return &VFIOManager{nvlib: nvlibInstance}
}

// ParentDevice represents an NVIDIA parent PCI device.
type ParentDevice struct {
	*nvpci.NvidiaPCIDevice
	VirtualFunctionPath string
}

// Device represents an NVIDIA (vGPU) device.
type Device struct {
	Path   string
	Parent *ParentDevice
}

func (m *VFIOManager) GetAllParentDevices() ([]*ParentDevice, error) {
	nvdevices, err := m.nvlib.Nvpci.GetGPUs()
	if err != nil {
		return nil, fmt.Errorf("unable to get all NVIDIA GPU devices: %v", err)
	}
	parentDevices := []*ParentDevice{}
	for _, device := range nvdevices {
		vfnum := 0
		numVF := int(device.SriovInfo.PhysicalFunction.NumVFs)
		for vfnum < numVF {
			vfAddr := filepath.Join(HostPCIDevicesRoot, device.Address, "virtfn"+strconv.Itoa(vfnum), "nvidia")
			if _, err := os.Stat(vfAddr); err != nil {
				return nil, fmt.Errorf("virtual function %d at address %s does not exist", vfnum, vfAddr)
			}
			parentDevices = append(parentDevices, &ParentDevice{
				NvidiaPCIDevice: device,
				VirtualFunctionPath: vfAddr,
			})
			vfnum++
		}
	}
	return parentDevices, nil
}

func (m *VFIOManager) GetAllDevices() ([]*Device, error) {
	parentDevices, err := m.GetAllParentDevices()
	if err != nil {
		return nil, fmt.Errorf("unable to get all parent devices: %v", err)
	}
	devices := []*Device{}
	for _, parentDevice := range parentDevices {
			vgpuTypeNumberBytes, err := os.ReadFile(filepath.Join(parentDevice.VirtualFunctionPath, "current_vgpu_type"))
			if err != nil {
				return nil, fmt.Errorf("unable to read current vGPU type: %v", err)
			}
			vgpuTypeNumber, err := strconv.Atoi(strings.TrimSpace(string(vgpuTypeNumberBytes)))
			if err != nil {
				return nil, fmt.Errorf("unable to convert current vGPU type number to int: %v", err)
			}
			if vgpuTypeNumber != 0 {
				devices = append(devices, &Device{
					Path:   parentDevice.VirtualFunctionPath,
					Parent: parentDevice,
				})
			}
	}
	return devices, nil
}

// GetPhysicalFunction gets the physical PCI device backing a 'parent' device.
func (p *ParentDevice) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	if p.NvidiaPCIDevice.SriovInfo.IsVF() {
		return p.NvidiaPCIDevice.SriovInfo.VirtualFunction.PhysicalFunction
	}
	// Either it is an SRIOV physical function or a non-SRIOV device, so return the device itself
	return p.NvidiaPCIDevice
}

// GetPhysicalFunction gets the physical PCI device that a vGPU is created on.
func (m *Device) GetPhysicalFunction() *nvpci.NvidiaPCIDevice {
	return m.Parent.GetPhysicalFunction()
}

// GetIdForVGPUTypeName returns the vGPU type ID for a given type name
func (p *ParentDevice) GetIdForVGPUTypeName(filePath string, vgpuTypeName string) (int, error) {
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
func (m *VFIOManager) IsVFIOEnabled(gpu int) (bool, error) {
	time.Sleep(10 * time.Second) // Wait for 10 seconds to ensure the virtual functions are ready
	nvdevice, err := m.nvlib.Nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return false, fmt.Errorf("unable to get GPU by index %d: %v", gpu, err)
	}
	// Check if vfio exists and has entries
	vfioPath := filepath.Join(HostPCIDevicesRoot, nvdevice.Address, "virtfn0", "nvidia")
	creatableTypesFile := filepath.Join(vfioPath, "creatable_vgpu_types")

	_, statErr := os.Stat(creatableTypesFile)
	if statErr == nil {
		return true, nil
	}

	return false, fmt.Errorf("unable to stat creatable_vgpu_types file at %s: %v", creatableTypesFile, statErr)
}

// IsVGPUTypeSupported checks if the vfioType is supported by this parent GPU
func (p *ParentDevice) IsVGPUTypeAvailable(vfioType string) (bool, error) {
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
		if name == vfioType {
			return true, nil
		}
	}
	return false, nil
}

// Delete deletes a vGPU type from a specific GPU
func (m *Device) Delete() error {
	currentVGPUTypePath := filepath.Join(m.Path, "current_vgpu_type")
	err := os.WriteFile(currentVGPUTypePath, []byte("0"), 0644)
	if err != nil {
		return fmt.Errorf("unable to write to %s: %v", currentVGPUTypePath, err)
	}
	return nil
}

func (p *ParentDevice) CreateVGPUDevice(vfioType string, vfnum string) error {
	vfPath := p.VirtualFunctionPath
	currentVGPUTypePath := filepath.Join(vfPath, "current_vgpu_type")
	number, err := p.GetIdForVGPUTypeName(filepath.Join(vfPath, "creatable_vgpu_types"), vfioType)
	if err != nil {
		return fmt.Errorf("unable to get vGPU type number: %v", err)
	}
	err = os.WriteFile(currentVGPUTypePath, []byte(strconv.Itoa(number)), 0644)
	if err != nil {
		return fmt.Errorf("unable to write current vGPU type: %v", err)
	}
	return nil
}

func (p *ParentDevice) GetAvailableVGPUInstances(vfioType string) (int, error) {
	available, err := p.IsVGPUTypeAvailable(vfioType)
	if err != nil {
		return 0, fmt.Errorf("unable to check if vGPU type is available: %v", err)
	}
	if available {
		return 1, nil
	}
	return 0, nil
}
