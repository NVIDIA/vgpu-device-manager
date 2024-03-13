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

	"github.com/sirupsen/logrus"
	cli "github.com/urfave/cli/v2"

	"github.com/NVIDIA/vgpu-device-manager/cmd/nvidia-vgpu-dm/assert"
)

var log = logrus.New()

// GetLogger returns the logger for the 'apply' command
func GetLogger() *logrus.Logger {
	return log
}

// Flags for the 'apply' command
type Flags struct {
	assert.Flags
}

// Context containing CLI flags and the selected VGPUConfig to apply
type Context struct {
	assert.Context
	Flags *Flags
}

// BuildCommand builds the 'apply' command
func BuildCommand() *cli.Command {
	applyFlags := Flags{}

	apply := cli.Command{}
	apply.Name = "apply"
	apply.Usage = "Apply changes (if necessary) for a specific vGPU device configuration from a configuration file"
	apply.Action = func(c *cli.Context) error {
		return applyWrapper(c, &applyFlags)
	}

	apply.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:        "config-file",
			Aliases:     []string{"f"},
			Usage:       "Path to the configuration file",
			Destination: &applyFlags.ConfigFile,
			EnvVars:     []string{"VGPU_DM_CONFIG_FILE"},
		},
		&cli.StringFlag{
			Name:        "selected-config",
			Aliases:     []string{"c"},
			Usage:       "The label of the vgpu-config from the config file to apply to the node",
			Destination: &applyFlags.SelectedConfig,
			EnvVars:     []string{"VGPU_DM_SELECTED_CONFIG"},
		},
	}

	return &apply
}

// CheckFlags ensures that any required flags are provided and ensures they are well-formed.
func CheckFlags(f *Flags) error {
	return assert.CheckFlags(&f.Flags)
}

// AssertVGPUConfig reuses calls from the 'assert' subcommand to check if the vGPU devices of a particular vGPU config are currently applied.
// The 'VGPUConfig' being checked is embedded in the 'Context' struct itself.
func (c *Context) AssertVGPUConfig() error {
	return assert.VGPUConfig(&c.Context)
}

// ApplyVGPUConfig applies a particular vGPU config to the node.
// The 'VGPUConfig' being applied is embedded in the 'Context' struct itself.
func (c *Context) ApplyVGPUConfig() error {
	return VGPUConfig(c)
}

func applyWrapper(c *cli.Context, f *Flags) error {
	err := CheckFlags(f)
	if err != nil {
		_ = cli.ShowSubcommandHelp(c)
		return err
	}

	log.Debugf("Parsing config file...")
	spec, err := assert.ParseConfigFile(&f.Flags)
	if err != nil {
		return fmt.Errorf("error parsing config file: %v", err)
	}

	log.Debugf("Selecting specific vGPU config...")
	vgpuConfig, err := assert.GetSelectedVGPUConfig(&f.Flags, spec)
	if err != nil {
		return fmt.Errorf("error selecting VGPU config: %v", err)
	}

	if f.ValidConfig {
		log.Infof("Selected vGPU device configuration is valid")
		return nil
	}

	context := Context{
		Flags: f,
		Context: assert.Context{
			Context:    c,
			Flags:      &f.Flags,
			VGPUConfig: vgpuConfig,
		},
	}

	log.Debugf("Checking current vGPU device configuration...")
	err = context.AssertVGPUConfig()
	if err != nil {
		log.Infof("Applying vGPU device configuration...")
		err := context.ApplyVGPUConfig()
		if err != nil {
			return err
		}
	}

	log.Infof("Selected vGPU device configuration successfully applied")
	return nil
}
