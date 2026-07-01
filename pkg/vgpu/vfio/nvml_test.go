/*
 * Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

package vfio

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
	"github.com/stretchr/testify/require"
)

// fakeVgpuTypeID is a minimal nvml.VgpuTypeId implementation defined as a
// named unsigned integer type, mirroring the concrete type used by go-nvml,
// so that numericVGPUTypeID can recover its numeric value via reflection.
type fakeVgpuTypeID uint32

var fakeVgpuTypeNames = map[uint32]string{
	1414: "NVIDIA H200X-1-18C",
	1428: "NVIDIA H200X-141C",
}

func (f fakeVgpuTypeID) GetBAR1Info() (nvml.VgpuTypeBar1Info, nvml.Return) {
	return nvml.VgpuTypeBar1Info{}, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetCapabilities(nvml.VgpuCapability) (bool, nvml.Return) {
	return false, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetClass() (string, nvml.Return) {
	return "", nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetCreatablePlacements(nvml.Device) (nvml.VgpuPlacementList, nvml.Return) {
	return nvml.VgpuPlacementList{}, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetDeviceID() (uint64, uint64, nvml.Return) {
	return 0, 0, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetFrameRateLimit() (uint32, nvml.Return) {
	return 0, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetFramebufferSize() (uint64, nvml.Return) {
	return 0, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetGpuInstanceProfileId() (uint32, nvml.Return) {
	return 0, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetLicense() (string, nvml.Return) {
	return "", nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetMaxInstances(nvml.Device) (int, nvml.Return) {
	return 0, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetMaxInstancesPerVm() (int, nvml.Return) {
	return 0, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetName() (string, nvml.Return) {
	name, ok := fakeVgpuTypeNames[uint32(f)]
	if !ok {
		return "", nvml.ERROR_NOT_FOUND
	}
	return name, nvml.SUCCESS
}

func (f fakeVgpuTypeID) GetNumDisplayHeads() (int, nvml.Return) {
	return 0, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetResolution(int) (uint32, uint32, nvml.Return) {
	return 0, 0, nvml.ERROR_NOT_SUPPORTED
}

func (f fakeVgpuTypeID) GetSupportedPlacements(nvml.Device) (nvml.VgpuPlacementList, nvml.Return) {
	return nvml.VgpuPlacementList{}, nvml.ERROR_NOT_SUPPORTED
}

var _ nvml.VgpuTypeId = fakeVgpuTypeID(0)

// newMockNVML builds a mock NVML library whose device at any PCI address
// reports the provided vGPU type IDs.
func newMockNVML(initRet nvml.Return, device nvml.Device, handleRet nvml.Return) *mock.Interface {
	return &mock.Interface{
		InitFunc: func() nvml.Return {
			return initRet
		},
		ShutdownFunc: func() nvml.Return {
			return nvml.SUCCESS
		},
		DeviceGetHandleByPciBusIdFunc: func(s string) (nvml.Device, nvml.Return) {
			return device, handleRet
		},
	}
}

func newMockDevice(typeIDs []nvml.VgpuTypeId, ret nvml.Return) *mock.Device {
	return &mock.Device{
		GetSupportedVgpusFunc: func() ([]nvml.VgpuTypeId, nvml.Return) {
			return typeIDs, ret
		},
	}
}

func TestNVMLTypeResolverSupportedVGPUTypes(t *testing.T) {
	supportedIDs := []nvml.VgpuTypeId{fakeVgpuTypeID(1414), fakeVgpuTypeID(1428)}

	t.Run("Supported types are returned as an ID to name map", func(t *testing.T) {
		device := newMockDevice(supportedIDs, nvml.SUCCESS)
		resolver := &nvmlTypeResolver{nvmllib: newMockNVML(nvml.SUCCESS, device, nvml.SUCCESS)}

		supported, err := resolver.SupportedVGPUTypes("0000:18:00.0")
		require.NoError(t, err)
		require.Equal(t, map[int]string{
			1414: "NVIDIA H200X-1-18C",
			1428: "NVIDIA H200X-141C",
		}, supported)
	})

	t.Run("Already initialized NVML is not an error", func(t *testing.T) {
		device := newMockDevice(supportedIDs, nvml.SUCCESS)
		resolver := &nvmlTypeResolver{nvmllib: newMockNVML(nvml.ERROR_ALREADY_INITIALIZED, device, nvml.SUCCESS)}

		_, err := resolver.SupportedVGPUTypes("0000:18:00.0")
		require.NoError(t, err)
	})

	t.Run("NVML initialization failure returns error", func(t *testing.T) {
		resolver := &nvmlTypeResolver{nvmllib: newMockNVML(nvml.ERROR_LIBRARY_NOT_FOUND, nil, nvml.SUCCESS)}

		_, err := resolver.SupportedVGPUTypes("0000:18:00.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "initializing NVML")
	})

	t.Run("Device handle lookup failure returns error", func(t *testing.T) {
		resolver := &nvmlTypeResolver{nvmllib: newMockNVML(nvml.SUCCESS, nil, nvml.ERROR_NOT_FOUND)}

		_, err := resolver.SupportedVGPUTypes("0000:18:00.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "device handle")
	})

	t.Run("Supported vGPU types query failure returns error", func(t *testing.T) {
		device := newMockDevice(nil, nvml.ERROR_NOT_SUPPORTED)
		resolver := &nvmlTypeResolver{nvmllib: newMockNVML(nvml.SUCCESS, device, nvml.SUCCESS)}

		_, err := resolver.SupportedVGPUTypes("0000:18:00.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "supported vGPU types")
	})

	t.Run("Type name lookup failure returns error", func(t *testing.T) {
		device := newMockDevice([]nvml.VgpuTypeId{fakeVgpuTypeID(9999)}, nvml.SUCCESS)
		resolver := &nvmlTypeResolver{nvmllib: newMockNVML(nvml.SUCCESS, device, nvml.SUCCESS)}

		_, err := resolver.SupportedVGPUTypes("0000:18:00.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "name of vGPU type")
	})

	t.Run("Opaque type ID without a numeric representation returns error", func(t *testing.T) {
		device := newMockDevice([]nvml.VgpuTypeId{&mock.VgpuTypeId{}}, nvml.SUCCESS)
		resolver := &nvmlTypeResolver{nvmllib: newMockNVML(nvml.SUCCESS, device, nvml.SUCCESS)}

		_, err := resolver.SupportedVGPUTypes("0000:18:00.0")
		require.Error(t, err)
		require.Contains(t, err.Error(), "numeric vGPU type ID")
	})
}

func TestNumericVGPUTypeID(t *testing.T) {
	t.Run("Named unsigned integer type", func(t *testing.T) {
		id, err := numericVGPUTypeID(fakeVgpuTypeID(1414))
		require.NoError(t, err)
		require.Equal(t, 1414, id)
	})

	t.Run("Struct-based mock has no numeric representation", func(t *testing.T) {
		_, err := numericVGPUTypeID(&mock.VgpuTypeId{})
		require.Error(t, err)
	})
}
