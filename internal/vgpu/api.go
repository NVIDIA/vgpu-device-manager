package vgpu

import (
	"fmt"

	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
)

// Interface defines the vGPU manager operations
type Interface interface {
	GetAllDevices() ([]Device, error)
	GetAllParentDevices() ([]ParentDevice, error)
	CreateVGPUDevices(device *nvpci.NvidiaPCIDevice, vgpuType string, count int) error
}

// ParentDevice represents a common interface for both VFIO and MDEV parent devices
type ParentDevice interface {
	GetPhysicalFunction() *nvpci.NvidiaPCIDevice
	IsVGPUTypeAvailable(string) (bool, error)
	CreateVGPUDevice(string, string) error
	GetAvailableVGPUInstances(string) (int, error)
}

// Device represents a common interface for both VFIO and MDEV vGPU device instances
type Device interface {
	GetPhysicalFunction() *nvpci.NvidiaPCIDevice
	Delete() error
}

// New creates a new vGPU manager (either VFIO or MDEV based)
func New() (Interface, error) {
	vfioInstance := NewNvvfio()

	// Determine mode once at initialization
	isVFIOMode, err := vfioInstance.IsVFIOEnabled(0)
	if err != nil {
		return nil, fmt.Errorf("error checking VFIO mode: %v", err)
	}

	if isVFIOMode {
		return vfioInstance, nil
	}
	// Use MDEV mode
	return NewNvmdev(), nil
}