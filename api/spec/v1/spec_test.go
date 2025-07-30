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
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	migpartedv1 "github.com/NVIDIA/mig-parted/api/spec/v1"
	migtypes "github.com/NVIDIA/mig-parted/pkg/types"

	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

func TestSpec(t *testing.T) {
	testCases := []struct {
		Description     string
		Spec            string
		expectedFailure bool
	}{
		{
			"Empty",
			"",
			false,
		},
		{
			"Only version field",
			`{
				"version": "v1"
			}`,
			false,
		},
		{
			"Well formed",
			`{
				"version": "v1",
				"vgpu-configs": {
					"all-a100-4c": [{
						"devices": "all",
						"vgpu-devices": {
							"A100-4C": 10
						}
					}]
				}
			}`,
			false,
		},
		{
			"Well formed - multiple 'vgpu-configs'",
			`{
				"version": "v1",
				"vgpu-configs": {
					"all-a100-4c": [{
						"devices": "all",
						"vgpu-devices": {
							"A100-4C": 10
						}
					}],
					"all-a100-5c": [{
						"devices": "all",
						"vgpu-devices": {
							"A100-5C": 8
						}
					}]
				}
			}`,
			false,
		},
		{
			"Well formed - wrong version",
			`{
				"version": "v2",
				"vgpu-configs": {
					"all-a100-4c": [{
						"devices": "all",
						"vgpu-devices": {
							"A100-4C": 10
						}
					}]
				}
			}`,
			true,
		},
		{
			"Missing version",
			`{
				"vgpu-configs": {
					"all-a100-4c": [{
						"devices": "all",
						"vgpu-devices": {
							"A100-4C": 10
						}
					}]
				}
			}`,
			true,
		},
		{
			"Erroneous field",
			`{
				"bogus": "field",
				"version": "v1",
				"vgpu-configs": {
					"all-a100-4c": [{
						"devices": "all",
						"vgpu-devices": {
							"A100-4C": 10
						}
					}]
				}
			}`,
			true,
		},
		{
			"Empty 'vgu-configs'",
			`{
				"version": "v1",
				"vgpu-configs": {}
			}`,
			true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Description, func(t *testing.T) {
			s := Spec{}
			err := yaml.Unmarshal([]byte(tc.Spec), &s)
			if tc.expectedFailure {
				require.NotNil(t, err, "Unexpected success yaml.Unmarshal")
			} else {
				require.Nil(t, err, "Unexpected failure yaml.Unmarshal")
			}
		})
	}
}

func TestVGPUConfigSpec(t *testing.T) {
	testCases := []struct {
		Description     string
		VGPUConfigSpec  string
		expectedFailure bool
	}{
		{
			"Empty",
			"",
			false,
		},
		{
			"Well formed",
			`{
				"devices": "all",
				"vgpu-devices": {
					"A100-4C": 10
				}
			}`,
			false,
		},
		{
			"Well formed with multiple vGPU types",
			`{
				"devices": "all",
				"vgpu-devices": {
					"A100-4C": 5,
					"A100-5C": 4
				}
			}`,
			false,
		},
		{
			"Well formed with filter",
			`{
				"device-filter": "MODEL",
				"devices": "all",
				"vgpu-devices": {
					"A100-4C": 10
				}
			}`,
			false,
		},
		{
			"Erroneous field",
			`{
				"bogus": "field",
				"devices": "all",
				"vgpu-devices": {
					"A100-4C": 10
				}
			}`,
			true,
		},
		{
			"Missing 'devices'",
			`{
				"vgpu-devices": {
					"A100-4C": 10
				}
			}`,
			true,
		},
		{
			"Missing 'vgpu-devices'",
			`{
				"devices": "all"
			}`,
			true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Description, func(t *testing.T) {
			s := VGPUConfigSpec{}
			err := yaml.Unmarshal([]byte(tc.VGPUConfigSpec), &s)
			if tc.expectedFailure {
				require.NotNil(t, err, "Unexpected success yaml.Unmarshal")
			} else {
				require.Nil(t, err, "Unexpected failure yaml.Unmarshal")
			}
		})
	}

}

func TestVGPUConfigSpecSliceToMigConfigSpecSlice(t *testing.T) {
	testCases := []struct {
		Description           string
		VGPUConfigSpecSlice   VGPUConfigSpecSlice
		ExpectedMigConfigSpec migpartedv1.MigConfigSpecSlice
		ExpectedError         string
	}{
		{
			"Empty slice",
			VGPUConfigSpecSlice{},
			nil,
			"",
		},
		{
			"Single MIG-backed vGPU type",
			VGPUConfigSpecSlice{
				{
					DeviceFilter: "MODEL",
					Devices:      "all",
					VGPUDevices: types.VGPUConfig{
						"A100-1-5C": 4,
					},
				},
			},
			migpartedv1.MigConfigSpecSlice{
				{
					DeviceFilter: "MODEL",
					Devices:      "all",
					MigEnabled:   true,
					MigDevices: migtypes.MigConfig{
						"1g.5gb": 4,
					},
				},
			},
			"",
		},
		{
			"Multiple MIG-backed vGPU types",
			VGPUConfigSpecSlice{
				{
					DeviceFilter: []string{"MODEL1", "MODEL2"},
					Devices:      []int{0, 1},
					VGPUDevices: types.VGPUConfig{
						"A100-1-5C":  2,
						"A100-2-10C": 1,
					},
				},
			},
			migpartedv1.MigConfigSpecSlice{
				{
					DeviceFilter: []string{"MODEL1", "MODEL2"},
					Devices:      []int{0, 1},
					MigEnabled:   true,
					MigDevices: migtypes.MigConfig{
						"1g.5gb":  2,
						"2g.10gb": 1,
					},
				},
			},
			"",
		},
		{
			"MIG-backed vGPU type with media extensions",
			VGPUConfigSpecSlice{
				{
					Devices: "all",
					VGPUDevices: types.VGPUConfig{
						"A100-1-5CME": 2,
					},
				},
			},
			migpartedv1.MigConfigSpecSlice{
				{
					Devices:    "all",
					MigEnabled: true,
					MigDevices: migtypes.MigConfig{
						"1g.5gb+me": 2,
					},
				},
			},
			"",
		},
		{
			"MIG-backed vGPU type with no media extensions",
			VGPUConfigSpecSlice{
				{
					Devices: "all",
					VGPUDevices: types.VGPUConfig{
						"A100-1-5CNOME": 2,
					},
				},
			},
			migpartedv1.MigConfigSpecSlice{
				{
					Devices:    "all",
					MigEnabled: true,
					MigDevices: migtypes.MigConfig{
						"1g.5gb-me": 2,
					},
				},
			},
			"",
		},
		{
			"MIG-backed vGPU type with all media extensions",
			VGPUConfigSpecSlice{
				{
					Devices: "all",
					VGPUDevices: types.VGPUConfig{
						"A100-1-5CMEALL": 2,
					},
				},
			},
			migpartedv1.MigConfigSpecSlice{
				{
					Devices:    "all",
					MigEnabled: true,
					MigDevices: migtypes.MigConfig{
						"1g.5gb+me.all": 2,
					},
				},
			},
			"",
		},
		{
			"MIG-backed vGPU type with graphics extensions",
			VGPUConfigSpecSlice{
				{
					Devices: "all",
					VGPUDevices: types.VGPUConfig{
						"A100-1-5CGFX": 2,
					},
				},
			},
			migpartedv1.MigConfigSpecSlice{
				{
					Devices:    "all",
					MigEnabled: true,
					MigDevices: migtypes.MigConfig{
						"1g.5gb+gfx": 2,
					},
				},
			},
			"",
		},
		{
			"Non-MIG vGPU type",
			VGPUConfigSpecSlice{
				{
					Devices: "all",
					VGPUDevices: types.VGPUConfig{
						"A100-40C": 2,
					},
				},
			},
			migpartedv1.MigConfigSpecSlice{
				{
					Devices:    "all",
					MigEnabled: false,
					MigDevices: migtypes.MigConfig{},
				},
			},
			"",
		},
		{
			"Mixed MIG and non-MIG vGPU types",
			VGPUConfigSpecSlice{
				{
					Devices: "all",
					VGPUDevices: types.VGPUConfig{
						"A100-40C":  1,
						"A100-1-5C": 2,
					},
				},
			},
			migpartedv1.MigConfigSpecSlice{
				{
					Devices:    "all",
					MigEnabled: true,
					MigDevices: migtypes.MigConfig{
						"1g.5gb": 2,
					},
				},
			},
			"",
		},
		{
			"Multiple specs with different configurations",
			VGPUConfigSpecSlice{
				{
					Devices: "all",
					VGPUDevices: types.VGPUConfig{
						"A100-1-5C": 4,
					},
				},
				{
					DeviceFilter: "MODEL",
					Devices:      []int{0, 1},
					VGPUDevices: types.VGPUConfig{
						"A100-40C": 1,
					},
				},
			},
			migpartedv1.MigConfigSpecSlice{
				{
					Devices:    "all",
					MigEnabled: true,
					MigDevices: migtypes.MigConfig{
						"1g.5gb": 4,
					},
				},
				{
					DeviceFilter: "MODEL",
					Devices:      []int{0, 1},
					MigEnabled:   false,
					MigDevices:   migtypes.MigConfig{},
				},
			},
			"",
		},
		{
			"Invalid vGPU type",
			VGPUConfigSpecSlice{
				{
					Devices: "all",
					VGPUDevices: types.VGPUConfig{
						"InvalidType": 1,
					},
				},
			},
			nil,
			"failed to parse vGPU type InvalidType:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Description, func(t *testing.T) {
			result, err := tc.VGPUConfigSpecSlice.ToMigConfigSpecSlice()
			if tc.ExpectedError != "" {
				require.NotNil(t, err, "Expected failure but got success")
				require.Nil(t, result, "Expected nil result on failure")
				require.ErrorContains(t, err, tc.ExpectedError)
			} else {
				require.Nil(t, err, "Unexpected failure: %v", err)
				require.Equal(t, tc.ExpectedMigConfigSpec, result, "Unexpected result")
			}
		})
	}
}
