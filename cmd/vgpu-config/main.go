/*
 * Copyright (c) NVIDIA CORPORATION.  All rights reserved.
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
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	cli "github.com/urfave/cli/v2"
)

type flags struct {
	xmlFile    string
	outputFile string
}

func main() {
	flags := flags{}

	c := cli.NewApp()
	c.Name = "vgpu-config"
	c.Usage = "Manage configuration files for NVIDIA vGPU Device Manager"
	c.Version = "0.1.0"

	generate := cli.Command{}
	generate.Name = "generate"
	generate.Usage = "Generate a vGPU device configuration file from an xml file (vgpuConfig.xml)"
	generate.Before = func(c *cli.Context) error {
		return validateFlags(&flags)
	}
	generate.Action = func(c *cli.Context) error {
		return Generate(c, &flags)
	}

	// Register the subcommand with the top-level CLI
	c.Commands = []*cli.Command{
		&generate,
	}

	generate.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:        "xml-file",
			Aliases:     []string{"f"},
			Usage:       "Path to the xml file",
			Required:    true,
			Destination: &flags.xmlFile,
			EnvVars:     []string{"XML_FILE"},
		},
		&cli.StringFlag{
			Name:        "output-file",
			Aliases:     []string{"o"},
			Required:    true,
			Usage:       "Path to the output file",
			Destination: &flags.outputFile,
			EnvVars:     []string{"OUTPUT_FILE"},
		},
	}

	if err := c.Run(os.Args); err != nil {
		log.Fatal(fmt.Errorf("error: %v", err))
	}
}

func validateFlags(f *flags) error {
	if f.xmlFile == "" {
		return fmt.Errorf("invalid --xml-file option: %v", f.xmlFile)
	}
	if f.outputFile == "" {
		return fmt.Errorf("invalid --output-file option: %v", f.outputFile)
	}

	return nil
}
