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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// DefaultPCIDevicesRoot represents the base path for all PCI devices under sysfs.
	DefaultPCIDevicesRoot = "/sys/bus/pci/devices"

	nvidiaVendorID        = 0x10de
	pciVgaControllerClass = 0x030000
	pci3dControllerClass  = 0x030200

	nvidiaDriver = "nvidia"

	vgpuSysfsDir           = "nvidia"
	creatableVGPUTypesFile = "creatable_vgpu_types"
	currentVGPUTypeFile    = "current_vgpu_type"
	gpuInstanceIDFile      = "gpu_instance_id"
)

// device represents a PCI device under the sysfs PCI devices root.
type device struct {
	root    string
	address string
}

// path returns the sysfs path of the device, optionally joined with the
// provided path elements.
func (d device) path(elem ...string) string {
	return filepath.Join(append([]string{d.root, d.address}, elem...)...)
}

// readString reads a sysfs file of the device and returns its trimmed contents.
func (d device) readString(elem ...string) (string, error) {
	data, err := os.ReadFile(d.path(elem...))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// readInt reads a sysfs file of the device and parses it as an integer.
func (d device) readInt(elem ...string) (int, error) {
	s, err := d.readString(elem...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

// readHex reads a sysfs file of the device and parses it as a hexadecimal number.
func (d device) readHex(name string) (uint32, error) {
	s, err := d.readString(name)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

// driver returns the name of the kernel driver the device is bound to.
func (d device) driver() (string, error) {
	target, err := os.Readlink(d.path("driver"))
	if err != nil {
		return "", err
	}
	return filepath.Base(target), nil
}

// isNvidiaGPU reports whether the device is an NVIDIA GPU (VGA or 3D controller).
func (d device) isNvidiaGPU() bool {
	vendor, err := d.readHex("vendor")
	if err != nil || vendor != nvidiaVendorID {
		return false
	}
	class, err := d.readHex("class")
	if err != nil {
		return false
	}
	return class == pciVgaControllerClass || class == pci3dControllerClass
}

// isVF reports whether the device is an SR-IOV virtual function.
func (d device) isVF() bool {
	_, err := os.Lstat(d.path("physfn"))
	return err == nil
}

// isVGPUCapablePF reports whether the device is an SR-IOV physical function
// bound to the NVIDIA driver, i.e. a GPU whose vGPU devices are managed
// through the vendor-specific VFIO framework.
func (d device) isVGPUCapablePF() bool {
	if _, err := os.Stat(d.path("sriov_totalvfs")); err != nil {
		return false
	}
	driver, err := d.driver()
	if err != nil {
		return false
	}
	return driver == nvidiaDriver
}

// hasVGPUSysfs reports whether the vendor-specific VFIO sysfs interface
// (the per-device 'nvidia' directory) is present for the device.
func (d device) hasVGPUSysfs() bool {
	info, err := os.Stat(d.path(vgpuSysfsDir))
	return err == nil && info.IsDir()
}

// currentVGPUType returns the numeric ID of the vGPU type currently
// configured on the device, or 0 if no vGPU device is configured.
func (d device) currentVGPUType() (int, error) {
	return d.readInt(vgpuSysfsDir, currentVGPUTypeFile)
}

// creatableVGPUTypes returns the list of vGPU types that can currently be
// created on the device.
func (d device) creatableVGPUTypes() ([]vgpuType, error) {
	content, err := d.readString(vgpuSysfsDir, creatableVGPUTypesFile)
	if err != nil {
		return nil, err
	}
	return parseCreatableVGPUTypes(content), nil
}

// setVGPUType writes a vGPU type ID to the device, creating a vGPU device of
// that type on it. Writing 0 deletes the vGPU device.
func (d device) setVGPUType(id int) error {
	f, err := os.OpenFile(d.path(vgpuSysfsDir, currentVGPUTypeFile), os.O_WRONLY|os.O_TRUNC|os.O_SYNC, 0200)
	if err != nil {
		return fmt.Errorf("unable to open current_vgpu_type file: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(strconv.Itoa(id)); err != nil {
		return fmt.Errorf("unable to write vGPU type ID: %v", err)
	}
	return nil
}

// gpuInstanceID returns the ID of the MIG GPU instance the vGPU device on
// this device is bound to. It is only readable after a vGPU type has been
// set; callers must treat errors as "not available".
func (d device) gpuInstanceID() (int, error) {
	return d.readInt(vgpuSysfsDir, gpuInstanceIDFile)
}

// virtualFunctions returns the SR-IOV virtual functions of the device,
// ordered by VF index.
func (d device) virtualFunctions() ([]device, error) {
	links, err := filepath.Glob(d.path() + "/virtfn*")
	if err != nil {
		return nil, fmt.Errorf("unable to list virtual functions: %v", err)
	}

	indices := make([]int, 0, len(links))
	byIndex := make(map[int]string, len(links))
	for _, link := range links {
		index, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(link), "virtfn"))
		if err != nil {
			continue
		}
		target, err := os.Readlink(link)
		if err != nil {
			return nil, fmt.Errorf("unable to resolve virtual function link %s: %v", link, err)
		}
		indices = append(indices, index)
		byIndex[index] = filepath.Base(target)
	}
	sort.Ints(indices)

	vfs := make([]device, 0, len(indices))
	for _, index := range indices {
		vfs = append(vfs, device{root: d.root, address: byIndex[index]})
	}
	return vfs, nil
}

// gpuAddresses returns the PCI addresses of all NVIDIA GPUs (excluding
// SR-IOV virtual functions) under the provided PCI devices root, in sysfs
// enumeration order. The index of an address in the returned slice is the
// GPU index used throughout this package.
func gpuAddresses(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("unable to read PCI devices root: %v", err)
	}

	var addresses []string
	for _, entry := range entries {
		d := device{root: root, address: entry.Name()}
		if d.isNvidiaGPU() && !d.isVF() {
			addresses = append(addresses, entry.Name())
		}
	}
	return addresses, nil
}
