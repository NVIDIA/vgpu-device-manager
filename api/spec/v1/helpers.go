/*
 * Copyright (c) 2021, NVIDIA CORPORATION.  All rights reserved.
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
	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

// MatchesDeviceFilter checks a 'VGPUConfigSpec' to see if its device filter matches the provided 'deviceID'.
func (vs *VGPUConfigSpec) MatchesDeviceFilter(deviceID types.DeviceID) bool {
	var deviceFilter []string
	switch df := vs.DeviceFilter.(type) {
	case string:
		if df != "" {
			deviceFilter = append(deviceFilter, df)
		}
	case []string:
		deviceFilter = df
	}

	if len(deviceFilter) == 0 {
		return true
	}

	for _, df := range deviceFilter {
		newDeviceID, _ := types.NewDeviceIDFromString(df)
		if newDeviceID == deviceID {
			return true
		}
	}

	return false
}

// MatchesAllDevices checks a 'VGPUConfigSpec' to see if it matches on 'all' devices.
func (vs *VGPUConfigSpec) MatchesAllDevices() bool {
	if devices, ok := vs.Devices.(string); ok {
		return devices == "all"
	}
	return false
}

// MatchesDevices checks a 'VGPUConfigSpec' to see if it matches on a device at the specified 'index'.
func (vs *VGPUConfigSpec) MatchesDevices(index int) bool {
	if devices, ok := vs.Devices.([]int); ok {
		for _, d := range devices {
			if index == d {
				return true
			}
		}
	}

	return vs.MatchesAllDevices()
}
