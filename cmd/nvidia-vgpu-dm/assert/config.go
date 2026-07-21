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

package assert

import (
	"fmt"

	"github.com/NVIDIA/go-nvlib/pkg/nvpci"

	v1 "github.com/NVIDIA/vgpu-device-manager/api/spec/v1"
	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
	"github.com/NVIDIA/vgpu-device-manager/pkg/vgpu"
)

// VGPUConfig asserts that the selected vGPU config is applied to the node
func VGPUConfig(c *Context) error {
	nvpci := nvpci.New()
	gpus, err := nvpci.GetGPUs()
	if err != nil {
		return fmt.Errorf("error enumerating GPUs: %v", err)
	}

	matched := make([]bool, len(gpus))
	configManager := vgpu.NewVGPUConfigManager("")
	err = WalkSelectedVGPUConfigForEachGPU(c.VGPUConfig, func(vc *v1.VGPUConfigSpec, i int, d types.DeviceID) error {
		current, err := configManager.GetVGPUConfig(i)
		if err != nil {
			return fmt.Errorf("error getting vGPU config: %v", err)
		}

		// GetVGPUConfig reports vGPU type names as the driver knows them,
		// which may differ from the names in the config file by a MIG
		// attribute suffix. Compare against the normalized config.
		desired := vc.VGPUDevices
		if normalized, err := configManager.NormalizeVGPUConfig(i, desired); err == nil {
			desired = normalized
		} else {
			log.Debugf("    Unable to normalize the desired vGPU config, comparing as-is: %v", err)
		}

		log.Debugf("    Asserting vGPU config: %v", desired)
		if current.Equals(desired) {
			log.Debugf("    Skipping -- already set to desired value")
			matched[i] = true
			return nil
		}

		matched[i] = false
		return nil
	})

	if err != nil {
		return err
	}

	for _, match := range matched {
		if !match {
			return fmt.Errorf("not all GPUs match the specified config")
		}
	}

	return nil
}
