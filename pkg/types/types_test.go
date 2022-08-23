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

package types

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestParseVGPUType(t *testing.T) {
	testCases := []struct {
		description string
		device      string
		valid       bool
	}{
		{
			"Empty device type",
			"",
			false,
		},
		{
			"Valid Q-Series (A16-8Q)",
			"A16-8Q",
			true,
		},
		{
			"Valid A-Series (A16-8A)",
			"A16-8A",
			true,
		},
		{
			"Valid B-Series (A16-8B)",
			"A16-8B",
			true,
		},
		{
			"Valid C-Series (A16-8C)",
			"A16-8C",
			true,
		},
		{
			"Invalid series",
			"A16-8E",
			false,
		},
		{
			"Valid A100-5C",
			"A100-5C",
			true,
		},
		{
			"Invalid ' A100-5C'",
			" A100-5C",
			false,
		},
		{
			"Invalid 'A100 -5C'",
			"A100 -5C",
			false,
		},
		{
			"Invalid 'A100- 5C'",
			"A100- 5C",
			false,
		},
		{
			"Invalid 'A100-5C '",
			"A100-5C ",
			false,
		},
		{
			"Valid A100-1-5C",
			"A100-1-5C",
			true,
		},
		{
			"Invalid ' A100-1-5C'",
			" A100-1-5C",
			false,
		},
		{
			"Invalid 'A100 -1-5C'",
			"A100 -1-5C",
			false,
		},
		{
			"Invalid 'A100- 1-5C'",
			"A100- 1-5C",
			false,
		},
		{
			"Invalid 'A100-1 -5C'",
			"A100-1 -5C",
			false,
		},
		{
			"Invalid 'A100-1- 5C'",
			"A100-1- 5C",
			false,
		},
		{
			"Invalid 'A100-1-5 C'",
			"A100-1-5 C",
			false,
		},
		{
			"Invalid 'A100-1-5C '",
			"A100-1-5C ",
			false,
		},
		{
			"Valid A100-1-5CME",
			"A100-1-5CME",
			true,
		},
		{
			"Invalid 'A100-1-5Cme'",
			"A100-1-5Cme",
			false,
		},
		{
			"Invalid 'A100-1-5C ME'",
			"A100-1-5C Me",
			false,
		},
		{
			"Invalid 'A100-1-5Cab'",
			"A100-1-5Cab",
			false,
		},
		{
			"Invalid bogus",
			"bogus",
			false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			_, err := ParseVGPUType(tc.device)
			if tc.valid {
				require.Nil(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestVGPUConfigAssertValid(t *testing.T) {
	testCases := []struct {
		description string
		config      VGPUConfig
		valid       bool
	}{
		{
			"Empty config",
			map[string]int{},
			true,
		},
		{
			"Valid config - one entry",
			map[string]int{
				"A100-5C": 1,
			},
			true,
		},
		{
			"Valid config - multiple entries",
			map[string]int{
				"A100-5C": 1,
				"A100-8C": 1,
			},
			true,
		},
		{
			"Invalid config - invalid count",
			map[string]int{
				"A100-5C": -1,
			},
			false,
		},
		{
			"Invalid config - all zero counts",
			map[string]int{
				"A100-5C": 0,
				"A100-8C": 0,
			},
			false,
		},
		{
			"Invalid config - malformed vGPU type",
			map[string]int{
				"A100-5c": 1,
			},
			false,
		},
		{
			"Invalid config - both time-sliced and MIG-back devices",
			map[string]int{
				"A100-5C":   1,
				"A100-1-5C": 1,
			},
			false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			err := tc.config.AssertValid()
			if tc.valid {
				require.Nil(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
