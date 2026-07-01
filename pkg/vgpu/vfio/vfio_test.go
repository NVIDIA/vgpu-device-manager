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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

const (
	// Realistic 'creatable_vgpu_types' content for an H200-like GPU with
	// both a MIG-backed and a whole-card (time-sliced) vGPU type.
	creatableH200 = "1414 : NVIDIA H200X-1-18C\n1428 : NVIDIA H200X-141C\n"
)

// fakeVF describes a single SR-IOV virtual function in the fake sysfs tree.
type fakeVF struct {
	address            string
	current            int
	creatable          string
	noVGPUSysfs        bool
	mdevSupportedTypes bool
}

// fakeGPU describes a single NVIDIA GPU (SR-IOV physical function) in the
// fake sysfs tree.
type fakeGPU struct {
	address      string
	class        string
	driver       string
	sriovCapable bool
	vfs          []fakeVF
}

// buildFakeSysfs constructs a fake PCI devices sysfs tree and returns its
// root directory. The layout mirrors /sys/bus/pci/devices as exposed by the
// vendor-specific VFIO framework.
func buildFakeSysfs(t *testing.T, gpus []fakeGPU) string {
	t.Helper()
	root := t.TempDir()

	driversDir := filepath.Join(root, "drivers")
	require.NoError(t, os.MkdirAll(filepath.Join(driversDir, "nvidia"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(driversDir, "vfio-pci"), 0755))

	for _, gpu := range gpus {
		class := gpu.class
		if class == "" {
			class = "0x030200"
		}
		driver := gpu.driver
		if driver == "" {
			driver = "nvidia"
		}

		pfDir := filepath.Join(root, gpu.address)
		require.NoError(t, os.MkdirAll(pfDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(pfDir, "vendor"), []byte("0x10de\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(pfDir, "class"), []byte(class+"\n"), 0644))
		require.NoError(t, os.Symlink(filepath.Join("drivers", driver), filepath.Join(pfDir, "driver")))

		if gpu.sriovCapable {
			require.NoError(t, os.WriteFile(filepath.Join(pfDir, "sriov_totalvfs"), []byte("16\n"), 0644))
			require.NoError(t, os.WriteFile(filepath.Join(pfDir, "sriov_numvfs"), []byte(fmt.Sprintf("%d\n", len(gpu.vfs))), 0644))
		}

		for i, vf := range gpu.vfs {
			addFakeVF(t, root, gpu.address, i, vf)
		}
	}

	return root
}

// addFakeVF adds a single virtual function to the fake sysfs tree.
func addFakeVF(t *testing.T, root, pfAddress string, index int, vf fakeVF) {
	t.Helper()

	vfDir := filepath.Join(root, vf.address)
	require.NoError(t, os.MkdirAll(vfDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(vfDir, "vendor"), []byte("0x10de\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(vfDir, "class"), []byte("0x030200\n"), 0644))
	require.NoError(t, os.Symlink(filepath.Join("drivers", "nvidia"), filepath.Join(vfDir, "driver")))
	require.NoError(t, os.Symlink(pfAddress, filepath.Join(vfDir, "physfn")))
	require.NoError(t, os.Symlink(vf.address, filepath.Join(root, pfAddress, fmt.Sprintf("virtfn%d", index))))

	if vf.mdevSupportedTypes {
		require.NoError(t, os.MkdirAll(filepath.Join(vfDir, "mdev_supported_types", "nvidia-556"), 0755))
	}

	if vf.noVGPUSysfs {
		return
	}

	vgpuDir := filepath.Join(vfDir, "nvidia")
	require.NoError(t, os.MkdirAll(vgpuDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(vgpuDir, "creatable_vgpu_types"), []byte(vf.creatable), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(vgpuDir, "current_vgpu_type"), []byte(fmt.Sprintf("%d\n", vf.current)), 0644))
	// 'gpu_instance_id', 'placement_id' and 'vgpu_params' are only readable
	// after a vGPU type has been set. Model that by only creating the file
	// (with read permission) when a type is set.
	if vf.current != 0 {
		require.NoError(t, os.WriteFile(filepath.Join(vgpuDir, "gpu_instance_id"), []byte("0\n"), 0644))
	}
}

// fakeResolver is a TypeResolver backed by a static map.
type fakeResolver struct {
	supported map[int]string
	err       error
	calls     int
}

func (r *fakeResolver) SupportedVGPUTypes(pfAddress string) (map[int]string, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return r.supported, nil
}

// failingResolver returns an error on every call.
func failingResolver() *fakeResolver {
	return &fakeResolver{err: fmt.Errorf("NVML not available")}
}

func h200Resolver() *fakeResolver {
	return &fakeResolver{supported: map[int]string{
		1414: "NVIDIA H200X-1-18C",
		1428: "NVIDIA H200X-141C",
	}}
}

func newTestManager(t *testing.T, root string, opts ...Option) Manager {
	t.Helper()
	defaultOpts := []Option{
		WithPCIDevicesRoot(root),
		WithTypeResolver(failingResolver()),
		WithSriovEnable(func(pfAddress string) error {
			return fmt.Errorf("sriov enable not expected in this test")
		}),
	}
	return New(append(defaultOpts, opts...)...)
}

func readCurrentVGPUType(t *testing.T, root, vfAddress string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, vfAddress, "nvidia", "current_vgpu_type"))
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}

func TestGetVGPUConfig(t *testing.T) {
	testCases := []struct {
		description string
		gpus        []fakeGPU
		resolver    *fakeResolver
		gpu         int
		expected    types.VGPUConfig
		expectError bool
	}{
		{
			description: "No VFs enabled returns empty config",
			gpus: []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true},
			},
			gpu:      0,
			expected: types.VGPUConfig{},
		},
		{
			description: "All VFs empty returns empty config",
			gpus: []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
					{address: "0000:18:00.4", creatable: creatableH200},
					{address: "0000:18:00.5", creatable: creatableH200},
				}},
			},
			gpu:      0,
			expected: types.VGPUConfig{},
		},
		{
			description: "Whole-card vGPU resolved via creatable types of sibling VF",
			gpus: []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
					{address: "0000:18:00.4", current: 1428},
					{address: "0000:18:00.5", creatable: creatableH200},
				}},
			},
			gpu:      0,
			expected: types.VGPUConfig{"H200X-141C": 1},
		},
		{
			description: "MIG-backed vGPUs counted per type",
			gpus: []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
					{address: "0000:18:00.4", current: 1414},
					{address: "0000:18:00.5", current: 1414},
					{address: "0000:18:00.6", creatable: creatableH200},
				}},
			},
			gpu:      0,
			expected: types.VGPUConfig{"H200X-1-18C": 2},
		},
		{
			description: "Type ID not in creatable lists resolved via resolver",
			gpus: []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
					{address: "0000:18:00.4", current: 1428},
					{address: "0000:18:00.5"},
				}},
			},
			resolver: h200Resolver(),
			gpu:      0,
			expected: types.VGPUConfig{"H200X-141C": 1},
		},
		{
			description: "Type ID not resolvable anywhere returns error",
			gpus: []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
					{address: "0000:18:00.4", current: 9999},
				}},
			},
			gpu:         0,
			expectError: true,
		},
		{
			description: "VF without vGPU sysfs interface returns error",
			gpus: []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
					{address: "0000:18:00.4", noVGPUSysfs: true},
				}},
			},
			gpu:         0,
			expectError: true,
		},
		{
			description: "GPU index out of range returns error",
			gpus: []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true},
			},
			gpu:         1,
			expectError: true,
		},
		{
			description: "Second GPU has independent config",
			gpus: []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
					{address: "0000:18:00.4", current: 1428},
					{address: "0000:18:00.5", creatable: creatableH200},
				}},
				{address: "0000:2a:00.0", sriovCapable: true, vfs: []fakeVF{
					{address: "0000:2a:00.4", creatable: creatableH200},
				}},
			},
			gpu:      1,
			expected: types.VGPUConfig{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			root := buildFakeSysfs(t, tc.gpus)
			opts := []Option{}
			if tc.resolver != nil {
				opts = append(opts, WithTypeResolver(tc.resolver))
			}
			m := newTestManager(t, root, opts...)

			config, err := m.GetVGPUConfig(tc.gpu)
			if tc.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.True(t, config.Equals(tc.expected), "got %v, expected %v", config, tc.expected)
		})
	}
}

func TestSetVGPUConfig(t *testing.T) {
	t.Run("Whole-card type is created on the first VF that can create it", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", creatable: creatableH200},
				{address: "0000:18:00.5", creatable: creatableH200},
			}},
		})
		m := newTestManager(t, root)

		err := m.SetVGPUConfig(0, types.VGPUConfig{"H200X-141C": 1})
		require.NoError(t, err)

		require.Equal(t, "1428", readCurrentVGPUType(t, root, "0000:18:00.4"))
		require.Equal(t, "0", readCurrentVGPUType(t, root, "0000:18:00.5"))
	})

	t.Run("MIG-backed types are created on multiple VFs", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", creatable: creatableH200},
				{address: "0000:18:00.5", creatable: creatableH200},
				{address: "0000:18:00.6", creatable: creatableH200},
			}},
		})
		m := newTestManager(t, root)

		err := m.SetVGPUConfig(0, types.VGPUConfig{"H200X-1-18C": 2})
		require.NoError(t, err)

		require.Equal(t, "1414", readCurrentVGPUType(t, root, "0000:18:00.4"))
		require.Equal(t, "1414", readCurrentVGPUType(t, root, "0000:18:00.5"))
		require.Equal(t, "0", readCurrentVGPUType(t, root, "0000:18:00.6"))
	})

	t.Run("Existing vGPU devices are cleared before new ones are created", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", current: 1428, creatable: creatableH200},
				{address: "0000:18:00.5", creatable: creatableH200},
			}},
		})
		m := newTestManager(t, root)

		err := m.SetVGPUConfig(0, types.VGPUConfig{"H200X-1-18C": 2})
		require.NoError(t, err)

		require.Equal(t, "1414", readCurrentVGPUType(t, root, "0000:18:00.4"))
		require.Equal(t, "1414", readCurrentVGPUType(t, root, "0000:18:00.5"))
	})

	t.Run("Unsupported type fails before clearing existing devices", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", current: 1428, creatable: creatableH200},
			}},
		})
		m := newTestManager(t, root)

		err := m.SetVGPUConfig(0, types.VGPUConfig{"A100-40C": 1})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not supported")

		// The previously configured vGPU device must not have been cleared.
		require.Equal(t, "1428", readCurrentVGPUType(t, root, "0000:18:00.4"))
	})

	t.Run("Attribute suffix is stripped when only the base type is creatable", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", creatable: "999 : NVIDIA DC-1-24Q\n"},
			}},
		})
		m := newTestManager(t, root)

		err := m.SetVGPUConfig(0, types.VGPUConfig{"DC-1-24QGFX": 1})
		require.NoError(t, err)

		require.Equal(t, "999", readCurrentVGPUType(t, root, "0000:18:00.4"))
	})

	t.Run("Count exceeding creatable capacity returns error", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", creatable: creatableH200},
				{address: "0000:18:00.5"},
			}},
		})
		m := newTestManager(t, root)

		err := m.SetVGPUConfig(0, types.VGPUConfig{"H200X-1-18C": 2})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to create")
	})

	t.Run("SR-IOV VFs are enabled when none are present", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true},
		})

		enableCalled := 0
		m := newTestManager(t, root, WithSriovEnable(func(pfAddress string) error {
			enableCalled++
			require.Equal(t, "0000:18:00.0", pfAddress)
			addFakeVF(t, root, "0000:18:00.0", 0, fakeVF{address: "0000:18:00.4", creatable: creatableH200})
			return nil
		}))

		err := m.SetVGPUConfig(0, types.VGPUConfig{"H200X-141C": 1})
		require.NoError(t, err)
		require.Equal(t, 1, enableCalled)
		require.Equal(t, "1428", readCurrentVGPUType(t, root, "0000:18:00.4"))
	})

	t.Run("Failure to enable SR-IOV VFs returns error", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true},
		})
		m := newTestManager(t, root, WithSriovEnable(func(pfAddress string) error {
			return fmt.Errorf("sriov-manage failed")
		}))

		err := m.SetVGPUConfig(0, types.VGPUConfig{"H200X-141C": 1})
		require.Error(t, err)
	})

	t.Run("GPU without SR-IOV interface returns error", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0"},
		})
		m := newTestManager(t, root)

		err := m.SetVGPUConfig(0, types.VGPUConfig{"H200X-141C": 1})
		require.Error(t, err)
		require.Contains(t, err.Error(), "vendor-specific VFIO")
	})

	t.Run("Empty config clears all vGPU devices", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", current: 1414, creatable: creatableH200},
				{address: "0000:18:00.5", current: 1414, creatable: creatableH200},
			}},
		})
		m := newTestManager(t, root)

		err := m.SetVGPUConfig(0, types.VGPUConfig{})
		require.NoError(t, err)

		require.Equal(t, "0", readCurrentVGPUType(t, root, "0000:18:00.4"))
		require.Equal(t, "0", readCurrentVGPUType(t, root, "0000:18:00.5"))
	})

	t.Run("Suffixed config compares equal to the applied state after normalization", func(t *testing.T) {
		// This mirrors the apply/assert flow: after a suffixed config has
		// been applied, GetVGPUConfig reports the stripped name the driver
		// knows. Comparing the current state against the normalized desired
		// config must report a match, so re-applying is a no-op instead of
		// a teardown-and-recreate cycle.
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", creatable: "999 : NVIDIA DC-1-24Q\n"},
			}},
		})
		m := newTestManager(t, root)

		desired := types.VGPUConfig{"DC-1-24QGFX": 1}
		require.NoError(t, m.SetVGPUConfig(0, desired))

		current, err := m.GetVGPUConfig(0)
		require.NoError(t, err)
		require.False(t, current.Equals(desired), "raw comparison must mismatch, that is why normalization exists")

		normalized, err := m.NormalizeVGPUConfig(0, desired)
		require.NoError(t, err)
		require.True(t, current.Equals(normalized), "got %v, expected %v", current, normalized)
	})

	t.Run("Config entries colliding after suffix stripping are rejected before clearing", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", current: 999, creatable: "999 : NVIDIA DC-1-24Q\n"},
			}},
		})
		m := newTestManager(t, root)

		err := m.SetVGPUConfig(0, types.VGPUConfig{"DC-1-24QGFX": 2, "DC-1-24Q": 3})
		require.Error(t, err)
		require.Contains(t, err.Error(), "map to the same supported vGPU type")

		// The previously configured vGPU device must not have been cleared.
		require.Equal(t, "999", readCurrentVGPUType(t, root, "0000:18:00.4"))
	})

	t.Run("Supported suffixed type is not substituted with its base type", func(t *testing.T) {
		// The resolver knows the suffixed type as a distinct type, but the
		// VF cannot currently create it: the request must fail instead of
		// silently creating the base type.
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", creatable: "999 : NVIDIA DC-1-24Q\n"},
			}},
		})
		resolver := &fakeResolver{supported: map[int]string{
			1001: "NVIDIA DC-1-24QGFX",
		}}
		m := newTestManager(t, root, WithTypeResolver(resolver))

		err := m.SetVGPUConfig(0, types.VGPUConfig{"DC-1-24QGFX": 1})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to create")

		// The base type must not have been created instead.
		require.Equal(t, "0", readCurrentVGPUType(t, root, "0000:18:00.4"))
	})

	t.Run("Re-applying the same config is idempotent", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", creatable: creatableH200},
				{address: "0000:18:00.5", creatable: creatableH200},
				{address: "0000:18:00.6", creatable: creatableH200},
			}},
		})
		m := newTestManager(t, root, WithTypeResolver(h200Resolver()))

		desired := types.VGPUConfig{"H200X-1-18C": 2}
		require.NoError(t, m.SetVGPUConfig(0, desired))

		current, err := m.GetVGPUConfig(0)
		require.NoError(t, err)
		require.True(t, current.Equals(desired), "got %v, expected %v", current, desired)

		// Re-apply and verify the same configuration is still in place.
		require.NoError(t, m.SetVGPUConfig(0, desired))
		current, err = m.GetVGPUConfig(0)
		require.NoError(t, err)
		require.True(t, current.Equals(desired), "got %v, expected %v", current, desired)
	})
}

func TestClearVGPUConfig(t *testing.T) {
	t.Run("All configured VFs are cleared", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", current: 1414},
				{address: "0000:18:00.5"},
				{address: "0000:18:00.6", current: 1428},
			}},
		})
		m := newTestManager(t, root)

		require.NoError(t, m.ClearVGPUConfig(0))

		require.Equal(t, "0", readCurrentVGPUType(t, root, "0000:18:00.4"))
		require.Equal(t, "0", readCurrentVGPUType(t, root, "0000:18:00.5"))
		require.Equal(t, "0", readCurrentVGPUType(t, root, "0000:18:00.6"))
	})

	t.Run("GPU with no VFs is a no-op", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true},
		})
		m := newTestManager(t, root)

		require.NoError(t, m.ClearVGPUConfig(0))
	})
}

func TestGPUEnumeration(t *testing.T) {
	t.Run("VFs and non-GPU devices are excluded from GPU indices", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:18:00.4", current: 1428},
				{address: "0000:18:00.5", creatable: creatableH200},
			}},
			{address: "0000:2a:00.0", sriovCapable: true},
		})

		// Add a non-GPU NVIDIA device (e.g. an audio function) which must be
		// ignored during GPU enumeration.
		audioDir := filepath.Join(root, "0000:18:00.1")
		require.NoError(t, os.MkdirAll(audioDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(audioDir, "vendor"), []byte("0x10de\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(audioDir, "class"), []byte("0x040300\n"), 0644))

		m := newTestManager(t, root)

		// GPU 0 is the PF at 0000:18:00.0 with one configured vGPU device.
		config, err := m.GetVGPUConfig(0)
		require.NoError(t, err)
		require.True(t, config.Equals(types.VGPUConfig{"H200X-141C": 1}))

		// GPU 1 is the PF at 0000:2a:00.0 with no VFs.
		config, err = m.GetVGPUConfig(1)
		require.NoError(t, err)
		require.True(t, config.Equals(types.VGPUConfig{}))

		// There is no GPU 2.
		_, err = m.GetVGPUConfig(2)
		require.Error(t, err)
	})
}

func TestMdevManagedVFs(t *testing.T) {
	// On GPUs whose vGPU devices are managed through the mdev framework
	// (e.g. Ampere), the mdev parent devices are also SR-IOV VFs, but they
	// carry an 'mdev_supported_types' directory instead of the
	// vendor-specific 'nvidia' directory. Before the VFs are enabled such a
	// system is indistinguishable from a vendor-VFIO one, so the vfio
	// backend may be selected; once the VFs turn out to be mdev-managed, it
	// must fail with an error pointing at the mdev framework instead of
	// blaming the driver installation.
	buildMdevSysfs := func(t *testing.T) string {
		t.Helper()
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:41:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:41:00.4", noVGPUSysfs: true, mdevSupportedTypes: true},
			}},
		})
		return root
	}

	t.Run("GetVGPUConfig reports the mdev framework", func(t *testing.T) {
		m := newTestManager(t, buildMdevSysfs(t))
		_, err := m.GetVGPUConfig(0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "mdev framework")
	})

	t.Run("SetVGPUConfig reports the mdev framework before clearing", func(t *testing.T) {
		m := newTestManager(t, buildMdevSysfs(t))
		err := m.SetVGPUConfig(0, types.VGPUConfig{"A100-40C": 1})
		require.Error(t, err)
		require.Contains(t, err.Error(), "mdev framework")
	})

	t.Run("ClearVGPUConfig reports the mdev framework", func(t *testing.T) {
		m := newTestManager(t, buildMdevSysfs(t))
		err := m.ClearVGPUConfig(0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "mdev framework")
	})

	t.Run("VF without any vGPU interface still blames the driver", func(t *testing.T) {
		root := buildFakeSysfs(t, []fakeGPU{
			{address: "0000:41:00.0", sriovCapable: true, vfs: []fakeVF{
				{address: "0000:41:00.4", noVGPUSysfs: true},
			}},
		})
		m := newTestManager(t, root)
		_, err := m.GetVGPUConfig(0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "NVIDIA vGPU Manager driver")
	})
}

func TestTypeRegistryCandidatePriority(t *testing.T) {
	// A supported suffixed type must never be silently substituted with its
	// base type: the exact name is exhausted against all sources (sysfs
	// creatable lists, then the resolver) before the stripped name is
	// considered.
	root := buildFakeSysfs(t, []fakeGPU{
		{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
			{address: "0000:18:00.4", creatable: "999 : NVIDIA DC-1-24Q\n"},
		}},
	})
	resolver := &fakeResolver{supported: map[int]string{
		1001: "NVIDIA DC-1-24QGFX",
	}}
	m := newTestManager(t, root, WithTypeResolver(resolver)).(*manager)

	pf := device{root: root, address: "0000:18:00.0"}
	vfs, err := pf.virtualFunctions()
	require.NoError(t, err)

	registry := m.newTypeRegistry(pf, vfs)
	id, name, err := registry.idForName("DC-1-24QGFX")
	require.NoError(t, err)
	require.Equal(t, 1001, id)
	require.Equal(t, "DC-1-24QGFX", name)

	// The base name still resolves to the sysfs-provided type.
	id, name, err = registry.idForName("DC-1-24Q")
	require.NoError(t, err)
	require.Equal(t, 999, id)
	require.Equal(t, "DC-1-24Q", name)
}

func TestNormalizeVGPUConfig(t *testing.T) {
	testCases := []struct {
		description string
		creatable   string
		config      types.VGPUConfig
		expected    types.VGPUConfig
		expectError string
	}{
		{
			description: "Suffixed type is normalized to the supported base type",
			creatable:   "999 : NVIDIA DC-1-24Q\n",
			config:      types.VGPUConfig{"DC-1-24QGFX": 1},
			expected:    types.VGPUConfig{"DC-1-24Q": 1},
		},
		{
			description: "Exactly supported type is kept",
			creatable:   creatableH200,
			config:      types.VGPUConfig{"H200X-1-18C": 2},
			expected:    types.VGPUConfig{"H200X-1-18C": 2},
		},
		{
			description: "Empty config stays empty",
			creatable:   creatableH200,
			config:      types.VGPUConfig{},
			expected:    types.VGPUConfig{},
		},
		{
			description: "Unsupported type is rejected",
			creatable:   creatableH200,
			config:      types.VGPUConfig{"T4-16Q": 1},
			expectError: "not supported",
		},
		{
			description: "Types colliding after suffix stripping are rejected",
			creatable:   "999 : NVIDIA DC-1-24Q\n",
			config:      types.VGPUConfig{"DC-1-24QGFX": 2, "DC-1-24Q": 3},
			expectError: "map to the same supported vGPU type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			root := buildFakeSysfs(t, []fakeGPU{
				{address: "0000:18:00.0", sriovCapable: true, vfs: []fakeVF{
					{address: "0000:18:00.4", creatable: tc.creatable},
				}},
			})
			m := newTestManager(t, root)

			normalized, err := m.NormalizeVGPUConfig(0, tc.config)
			if tc.expectError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectError)
				return
			}
			require.NoError(t, err)
			require.True(t, normalized.Equals(tc.expected), "got %v, expected %v", normalized, tc.expected)
		})
	}
}

func TestSriovManageArgs(t *testing.T) {
	t.Run("Without a host root mount the script is run directly", func(t *testing.T) {
		args := sriovManageArgs("", "0000:18:00.0")
		require.Equal(t, []string{DefaultSriovManagePath, "-e", "0000:18:00.0"}, args)
	})

	t.Run("With a host root mount the script is run through chroot", func(t *testing.T) {
		args := sriovManageArgs("/host", "0000:18:00.0")
		require.Equal(t, []string{"chroot", "/host", DefaultSriovManagePath, "-e", "0000:18:00.0"}, args)
	})
}

func TestSriovManageEnableValidation(t *testing.T) {
	// Invalid PCI addresses must be rejected before any command is run.
	enable := sriovManageEnable("")
	for _, address := range []string{
		"",
		"not-an-address",
		"../../../etc/passwd",
		"0000:18:00.0; rm -rf /",
		"0000:18:00",
	} {
		err := enable(address)
		require.Error(t, err, "address %q must be rejected", address)
		require.Contains(t, err.Error(), "invalid PCI address")
	}
}

func TestHasVGPUCapableDevices(t *testing.T) {
	testCases := []struct {
		description string
		gpus        []fakeGPU
		expected    bool
	}{
		{
			"NVIDIA GPU with SR-IOV bound to nvidia",
			[]fakeGPU{{address: "0000:18:00.0", sriovCapable: true}},
			true,
		},
		{
			"NVIDIA GPU without SR-IOV",
			[]fakeGPU{{address: "0000:18:00.0"}},
			false,
		},
		{
			"NVIDIA GPU with SR-IOV bound to vfio-pci",
			[]fakeGPU{{address: "0000:18:00.0", driver: "vfio-pci", sriovCapable: true}},
			false,
		},
		{
			"No devices",
			nil,
			false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			root := buildFakeSysfs(t, tc.gpus)
			require.Equal(t, tc.expected, HasVGPUCapableDevices(root))
		})
	}
}
