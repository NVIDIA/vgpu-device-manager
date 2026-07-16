/*
 * Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
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

package main

// VGPUConfig contains information about all physical GPUs and their supported vGPU types
type VGPUConfig struct {
	Version   string     `xml:"version"`
	VGPUTypes []VGPUType `xml:"vgpuType"`
	PGPUs     []PGPU     `xml:"pgpu"`
}

// VGPUType represents a single vGPU type
type VGPUType struct {
	ID       int      `xml:"id,attr"`
	Name     string   `xml:"name,attr"`
	Class    string   `xml:"class,attr"`
	DeviceID DeviceID `xml:"devId"`
}

// DeviceID contains PCI identifier information
type DeviceID struct {
	VendorID          string `xml:"vendorId,attr"`
	DeviceID          string `xml:"deviceId,attr"`
	SubsystemVendorID string `xml:"subsystemVendorId,attr"`
	SubsystemID       string `xml:"subsystemId,attr"`
}

// PGPU represents a physical GPU
type PGPU struct {
	DeviceID       DeviceID        `xml:"devId"`
	SupportedVGPUs []SupportedVGPU `xml:"supportedVgpu"`
}

// SupportedVGPU represents a single vGPU type
type SupportedVGPU struct {
	ID       int `xml:"vgpuId,attr"`
	MaxVGPUs int `xml:"maxVgpus"`
}
