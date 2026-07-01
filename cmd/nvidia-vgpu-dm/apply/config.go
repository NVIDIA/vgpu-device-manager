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

package apply

import (
	"fmt"

	v1 "github.com/NVIDIA/vgpu-device-manager/api/spec/v1"
	"github.com/NVIDIA/vgpu-device-manager/cmd/nvidia-vgpu-dm/assert"
	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
	"github.com/NVIDIA/vgpu-device-manager/pkg/vgpu"
)

// VGPUConfig applies the selected vGPU config to the node
func VGPUConfig(c *Context) error {
	configManager := vgpu.NewVGPUConfigManager(c.Flags.HostRootMount)
	return assert.WalkSelectedVGPUConfigForEachGPU(c.VGPUConfig, func(vc *v1.VGPUConfigSpec, i int, d types.DeviceID) error {
		current, err := configManager.GetVGPUConfig(i)
		if err != nil {
			return fmt.Errorf("error getting vGPU config: %v", err)
		}

		// GetVGPUConfig reports vGPU type names as the driver knows them,
		// which may differ from the names in the config file by a MIG
		// attribute suffix. Compare against the normalized config so that a
		// config that is already applied is not torn down and recreated.
		desired := vc.VGPUDevices
		if normalized, err := configManager.NormalizeVGPUConfig(i, desired); err == nil {
			desired = normalized
		} else {
			log.Debugf("    Unable to normalize the desired vGPU config, comparing as-is: %v", err)
		}

		if current.Equals(desired) {
			log.Debugf("    Skipping -- already set to desired value")
			return nil
		}

		log.Debugf("    Updating vGPU config: %v", vc.VGPUDevices)
		err = configManager.SetVGPUConfig(i, vc.VGPUDevices)
		if err != nil {
			return fmt.Errorf("error setting VGPU config: %v", err)
		}

		return nil
	})
}
