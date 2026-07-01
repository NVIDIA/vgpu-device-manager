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

package vgpu

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildMdevBus creates a fake /sys/class/mdev_bus directory, optionally
// containing a parent device entry.
func buildMdevBus(t *testing.T, parents ...string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "mdev_bus")
	require.NoError(t, os.MkdirAll(root, 0755))
	for _, parent := range parents {
		require.NoError(t, os.MkdirAll(filepath.Join(root, parent), 0755))
	}
	return root
}

// buildPCIDevices creates a fake /sys/bus/pci/devices directory. When
// vgpuCapable is true, it contains an NVIDIA GPU exposed as an SR-IOV
// physical function bound to the 'nvidia' driver.
func buildPCIDevices(t *testing.T, vgpuCapable bool) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "devices")
	require.NoError(t, os.MkdirAll(root, 0755))
	if !vgpuCapable {
		return root
	}

	require.NoError(t, os.MkdirAll(filepath.Join(root, "drivers", "nvidia"), 0755))
	pfDir := filepath.Join(root, "0000:18:00.0")
	require.NoError(t, os.MkdirAll(pfDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pfDir, "vendor"), []byte("0x10de\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(pfDir, "class"), []byte("0x030200\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(pfDir, "sriov_totalvfs"), []byte("16\n"), 0644))
	require.NoError(t, os.Symlink(filepath.Join("drivers", "nvidia"), filepath.Join(pfDir, "driver")))
	return root
}

func TestDetectFramework(t *testing.T) {
	testCases := []struct {
		description    string
		mdevBusRoot    func(t *testing.T) string
		pciDevicesRoot func(t *testing.T) string
		expected       Framework
	}{
		{
			"mdev bus with parent devices",
			func(t *testing.T) string { return buildMdevBus(t, "0000:41:00.0") },
			func(t *testing.T) string { return buildPCIDevices(t, false) },
			FrameworkMdev,
		},
		{
			"No mdev bus, vGPU capable SR-IOV physical function",
			func(t *testing.T) string { return filepath.Join(t.TempDir(), "nonexistent") },
			func(t *testing.T) string { return buildPCIDevices(t, true) },
			FrameworkVendorVFIO,
		},
		{
			"Empty mdev bus, vGPU capable SR-IOV physical function",
			func(t *testing.T) string { return buildMdevBus(t) },
			func(t *testing.T) string { return buildPCIDevices(t, true) },
			FrameworkVendorVFIO,
		},
		{
			"mdev bus with parent devices takes precedence",
			func(t *testing.T) string { return buildMdevBus(t, "0000:41:00.0") },
			func(t *testing.T) string { return buildPCIDevices(t, true) },
			FrameworkMdev,
		},
		{
			"Neither mdev bus nor SR-IOV physical functions defaults to mdev",
			func(t *testing.T) string { return filepath.Join(t.TempDir(), "nonexistent") },
			func(t *testing.T) string { return buildPCIDevices(t, false) },
			FrameworkMdev,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result := detectFramework(tc.mdevBusRoot(t), tc.pciDevicesRoot(t))
			require.Equal(t, tc.expected, result)
		})
	}
}
