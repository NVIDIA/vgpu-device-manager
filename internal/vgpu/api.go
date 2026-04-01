package vgpu

import (
	"fmt"

	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
)

// Interface is the exported vGPU manager API (implemented by *nvvfio and *nvmdev).
type Interface interface {
	GetAllDevices() ([]Device, error)
	GetAllParentDevices() ([]ParentDevice, error)
	CreateVGPUDevices(device *nvpci.NvidiaPCIDevice, vgpuType string, count int) error
}

// ParentDevice is implemented by *vfioParentDevice and *mdevParentDevice.
type ParentDevice interface {
	GetPhysicalFunction() *nvpci.NvidiaPCIDevice
	IsVGPUTypeAvailable(string) (bool, error)
	CreateVGPUDevice(string, string) error
	GetAvailableVGPUInstances(string) (int, error)
}

// Device is implemented by *vfioDevice and *mdevDevice.
type Device interface {
	GetPhysicalFunction() *nvpci.NvidiaPCIDevice
	Delete() error
}

// New returns an Interface, selecting *nvvfio when isVFIOEnabled(0) is true, otherwise *nvmdev from newNvmdev().
func New() (Interface, error) {
	vfioInstance := newNvvfio()

	isVFIOMode, err := vfioInstance.isVFIOEnabled(0)
	if err != nil {
		return nil, fmt.Errorf("error checking VFIO mode: %w", err)
	}

	if isVFIOMode {
		return vfioInstance, nil
	}
	return newNvmdev(), nil
}
