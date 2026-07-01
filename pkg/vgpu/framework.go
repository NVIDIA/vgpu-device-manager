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

package vgpu

import (
	"os"

	"github.com/NVIDIA/vgpu-device-manager/pkg/vgpu/vfio"
)

// DefaultMdevBusRoot represents the base path of the mediated device bus
// under sysfs. It is only present when the vGPU host driver uses the
// mediated device (mdev) framework.
const DefaultMdevBusRoot = "/sys/class/mdev_bus"

// Framework represents the framework the vGPU host driver uses to expose
// vGPU devices to the system.
type Framework string

const (
	// FrameworkMdev represents the mediated device (mdev) framework, used
	// by the NVIDIA vGPU Manager driver on GPUs up to and including the
	// Ampere architecture.
	FrameworkMdev = Framework("mdev")
	// FrameworkVendorVFIO represents the vendor-specific VFIO framework,
	// used by the NVIDIA vGPU Manager driver (vGPU 17.0+) on Ada, Hopper
	// and newer GPUs. vGPU devices are managed through per-VF sysfs files
	// instead of mediated devices.
	FrameworkVendorVFIO = Framework("vendor-vfio")
)

// DetectFramework detects the vGPU device management framework used by the
// vGPU host driver on this system.
func DetectFramework() Framework {
	return detectFramework(DefaultMdevBusRoot, vfio.DefaultPCIDevicesRoot)
}

// detectFramework detects the vGPU device management framework based on the
// provided sysfs roots. A populated mdev bus always indicates the mdev
// framework. Otherwise, an NVIDIA GPU exposed as an SR-IOV physical function
// bound to the 'nvidia' driver indicates the vendor-specific VFIO framework.
// The mdev framework is assumed by default to preserve the historical
// behavior (and error messages) on systems where neither is detected.
func detectFramework(mdevBusRoot, pciDevicesRoot string) Framework {
	if entries, err := os.ReadDir(mdevBusRoot); err == nil && len(entries) > 0 {
		return FrameworkMdev
	}
	if vfio.HasVGPUCapableDevices(pciDevicesRoot) {
		return FrameworkVendorVFIO
	}
	return FrameworkMdev
}

// NewVGPUConfigManager returns a vGPU config Manager matching the vGPU
// device management framework used by the vGPU host driver on this system.
// The hostRootMount parameter optionally points at a container mount of the
// host root filesystem; it is only used by the vendor-specific VFIO backend
// to run the sriov-manage script from the host driver installation.
func NewVGPUConfigManager(hostRootMount string) Manager {
	if DetectFramework() == FrameworkVendorVFIO {
		return vfio.New(vfio.WithHostRootMount(hostRootMount))
	}
	return NewNvlibVGPUConfigManager()
}
