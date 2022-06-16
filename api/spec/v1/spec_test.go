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
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
	"testing"
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
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					]
				}
			}`,
			false,
		},
		{
			"Well formed - multiple 'vgpu-configs'",
			`{
				"version": "v1",
				"vgpu-configs": {
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					],
					"a100-full-profile": [
						"A100-24C",
					]
				}
			}`,
			false,
		},
		{
			"Well formed - wrong version",
			`{
				"version": "v2",
				"vgpu-configs": {
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					]
				}
			}`,
			true,
		},
		{
			"Missing version",
			`{
				"vgpu-configs": {
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					]
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
					"default": [
						"A100-4C",
						"A30-4C",
						"T4-1Q",
					]
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
