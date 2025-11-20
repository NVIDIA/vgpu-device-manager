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
	return assert.WalkSelectedVGPUConfigForEachGPU(c.VGPUConfig, func(vc *v1.VGPUConfigSpec, i int, d types.DeviceID) error {
		configManager := vgpu.NewNvlibVGPUConfigManager()
		current, err := configManager.GetVGPUConfig(i)
		if err != nil {
			return fmt.Errorf("error getting vGPU config: %v", err)
		}

		if current.Equals(vc.VGPUDevices) {
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
