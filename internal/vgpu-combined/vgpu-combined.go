package vgpu_combined

import (
	"fmt"
	"strconv"

	"github.com/NVIDIA/go-nvlib/pkg/nvmdev"
	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
	"github.com/NVIDIA/vgpu-device-manager/internal/nvlib"
	"github.com/NVIDIA/vgpu-device-manager/internal/vfio"
	"github.com/google/uuid"
)

type VGPUCombinedManager struct {
	isVFIOMode bool
	vfio       *vfio.VFIOManager
	nvlib      nvlib.Interface
}

func NewVGPUCombinedManager() (*VGPUCombinedManager, error) {
	nvlibInstance := nvlib.New()
	vfioManager := vfio.NewVFIOManager(nvlibInstance)

	// Determine mode once at initialization
	isVFIOMode, err := vfioManager.IsVFIOEnabled(0)
	if err != nil {
		return nil, fmt.Errorf("error checking if VFIO is enabled: %v", err)
	}

	return &VGPUCombinedManager{
		isVFIOMode: isVFIOMode,
		vfio:       vfioManager,
		nvlib:      nvlibInstance,
	}, nil
}

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

// IsVFIOMode returns true if the manager is running in VFIO mode, false for MDEV mode
func (m *VGPUCombinedManager) IsVFIOMode() bool {
	return m.isVFIOMode
}

// GetNvpci returns the nvpci interface for GPU enumeration
func (m *VGPUCombinedManager) GetNvpci() nvpci.Interface {
	return m.nvlib.Nvpci
}

// GetNvmdev returns the nvmdev interface for MDEV operations
func (m *VGPUCombinedManager) GetNvmdev() nvmdev.Interface {
	return m.nvlib.Nvmdev
}

// GetAllParentDevices returns all parent devices as a common interface type
func (m *VGPUCombinedManager) GetAllParentDevices() ([]ParentDeviceInterface, error) {
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

func (m *VGPUCombinedManager) GetAllParentDevicesbyAddress(address string) ([]ParentDeviceInterface, error) {
	allParents, err := m.GetAllParentDevices()
	if err != nil {
		return nil, fmt.Errorf("error getting all parent devices: %v", err)
	}

	// Filter for 'parent' devices that are backed by the physical function
	parents := []ParentDeviceInterface{}
	for _, p := range allParents {
		pf := p.GetPhysicalFunction()
		if pf.Address == address {
			parents = append(parents, p)
		}
	}

	if len(parents) == 0 {
		return nil, fmt.Errorf("no parent devices found for GPU at address '%s'", address)
	}
	return parents, nil
}

// GetAllDevices returns all vGPU device instances as a common interface type
func (m *VGPUCombinedManager) GetAllDevices() ([]DeviceInterface, error) {
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

func (m *VGPUCombinedManager) CreateVGPUDevices(device *nvpci.NvidiaPCIDevice, vgpuType string, count int) error {
	parents, err := m.GetAllParentDevicesbyAddress(device.Address)
	if err != nil {
		return fmt.Errorf("error getting all parent devices by address: %v", err)
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
			if m.IsVFIOMode() {
				err = parent.CreateVGPUDevice(vgpuType, strconv.Itoa(i))
				if err != nil {
					return fmt.Errorf("unable to create %s vGPU device on parent device %s: %v", vgpuType, parent.GetPhysicalFunction().Address, err)
				}
			} else {
				err = parent.CreateVGPUDevice(vgpuType, uuid.New().String())
				if err != nil {
					return fmt.Errorf("unable to create %s vGPU device on parent device %s: %v", vgpuType, parent.GetPhysicalFunction().Address, err)
				}
			}
		}
		remainingToCreate -= numToCreate
	}
	if remainingToCreate > 0 {
		return fmt.Errorf("failed to create %[1]d %[2]s vGPU devices on the GPU. ensure '%[1]d' does not exceed the maximum supported instances for '%[2]s'", count, vgpuType)
	}
	return nil
}
