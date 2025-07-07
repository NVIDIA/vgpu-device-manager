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

package v1

import (
	"encoding/json"
	"fmt"

	migpartedv1 "github.com/NVIDIA/mig-parted/api/spec/v1"
	migtypes "github.com/NVIDIA/mig-parted/pkg/types"

	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

// Version indicates the version of the 'Spec' struct used to hold information on 'VGPUConfigs'.
const Version = "v1"

// Spec is a versioned struct used to hold information on 'VGPUConfigs'.
type Spec struct {
	Version     string                         `json:"version" yaml:"version"`
	VGPUConfigs map[string]VGPUConfigSpecSlice `json:"vgpu-configs,omitempty" yaml:"vgpu-configs,omitempty"`
}

// VGPUConfigSpec defines the spec to declare the desired vGPU devices configuration for a set of GPUs.
type VGPUConfigSpec struct {
	DeviceFilter interface{}      `json:"device-filter,omitempty" yaml:"device-filter,flow,omitempty"`
	Devices      interface{}      `json:"devices"                 yaml:"devices,flow"`
	VGPUDevices  types.VGPUConfig `json:"vgpu-devices"             yaml:"vgpu-devices"`
}

// VGPUConfigSpecSlice represents a slice of 'VGPUConfigSpec'.
type VGPUConfigSpecSlice []VGPUConfigSpec

// UnmarshalJSON unmarshals raw bytes into a versioned 'Spec'.
func (s *Spec) UnmarshalJSON(b []byte) error {
	spec := make(map[string]json.RawMessage)
	err := json.Unmarshal(b, &spec)
	if err != nil {
		return err
	}

	if !containsKey(spec, "version") && len(spec) > 0 {
		return fmt.Errorf("unable to parse with missing 'version' field")
	}

	result := Spec{}
	for k, v := range spec {
		switch k {
		case "version":
			var version string
			err = json.Unmarshal(v, &version)
			if err != nil {
				return err
			}
			if version != Version {
				return fmt.Errorf("unknown version: %v", version)
			}
			result.Version = version
		case "vgpu-configs":
			configs := map[string]VGPUConfigSpecSlice{}
			err := json.Unmarshal(v, &configs)
			if err != nil {
				return err
			}
			if len(configs) == 0 {
				return fmt.Errorf("at least one entry in '%v' is required", k)
			}
			for c, s := range configs {
				if len(s) == 0 {
					return fmt.Errorf("at least one entry in '%v' is required", c)
				}
			}
			result.VGPUConfigs = configs
		default:
			return fmt.Errorf("unexpected field: %v", k)
		}
	}

	*s = result
	return nil
}

// UnmarshalJSON unmarshals raw bytes into a 'VGPUConfigSpec'.
func (s *VGPUConfigSpec) UnmarshalJSON(b []byte) error {
	spec := make(map[string]json.RawMessage)
	err := json.Unmarshal(b, &spec)
	if err != nil {
		return err
	}

	required := []string{"devices", "vgpu-devices"}
	for _, r := range required {
		if !containsKey(spec, r) {
			return fmt.Errorf("missing required field: %v", r)
		}
	}

	result := VGPUConfigSpec{}
	for k, v := range spec {
		switch k {
		case "device-filter":
			var str string
			err1 := json.Unmarshal(v, &str)
			if err1 == nil {
				result.DeviceFilter = str
				break
			}
			var strslice []string
			err2 := json.Unmarshal(v, &strslice)
			if err2 == nil {
				result.DeviceFilter = strslice
				break
			}
			return fmt.Errorf("(%v, %v)", err1, err2)
		case "devices":
			var str string
			err1 := json.Unmarshal(v, &str)
			if err1 == nil {
				if str != "all" {
					return fmt.Errorf("invalid string input for '%v': %v", k, str)
				}
				result.Devices = str
				break
			}
			var intslice []int
			err2 := json.Unmarshal(v, &intslice)
			if err2 == nil {
				result.Devices = intslice
				break
			}
			return fmt.Errorf("(%v, %v)", err1, err2)
		case "vgpu-devices":
			devices := make(types.VGPUConfig)
			err := json.Unmarshal(v, &devices)
			if err != nil {
				return err
			}
			err = devices.AssertValid()
			if err != nil {
				return fmt.Errorf("error validating values in '%v' field: %v", k, err)
			}
			result.VGPUDevices = devices
		default:
			return fmt.Errorf("unexpected field: %v", k)
		}
	}

	*s = result
	return nil
}

func (s VGPUConfigSpecSlice) ToMigConfigSpecSlice() (migpartedv1.MigConfigSpecSlice, error) {
	var migConfigSpecs migpartedv1.MigConfigSpecSlice

	for _, vgpuSpec := range s {
		migSpec := migpartedv1.MigConfigSpec{
			DeviceFilter: vgpuSpec.DeviceFilter,
			Devices:      vgpuSpec.Devices,
			MigDevices:   make(migtypes.MigConfig),
		}

		migEnabled := false
		for vgpuType := range vgpuSpec.VGPUDevices {
			vgpu, err := types.ParseVGPUType(vgpuType)
			if err != nil {
				return nil, fmt.Errorf("failed to parse vGPU type %s: %w", vgpuType, err)
			}

			if vgpu.G > 0 {
				migEnabled = true
				migProfile := fmt.Sprintf("%dg.%dgb", vgpu.G, vgpu.GB)
				for _, attr := range vgpu.Attr {
					if attr == types.AttributeMediaExtensions {
						migProfile += ".me"
						break
					}
				}
				migSpec.MigDevices[migProfile] = vgpuSpec.VGPUDevices[vgpuType]
			}
		}

		migSpec.MigEnabled = migEnabled

		migConfigSpecs = append(migConfigSpecs, migSpec)
	}

	return migConfigSpecs, nil
}

func containsKey(m map[string]json.RawMessage, s string) bool {
	_, exists := m[s]
	return exists
}
