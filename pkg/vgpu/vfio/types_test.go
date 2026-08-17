/*
 * Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

package vfio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCreatableVGPUTypes(t *testing.T) {
	testCases := []struct {
		description string
		content     string
		expected    []vgpuType
	}{
		{
			"Empty file",
			"",
			nil,
		},
		{
			"Single whole-card type",
			"1428 : NVIDIA H200X-141C\n",
			[]vgpuType{
				{ID: 1428, Name: "NVIDIA H200X-141C"},
			},
		},
		{
			"MIG-backed and whole-card types",
			"1414 : NVIDIA H200X-1-18C\n1428 : NVIDIA H200X-141C\n",
			[]vgpuType{
				{ID: 1414, Name: "NVIDIA H200X-1-18C"},
				{ID: 1428, Name: "NVIDIA H200X-141C"},
			},
		},
		{
			"Extra whitespace around ID and name",
			"  1414\t:   NVIDIA H200X-1-18C  \n",
			[]vgpuType{
				{ID: 1414, Name: "NVIDIA H200X-1-18C"},
			},
		},
		{
			"Blank lines are skipped",
			"\n1414 : NVIDIA H200X-1-18C\n\n\n1428 : NVIDIA H200X-141C\n\n",
			[]vgpuType{
				{ID: 1414, Name: "NVIDIA H200X-1-18C"},
				{ID: 1428, Name: "NVIDIA H200X-141C"},
			},
		},
		{
			"Whitespace-separated format without colon",
			"1414 NVIDIA H200X-1-18C\n",
			[]vgpuType{
				{ID: 1414, Name: "NVIDIA H200X-1-18C"},
			},
		},
		{
			"Header or non-numeric lines are skipped",
			"ID : vGPU type\n1414 : NVIDIA H200X-1-18C\n",
			[]vgpuType{
				{ID: 1414, Name: "NVIDIA H200X-1-18C"},
			},
		},
		{
			"Line with ID but no name is skipped",
			"1414 :\n1428 : NVIDIA H200X-141C\n",
			[]vgpuType{
				{ID: 1428, Name: "NVIDIA H200X-141C"},
			},
		},
		{
			"Older style names with GRID prefix",
			"63 : GRID P40-2Q\n",
			[]vgpuType{
				{ID: 63, Name: "GRID P40-2Q"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result := parseCreatableVGPUTypes(tc.content)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestVGPUTypeShortName(t *testing.T) {
	testCases := []struct {
		description string
		name        string
		expected    string
	}{
		{
			"NVIDIA prefixed name",
			"NVIDIA H200X-1-18C",
			"H200X-1-18C",
		},
		{
			"GRID prefixed name",
			"GRID P40-2Q",
			"P40-2Q",
		},
		{
			"Name without prefix",
			"A100-40C",
			"A100-40C",
		},
		{
			"Multi-word product name",
			"NVIDIA RTX6000-Ada-2Q",
			"RTX6000-Ada-2Q",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			v := vgpuType{ID: 1, Name: tc.name}
			require.Equal(t, tc.expected, v.shortName())
		})
	}
}
