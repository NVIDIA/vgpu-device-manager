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
	"os"

	log "github.com/sirupsen/logrus"
	cli "github.com/urfave/cli/v2"

	"github.com/NVIDIA/vgpu-device-manager/cmd/nvidia-vgpu-dm/apply"
	"github.com/NVIDIA/vgpu-device-manager/cmd/nvidia-vgpu-dm/assert"
)

// Flags represents the top level flags that can be passed to the vgpu-dm CLI
type Flags struct {
	Debug bool
}

func main() {
	flags := Flags{}

	c := cli.NewApp()
	c.Name = "nvidia-vgpu-dm"
	c.Usage = "Manage NVIDIA vGPU devices"
	c.Version = "0.2.0"

	c.Flags = []cli.Flag{
		&cli.BoolFlag{
			Name:        "debug",
			Aliases:     []string{"d"},
			Usage:       "Enable debug-level logging",
			Destination: &flags.Debug,
			EnvVars:     []string{"VGPU_DM_DEBUG"},
		},
	}

	c.Commands = []*cli.Command{
		apply.BuildCommand(),
		assert.BuildCommand(),
	}

	c.Before = func(c *cli.Context) error {
		logLevel := log.InfoLevel
		if flags.Debug {
			logLevel = log.DebugLevel
		}
		assertLog := assert.GetLogger()
		assertLog.SetLevel(logLevel)
		applyLog := apply.GetLogger()
		applyLog.SetLevel(logLevel)
		return nil
	}

	err := c.Run(os.Args)
	if err != nil {
		log.Fatalf(err.Error())
	}
}
