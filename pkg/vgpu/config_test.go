/*
 * Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

package vgpu

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

func TestSanitizeVGPUConfig(t *testing.T) {
	// The GPU supports the base MIG-backed type and one suffixed
	// time-sliced type, mirroring boards where MIG-backed vGPU type names
	// carry no attribute suffix (e.g. RTX Pro 6000 Blackwell).
	supported := map[string]bool{
		"DC-1-24Q":   true,
		"DC-24QGFX":  true,
		"A100-1-5C":  true,
		"A100-2-10C": true,
	}
	isSupported := func(vgpuType string) bool { return supported[vgpuType] }

	testCases := []struct {
		description string
		config      types.VGPUConfig
		expected    types.VGPUConfig
		expectError string
	}{
		{
			description: "Exactly supported types are kept",
			config:      types.VGPUConfig{"A100-1-5C": 2, "A100-2-10C": 1},
			expected:    types.VGPUConfig{"A100-1-5C": 2, "A100-2-10C": 1},
		},
		{
			description: "Suffixed type is mapped to the supported base type",
			config:      types.VGPUConfig{"DC-1-24QGFX": 2},
			expected:    types.VGPUConfig{"DC-1-24Q": 2},
		},
		{
			description: "Exactly supported suffixed type is not stripped",
			config:      types.VGPUConfig{"DC-24QGFX": 1},
			expected:    types.VGPUConfig{"DC-24QGFX": 1},
		},
		{
			description: "Unsupported type is rejected",
			config:      types.VGPUConfig{"T4-16Q": 1},
			expectError: "not supported",
		},
		{
			description: "Types colliding after suffix stripping are rejected",
			config:      types.VGPUConfig{"DC-1-24QGFX": 2, "DC-1-24Q": 3},
			expectError: "map to the same supported vGPU type",
		},
		{
			description: "Empty config stays empty",
			config:      types.VGPUConfig{},
			expected:    types.VGPUConfig{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result, err := sanitizeVGPUConfig(tc.config, isSupported)
			if tc.expectError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectError)
				return
			}
			require.NoError(t, err)
			require.True(t, result.Equals(tc.expected), "got %v, expected %v", result, tc.expected)
		})
	}
}

// TestStripVGPUConfigSuffix verifies that the helper delegates to
// types.StripAttributeSuffix, whose contract is covered in detail by the
// tests in the types package.
func TestStripVGPUConfigSuffix(t *testing.T) {
	testCases := []struct {
		description string
		input       string
		expected    string
	}{
		{
			"No suffix - time-sliced vGPU type",
			"A100-5C",
			"A100-5C",
		},
		{
			"GFX suffix",
			"A100-1-5CGFX",
			"A100-1-5C",
		},
		{
			"MEALL suffix (should be stripped instead of ME)",
			"A100-7-40CMEALL",
			"A100-7-40C",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result := stripVGPUConfigSuffix(tc.input)
			require.Equal(t, tc.expected, result, "stripVGPUConfigSuffix(%q) = %q, expected %q", tc.input, result, tc.expected)
		})
	}
}
