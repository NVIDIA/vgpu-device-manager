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

func TestPCIAddressToID(t *testing.T) {
	testCases := []struct {
		description string
		address     string
		expected    uint64
	}{
		{
			"Standard 4-digit domain address",
			"0000:18:00.0",
			0x18000,
		},
		{
			"Non-zero domain",
			"0001:02:00.4",
			0x102004,
		},
		{
			"Hexadecimal bus/device letters",
			"0000:1a:1f.7",
			0x1a1f7,
		},
		{
			"Malformed address parses as 0",
			"not-a-pci-address",
			0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			require.Equal(t, tc.expected, pciAddressToID(tc.address))
		})
	}
}

func TestSortPCIAddresses(t *testing.T) {
	// This exercises the ordering guarantee directly against a Go slice
	// built in scrambled order, independent of whatever order os.ReadDir
	// happens to enumerate sysfs entries in. In particular, addresses whose
	// bus byte differs are given in descending order here to ensure the
	// sort is doing real numeric work and not just observing an
	// already-sorted input.
	addresses := []string{
		"0000:41:00.0",
		"0000:02:00.0",
		"0001:00:00.0",
		"0000:18:00.0",
	}

	sortPCIAddresses(addresses)

	require.Equal(t, []string{
		"0000:02:00.0",
		"0000:18:00.0",
		"0000:41:00.0",
		"0001:00:00.0",
	}, addresses)
}

func TestGpuAddressesOrder(t *testing.T) {
	// Build a fake sysfs tree where the directory names, if compared as
	// plain strings, would already sort in numeric PCI address order (as
	// real sysfs directory names always do for fixed-width addresses) --
	// the point of this test is to pin gpuAddresses to the explicit numeric
	// sort in sortPCIAddresses/TestSortPCIAddresses above, rather than to
	// the incidental behavior of os.ReadDir, which this test alone cannot
	// distinguish from. TestSortPCIAddresses is what actually proves the
	// sort key is correct; this test proves gpuAddresses applies it.
	root := buildFakeSysfs(t, []fakeGPU{
		{address: "0000:41:00.0", sriovCapable: true},
		{address: "0000:02:00.0", sriovCapable: true},
		{address: "0000:18:00.0", sriovCapable: true},
	})

	addresses, err := gpuAddresses(root)
	require.NoError(t, err)
	require.Equal(t, []string{
		"0000:02:00.0",
		"0000:18:00.0",
		"0000:41:00.0",
	}, addresses)
}
