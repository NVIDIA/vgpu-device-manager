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
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/NVIDIA/go-nvlib/pkg/nvpci"
	"github.com/sirupsen/logrus"
	cli "github.com/urfave/cli/v2"
	v1 "gitlab.com/nvidia/cloud-native/vgpu-device-manager/api/spec/v1"
	"gitlab.com/nvidia/cloud-native/vgpu-device-manager/pkg/types"
	"sigs.k8s.io/yaml"
)

var log = logrus.New()

// GetLogger returns the logger for the 'assert' command
func GetLogger() *logrus.Logger {
	return log
}

// Flags for the 'assert' command
type Flags struct {
	ConfigFile     string
	SelectedConfig string
	ValidConfig    bool
}

// Context containing CLI flags and the selected VGPUConfig to assert
type Context struct {
	*cli.Context
	Flags      *Flags
	VGPUConfig v1.VGPUConfigSpecSlice
}

// BuildCommand builds the 'assert' command
func BuildCommand() *cli.Command {
	assertFlags := Flags{}

	assert := cli.Command{}
	assert.Name = "assert"
	assert.Usage = "Assert that a specific vGPU device configuration is currently applied to the node"
	assert.Action = func(c *cli.Context) error {
		return assertWrapper(c, &assertFlags)
	}

	assert.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:        "config-file",
			Aliases:     []string{"f"},
			Usage:       "Path to the configuration file",
			Destination: &assertFlags.ConfigFile,
			EnvVars:     []string{"VGPU_DM_CONFIG_FILE"},
		},
		&cli.StringFlag{
			Name:        "selected-config",
			Aliases:     []string{"c"},
			Usage:       "The name of the vgpu-config from the config file to assert is applied to the node",
			Destination: &assertFlags.SelectedConfig,
			EnvVars:     []string{"VGPU_DM_SELECTED_CONFIG"},
		},
		&cli.BoolFlag{
			Name:        "valid-config",
			Aliases:     []string{"a"},
			Usage:       "Only assert that the config file is valid and the selected config is present in it",
			Destination: &assertFlags.ValidConfig,
			EnvVars:     []string{"VGPU_DM_VALID_CONFIG"},
		},
	}

	return &assert
}

func assertWrapper(c *cli.Context, f *Flags) error {
	err := CheckFlags(f)
	if err != nil {
		cli.ShowSubcommandHelp(c)
		return err
	}

	log.Debugf("Parsing config file...")
	spec, err := ParseConfigFile(f)
	if err != nil {
		return fmt.Errorf("error parsing config file: %v", err)
	}

	log.Debugf("Selecting specific vGPU config...")
	vgpuConfig, err := GetSelectedVGPUConfig(f, spec)
	if err != nil {
		return fmt.Errorf("error selecting VGPU config: %v", err)
	}

	if f.ValidConfig {
		log.Infof("Selected vGPU device configuration is valid")
		return nil
	}

	context := Context{
		Context:    c,
		Flags:      f,
		VGPUConfig: vgpuConfig,
	}

	log.Debugf("Asserting vGPU device configuration...")
	err = VGPUConfig(&context)
	if err != nil {
		log.Debug(err.Error())
		return fmt.Errorf("Assertion failure: selected configuration not currently applied")
	}

	log.Infof("Selected vGPU device configuration is currently applied")
	return nil
}

// CheckFlags ensures that any required flags are provided and ensures they are well-formed.
func CheckFlags(f *Flags) error {
	var missing []string
	if f.ConfigFile == "" {
		missing = append(missing, "config-file")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required flags '%v'", strings.Join(missing, ", "))
	}
	return nil
}

// ParseConfigFile parses the vGPU device configuration file
func ParseConfigFile(f *Flags) (*v1.Spec, error) {
	var err error
	var configYaml []byte

	if f.ConfigFile == "-" {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			configYaml = append(configYaml, scanner.Bytes()...)
			configYaml = append(configYaml, '\n')
		}
	} else {
		configYaml, err = os.ReadFile(f.ConfigFile)
		if err != nil {
			return nil, fmt.Errorf("read error: %v", err)
		}
	}

	var spec v1.Spec
	err = yaml.Unmarshal(configYaml, &spec)
	if err != nil {
		return nil, fmt.Errorf("unmarshal error: %v", err)
	}

	return &spec, nil
}

// GetSelectedVGPUConfig gets the selected VGPUConfigSpecSlice from the config file
func GetSelectedVGPUConfig(f *Flags, spec *v1.Spec) (v1.VGPUConfigSpecSlice, error) {
	if len(spec.VGPUConfigs) > 1 && f.SelectedConfig == "" {
		return nil, fmt.Errorf("missing required flag 'selected-config' when more than one config available")
	}

	if len(spec.VGPUConfigs) == 1 && f.SelectedConfig == "" {
		for c := range spec.VGPUConfigs {
			f.SelectedConfig = c
		}
	}

	if _, exists := spec.VGPUConfigs[f.SelectedConfig]; !exists {
		return nil, fmt.Errorf("selected vgpu-config not present: %v", f.SelectedConfig)
	}

	return spec.VGPUConfigs[f.SelectedConfig], nil
}

// WalkSelectedVGPUConfigForEachGPU applies a function 'f' to the selected 'VGPUConfig' for each GPU on the node
func WalkSelectedVGPUConfigForEachGPU(vgpuConfig v1.VGPUConfigSpecSlice, f func(*v1.VGPUConfigSpec, int, types.DeviceID) error) error {
	nvpci := nvpci.New()
	gpus, err := nvpci.GetGPUs()
	if err != nil {
		return fmt.Errorf("error enumerating GPUs: %v", err)
	}

	for _, vc := range vgpuConfig {
		if vc.DeviceFilter == nil {
			log.Debugf("Walking VGPUConfig for (devices=%v)", vc.Devices)
		} else {
			log.Debugf("Walking VGPUConfig for (device-filter=%v, devices=%v)", vc.DeviceFilter, vc.Devices)
		}

		for i, gpu := range gpus {
			deviceID := types.NewDeviceID(gpu.Device, gpu.Vendor)

			if !vc.MatchesDeviceFilter(deviceID) {
				continue
			}

			if !vc.MatchesDevices(i) {
				continue
			}

			log.Debugf("  GPU %v: %v", i, deviceID)

			err = f(&vc, i, deviceID)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
