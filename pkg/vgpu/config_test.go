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
)

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
			"No suffix - MIG-backed vGPU type",
			"A100-1-5C",
			"A100-1-5C",
		},
		{
			"ME suffix",
			"A100-1-5CME",
			"A100-1-5C",
		},
		{
			"NOME suffix",
			"A100-1-5CNOME",
			"A100-1-5C",
		},
		{
			"MEALL suffix",
			"A100-1-5CMEALL",
			"A100-1-5C",
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
		{
			"Empty string",
			"",
			"",
		},
		{
			"String with ME in middle (should not be stripped)",
			"A100ME-5C",
			"A100ME-5C",
		},
		{
			"String with NOME in middle (should not be stripped)",
			"A100NOME-5C",
			"A100NOME-5C",
		},
		{
			"String with MEALL in middle (should not be stripped)",
			"A100MEALL-5C",
			"A100MEALL-5C",
		},
		{
			"String with GFX in middle (should not be stripped)",
			"A100GFX-5C",
			"A100GFX-5C",
		},
		{
			"RTX Ada series with GFX suffix",
			"RTX6000-Ada-2QGFX",
			"RTX6000-Ada-2Q",
		},
		{
			"RTX Ada series without suffix",
			"RTX6000-Ada-2Q",
			"RTX6000-Ada-2Q",
		},
		{
			"H100 with ME suffix",
			"H100-1-20CME",
			"H100-1-20C",
		},
		{
			"Multiple valid suffixes in name - only last one stripped",
			"A100-1-5CNOMEME",
			"A100-1-5CNOME",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result := stripVGPUConfigSuffix(tc.input)
			require.Equal(t, tc.expected, result, "stripVGPUConfigSuffix(%q) = %q, expected %q", tc.input, result, tc.expected)
		})
	}
}
