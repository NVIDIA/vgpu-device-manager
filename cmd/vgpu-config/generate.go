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

import (
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	cli "github.com/urfave/cli/v2"

	v1 "github.com/NVIDIA/vgpu-device-manager/api/spec/v1"
	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

const (
	// TODO: make the default's configurable
	defaultVGPUConfigName    = "default"
	framebufferPolicyMax     = "max"
	framebufferPolicyHalf    = "half"
	framebufferPolicyMin     = "min"
	defaultFramebufferPolicy = framebufferPolicyHalf
)

// Generate converts 'vgpuConfig.xml' into a configuration file (yaml) for the vGPU Device Manager
func Generate(c *cli.Context, f *flags) error {
	xmlFile, err := parseXMLFile(f)
	if err != nil {
		return fmt.Errorf("error parsing xml file: %v", err)
	}

	// Mapping between vGPU type id and vGPU type information in the xml file
	idToType := map[int]VGPUType{}
	for _, v := range xmlFile.VGPUTypes {
		idToType[v.ID] = v
	}

	// Initialize the vGPU Device Manager configuration spec
	spec := v1.Spec{
		Version:     "v1",
		VGPUConfigs: map[string]v1.VGPUConfigSpecSlice{},
	}

	// The default configuration will contain one entry per physical GPU supported
	defaultConfig := v1.VGPUConfigSpecSlice{}

	for _, p := range xmlFile.PGPUs {
		// Mapping VGPU series to the list of supported VGPU types for the PGPU.
		// Will use this later when picking a default vGPU type for the PGPU.
		supportedVGPUs := map[types.Series][]*types.VGPUType{}
		for _, v := range p.SupportedVGPUs {
			// Only process vGPU types of class 'Quadro' or 'Compute'.
			// This restriction may be relaxed in the future.
			class := idToType[v.ID].Class
			if class == "NVS" {
				continue
			}

			_type := idToType[v.ID]
			// Strip product prefixes from the vGPU type name string
			typeName, err := stripVGPUTypeName(_type.Name)
			if err != nil {
				return fmt.Errorf("failed to strip product prefixes from vGPU type name %q: %w", _type.Name, err)
			}

			vgpuType, err := types.ParseVGPUType(typeName)
			if err != nil {
				return fmt.Errorf("could not parse vGPU type %q: %v", _type.Name, err)
			}

			// Add entry for this vGPU Type in the config
			vgpuTypeStr := vgpuType.String()
			spec.VGPUConfigs[vgpuTypeStr] = v1.VGPUConfigSpecSlice{
				v1.VGPUConfigSpec{
					Devices: "all",
					VGPUDevices: types.VGPUConfig{
						vgpuTypeStr: v.MaxVGPUs,
					},
				},
			}

			// Only consider non MIG-backed types later on when picking a default type for the PGPU.
			// Note: 'G' is the number of GPU instances
			if vgpuType.G == 0 {
				supportedVGPUs[vgpuType.S] = append(supportedVGPUs[vgpuType.S], vgpuType)
			}
		}

		// The below picks a default vGPU type for the PGPU. A Q-series type is selected by default
		// unless the PGPU does not support Q-series, then C-series is used.
		vgpuSlice := supportedVGPUs['Q']
		if len(supportedVGPUs['Q']) == 0 && len(supportedVGPUs['C']) == 0 {
			continue
		}
		if len(supportedVGPUs['Q']) == 0 {
			vgpuSlice = supportedVGPUs['C']
		}

		defaultVGPUType, err := getDefaultVGPUType(vgpuSlice, defaultFramebufferPolicy)
		if err != nil {
			return fmt.Errorf("error getting default vGPU type: %v", err)
		}

		defaultName := defaultVGPUType.String()
		numInstances := spec.VGPUConfigs[defaultName][0].VGPUDevices[defaultName]

		deviceFilter, err := getDeviceFilterString(p.DeviceID)
		if err != nil {
			return fmt.Errorf("error getting device filter: %v", err)
		}

		// Add default config entry for the PGPU
		defaultConfig = append(defaultConfig, v1.VGPUConfigSpec{
			DeviceFilter: deviceFilter,
			Devices:      "all",
			VGPUDevices: types.VGPUConfig{
				defaultName: numInstances,
			},
		})
	}

	spec.VGPUConfigs[defaultVGPUConfigName] = defaultConfig

	data, err := yaml.Marshal(&spec)
	if err != nil {
		return fmt.Errorf("error marshalling data: %v", err)
	}

	err = os.WriteFile(f.outputFile, data, 0600)
	if err != nil {
		return fmt.Errorf("could not write to file: %v", err)
	}
	return nil
}

func parseXMLFile(f *flags) (*VGPUConfig, error) {
	xmlFile, err := os.ReadFile(f.xmlFile)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %v", err)
	}

	var vgpuConfig VGPUConfig
	err = xml.Unmarshal(xmlFile, &vgpuConfig)
	if err != nil {
		return nil, fmt.Errorf("unmarshal error: %v", err)
	}

	return &vgpuConfig, nil
}

// Extract the vGPU type name from a string that may contain product prefixes.
// Examples:
//   - "NVIDIA A100-4C" -> "A100-4C"
//   - "NVIDIA RTX Pro 6000 Blackwell DC-48C" -> "DC-48C"
func stripVGPUTypeName(s string) (string, error) {
	nameStr := strings.TrimSpace(s)
	nameSplit := strings.Split(nameStr, " ")
	typeName := nameSplit[len(nameSplit)-1]
	if typeName == "" {
		return "", fmt.Errorf("unable to parse mdev_type name from: %s", s)
	}
	return typeName, nil
}

func getDefaultVGPUType(vgpuTypes []*types.VGPUType, policy string) (*types.VGPUType, error) {
	// Sort in descending order by framebuffer size in GB
	sort.Slice(vgpuTypes, func(i, j int) bool {
		return vgpuTypes[i].GB > vgpuTypes[j].GB
	})

	if len(vgpuTypes) == 0 {
		return nil, fmt.Errorf("no vGPU types")
	}
	// For GH200, there is only one valid vGPU type, GH200-96C, when MIG is not enabled
	if len(vgpuTypes) == 1 {
		return vgpuTypes[0], nil
	}

	halfGB := vgpuTypes[0].GB / 2
	switch policy {
	case framebufferPolicyMax:
		return vgpuTypes[0], nil
	case framebufferPolicyMin:
		return vgpuTypes[len(vgpuTypes)-1], nil
	case framebufferPolicyHalf:
		for i, v := range vgpuTypes {
			if v.GB == halfGB {
				return vgpuTypes[i], nil
			}
		}
		return nil, fmt.Errorf("error finding a vGPU type with half the max framebuffer size")
	default:
		return nil, fmt.Errorf("invalid policy '%s' for selecting default vGPU type", policy)
	}
}

func getDeviceFilterString(deviceInfo DeviceID) (string, error) {
	deviceID, err := strconv.ParseUint(deviceInfo.DeviceID, 0, 16)
	if err != nil {
		return "", fmt.Errorf("unable to convert device id string to uint16: %v", err)
	}

	vendorID, err := strconv.ParseUint(deviceInfo.VendorID, 0, 16)
	if err != nil {
		return "", fmt.Errorf("unable to convert vendor id string to uint16: %v", err)
	}

	deviceFilter := types.NewDeviceID(uint16(deviceID), uint16(vendorID))
	return deviceFilter.String(), nil
}
