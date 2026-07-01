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
