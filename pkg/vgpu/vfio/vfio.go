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

// Package vfio implements vGPU configuration management for GPUs whose vGPU
// devices are exposed through the vendor-specific VFIO framework used by the
// NVIDIA vGPU Manager driver on Ada, Hopper and newer GPUs (vGPU 17.0+).
//
// On these systems there is no mediated device (mdev) framework. Instead,
// each GPU is an SR-IOV physical function bound to the 'nvidia' driver, and
// vGPU devices are created on its virtual functions by writing a numeric
// vGPU type ID to the per-VF 'nvidia/current_vgpu_type' sysfs file. The set
// of type IDs currently creatable on a VF is reported by its
// 'nvidia/creatable_vgpu_types' sysfs file.
package vfio

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"

	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

// DefaultSriovManagePath is the path of the sriov-manage script shipped with
// the NVIDIA vGPU Manager driver, used to enable SR-IOV virtual functions on
// a GPU.
const DefaultSriovManagePath = "/usr/lib/nvidia/sriov-manage"

var pciAddressPattern = regexp.MustCompile(`^[0-9a-fA-F]{4,8}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-7]$`)

// Manager represents a set of functions for managing vGPU configurations on
// GPUs exposed through the vendor-specific VFIO framework.
type Manager interface {
	GetVGPUConfig(gpu int) (types.VGPUConfig, error)
	SetVGPUConfig(gpu int, config types.VGPUConfig) error
	ClearVGPUConfig(gpu int) error
	NormalizeVGPUConfig(gpu int, config types.VGPUConfig) (types.VGPUConfig, error)
}

// TypeResolver resolves the full set of vGPU types supported by a GPU to a
// map of numeric type IDs to type names. It complements the per-VF
// 'creatable_vgpu_types' sysfs files, which only list types that can be
// created at the time of reading: once existing vGPU devices consume the
// GPU's capacity, the creatable lists no longer include the types of the
// devices that were already created.
type TypeResolver interface {
	SupportedVGPUTypes(pfAddress string) (map[int]string, error)
}

type manager struct {
	pciDevicesRoot string
	hostRootMount  string
	resolver       TypeResolver
	sriovEnable    func(pfAddress string) error
}

var _ Manager = (*manager)(nil)

// Option is a function that configures the vfio Manager.
type Option func(*manager)

// WithPCIDevicesRoot provides an Option to set the root path for PCI devices
// under sysfs.
func WithPCIDevicesRoot(root string) Option {
	return func(m *manager) {
		m.pciDevicesRoot = root
	}
}

// WithHostRootMount provides an Option to set the container path where the
// host root directory is mounted. When set, the sriov-manage script is run
// through chroot into this mount so that the script shipped with the host
// driver installation is used.
func WithHostRootMount(mount string) Option {
	return func(m *manager) {
		m.hostRootMount = mount
	}
}

// WithTypeResolver provides an Option to set the TypeResolver used to map
// vGPU type IDs to type names when the sysfs creatable types files cannot.
func WithTypeResolver(resolver TypeResolver) Option {
	return func(m *manager) {
		m.resolver = resolver
	}
}

// WithSriovEnable provides an Option to set the function used to enable
// SR-IOV virtual functions on a GPU.
func WithSriovEnable(enable func(pfAddress string) error) Option {
	return func(m *manager) {
		m.sriovEnable = enable
	}
}

// New returns a new Manager for vGPU configurations on GPUs exposed through
// the vendor-specific VFIO framework.
func New(opts ...Option) Manager {
	m := &manager{
		pciDevicesRoot: DefaultPCIDevicesRoot,
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.resolver == nil {
		m.resolver = newNVMLTypeResolver()
	}
	if m.sriovEnable == nil {
		m.sriovEnable = sriovManageEnable(m.hostRootMount)
	}
	return m
}

// HasVGPUCapableDevices reports whether any NVIDIA GPU under the provided
// PCI devices root exposes the vendor-specific VFIO SR-IOV interface, i.e.
// is an SR-IOV physical function bound to the 'nvidia' driver.
func HasVGPUCapableDevices(pciDevicesRoot string) bool {
	addresses, err := gpuAddresses(pciDevicesRoot)
	if err != nil {
		return false
	}
	for _, address := range addresses {
		d := device{root: pciDevicesRoot, address: address}
		if d.isVGPUCapablePF() {
			return true
		}
	}
	return false
}

// GetVGPUConfig gets the 'VGPUConfig' currently applied to a GPU at a particular index
func (m *manager) GetVGPUConfig(gpu int) (types.VGPUConfig, error) {
	pf, err := m.gpuByIndex(gpu)
	if err != nil {
		return nil, fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	vfs, err := pf.virtualFunctions()
	if err != nil {
		return nil, fmt.Errorf("error enumerating virtual functions for GPU %s: %v", pf.address, err)
	}

	vgpuConfig := types.VGPUConfig{}
	if len(vfs) == 0 {
		return vgpuConfig, nil
	}

	registry := m.newTypeRegistry(pf, vfs)
	for _, vf := range vfs {
		if !vf.hasVGPUSysfs() {
			return nil, vgpuSysfsMissingError(vf)
		}
		current, err := vf.currentVGPUType()
		if err != nil {
			return nil, fmt.Errorf("error reading current vGPU type for VF %s: %v", vf.address, err)
		}
		if current == 0 {
			continue
		}
		name, err := registry.nameForID(current)
		if err != nil {
			return nil, fmt.Errorf("error resolving vGPU type ID %d on VF %s: %v", current, vf.address, err)
		}
		vgpuConfig[name]++
	}

	return vgpuConfig, nil
}

// SetVGPUConfig applies the selected `VGPUConfig` to a GPU at a particular index if it is not already applied
func (m *manager) SetVGPUConfig(gpu int, config types.VGPUConfig) error {
	pf, err := m.gpuByIndex(gpu)
	if err != nil {
		return fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	if !pf.isVGPUCapablePF() {
		return fmt.Errorf("GPU (index=%d, address=%s) does not expose the vendor-specific VFIO SR-IOV interface", gpu, pf.address)
	}

	vfs, err := m.ensureVFs(pf)
	if err != nil {
		return err
	}

	// Fail before any vGPU device is deleted if the VFs are not managed
	// through the vendor-specific VFIO framework.
	for _, vf := range vfs {
		if !vf.hasVGPUSysfs() {
			return vgpuSysfsMissingError(vf)
		}
	}

	// Before deleting any existing vGPU devices, ensure all vGPU types
	// specified in the config are supported for the GPU we are applying the
	// configuration to. As with the mdev backend, a MIG attribute suffix
	// (ME, NOME, MEALL, GFX) is stripped from the type name if the type is
	// only supported without it.
	registry := m.newTypeRegistry(pf, vfs)
	requests, err := resolveVGPUConfig(registry, config)
	if err != nil {
		return fmt.Errorf("%v on GPU (index=%d, address=%s)", err, gpu, pf.address)
	}

	err = m.ClearVGPUConfig(gpu)
	if err != nil {
		return fmt.Errorf("error clearing VGPUConfig: %v", err)
	}

	for _, req := range requests {
		remainingToCreate := req.count
		for _, vf := range vfs {
			// A non-positive count must stop before any VF is touched: a
			// negative count would otherwise never reach zero and fill every
			// free VF with the requested type.
			if remainingToCreate <= 0 {
				break
			}

			current, err := vf.currentVGPUType()
			if err != nil {
				return fmt.Errorf("error reading current vGPU type for VF %s: %v", vf.address, err)
			}
			if current != 0 {
				continue
			}

			// The creatable types of a VF change as vGPU devices are created
			// on its siblings, so they must be re-read for every VF.
			creatable, err := vf.creatableVGPUTypes()
			if err != nil {
				return fmt.Errorf("error reading creatable vGPU types for VF %s: %v", vf.address, err)
			}
			if !containsTypeID(creatable, req.id) {
				continue
			}

			if err := vf.setVGPUType(req.id); err != nil {
				return fmt.Errorf("unable to create %s vGPU device on VF %s: %v", req.name, vf.address, err)
			}
			remainingToCreate--
		}

		if remainingToCreate > 0 {
			err := fmt.Errorf("failed to create %[1]d %[2]s vGPU devices on the GPU. ensure '%[1]d' does not exceed the maximum supported instances for '%[2]s'", req.count, req.name)
			if vgpu, parseErr := types.ParseVGPUType(req.name); parseErr == nil && vgpu.G > 0 {
				err = fmt.Errorf("%v. for MIG-backed vGPU types, also ensure MIG mode is enabled and enough GPU instances have been created", err)
			}
			return err
		}
	}

	return nil
}

// NormalizeVGPUConfig returns the config as it would be realized on the GPU
// at a particular index, mapping vGPU types that are only supported without
// their MIG attribute suffix to the supported name.
func (m *manager) NormalizeVGPUConfig(gpu int, config types.VGPUConfig) (types.VGPUConfig, error) {
	if len(config) == 0 {
		return config, nil
	}

	pf, err := m.gpuByIndex(gpu)
	if err != nil {
		return nil, fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	vfs, err := pf.virtualFunctions()
	if err != nil {
		return nil, fmt.Errorf("error enumerating virtual functions for GPU %s: %v", pf.address, err)
	}

	registry := m.newTypeRegistry(pf, vfs)
	resolved, err := resolveVGPUConfig(registry, config)
	if err != nil {
		return nil, fmt.Errorf("%v on GPU (index=%d, address=%s)", err, gpu, pf.address)
	}

	normalized := types.VGPUConfig{}
	for _, r := range resolved {
		normalized[r.name] = r.count
	}
	return normalized, nil
}

// ClearVGPUConfig clears the 'VGPUConfig' for a GPU at a particular index by
// deleting all vGPU devices associated with it
func (m *manager) ClearVGPUConfig(gpu int) error {
	pf, err := m.gpuByIndex(gpu)
	if err != nil {
		return fmt.Errorf("error getting device at index '%d': %v", gpu, err)
	}

	vfs, err := pf.virtualFunctions()
	if err != nil {
		return fmt.Errorf("error enumerating virtual functions for GPU %s: %v", pf.address, err)
	}

	for _, vf := range vfs {
		if !vf.hasVGPUSysfs() {
			return vgpuSysfsMissingError(vf)
		}
		current, err := vf.currentVGPUType()
		if err != nil {
			return fmt.Errorf("error reading current vGPU type for VF %s: %v", vf.address, err)
		}
		if current == 0 {
			continue
		}
		if err := vf.setVGPUType(0); err != nil {
			return fmt.Errorf("error deleting vGPU device (type ID %d) on VF %s: %v", current, vf.address, err)
		}
	}

	return nil
}

// vgpuSysfsMissingError explains why the vendor-specific VFIO sysfs
// interface is not present on a VF. On GPUs whose vGPU devices are managed
// through the mdev framework (e.g. Ampere), the mdev parent devices are also
// SR-IOV VFs; before the VFs are enabled such a system is indistinguishable
// from a vendor-VFIO one, so the vfio backend may have been selected for it.
// Once the VFs are enabled, the mdev bus is populated and the mdev backend
// is selected instead.
func vgpuSysfsMissingError(vf device) error {
	if vf.hasMdevSupportedTypes() {
		return fmt.Errorf("VF %s exposes vGPU types through the mdev framework: re-run to select the mdev backend now that the virtual functions are enabled", vf.address)
	}
	return fmt.Errorf("vGPU sysfs interface not found for VF %s: ensure the NVIDIA vGPU Manager driver is loaded", vf.address)
}

// gpuByIndex returns the device representing the NVIDIA GPU at a particular index.
func (m *manager) gpuByIndex(gpu int) (device, error) {
	addresses, err := gpuAddresses(m.pciDevicesRoot)
	if err != nil {
		return device{}, err
	}
	if gpu < 0 || gpu >= len(addresses) {
		return device{}, fmt.Errorf("no GPU found at index '%d'", gpu)
	}
	return device{root: m.pciDevicesRoot, address: addresses[gpu]}, nil
}

// ensureVFs returns the SR-IOV virtual functions of a physical function,
// enabling them first if none are present. The sriov-manage script completes
// the creation of the VFs before it returns, so no polling is required.
func (m *manager) ensureVFs(pf device) ([]device, error) {
	vfs, err := pf.virtualFunctions()
	if err != nil {
		return nil, fmt.Errorf("error enumerating virtual functions for GPU %s: %v", pf.address, err)
	}
	if len(vfs) > 0 {
		return vfs, nil
	}

	if err := m.sriovEnable(pf.address); err != nil {
		return nil, fmt.Errorf("error enabling SR-IOV virtual functions for GPU %s: %v", pf.address, err)
	}

	vfs, err = pf.virtualFunctions()
	if err != nil {
		return nil, fmt.Errorf("error enumerating virtual functions for GPU %s: %v", pf.address, err)
	}
	if len(vfs) == 0 {
		return nil, fmt.Errorf("no virtual functions found for GPU %s after enabling SR-IOV", pf.address)
	}
	return vfs, nil
}

// sriovManageArgs builds the argument vector for enabling SR-IOV virtual
// functions on a GPU via the sriov-manage script shipped with the NVIDIA
// vGPU Manager driver. If hostRootMount is non-empty, the script is run
// through chroot into it so that the script from the host driver
// installation is used.
func sriovManageArgs(hostRootMount, pfAddress string) []string {
	if hostRootMount != "" {
		return []string{"chroot", hostRootMount, DefaultSriovManagePath, "-e", pfAddress}
	}
	return []string{DefaultSriovManagePath, "-e", pfAddress}
}

// sriovManageEnable returns a function that enables SR-IOV virtual functions
// on a GPU by running the sriov-manage script.
func sriovManageEnable(hostRootMount string) func(pfAddress string) error {
	return func(pfAddress string) error {
		if !pciAddressPattern.MatchString(pfAddress) {
			return fmt.Errorf("invalid PCI address '%s'", pfAddress)
		}

		args := sriovManageArgs(hostRootMount, pfAddress)
		cmd := exec.Command(args[0], args[1:]...) // #nosec G204 -- hostRootMount comes from a CLI flag, pfAddress is validated against a PCI address pattern.
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("error running sriov-manage: %v", err)
		}
		return nil
	}
}

// typeRegistry maps between numeric vGPU type IDs and vGPU type names for a
// single GPU. It is populated from the 'creatable_vgpu_types' sysfs files of
// the GPU's virtual functions and lazily falls back to the TypeResolver for
// type IDs and names not found there.
type typeRegistry struct {
	pfAddress    string
	resolver     TypeResolver
	idToName     map[int]string
	shortToID    map[string]int
	resolverDone bool
	resolverErr  error
}

// newTypeRegistry creates a typeRegistry for a GPU, populated from the
// creatable vGPU types of its virtual functions. Read errors are tolerated
// here; resolution failures surface at lookup time instead.
func (m *manager) newTypeRegistry(pf device, vfs []device) *typeRegistry {
	r := &typeRegistry{
		pfAddress: pf.address,
		resolver:  m.resolver,
		idToName:  make(map[int]string),
		shortToID: make(map[string]int),
	}
	for _, vf := range vfs {
		creatable, err := vf.creatableVGPUTypes()
		if err != nil {
			continue
		}
		for _, t := range creatable {
			r.add(t)
		}
	}
	return r
}

func (r *typeRegistry) add(t vgpuType) {
	r.idToName[t.ID] = t.Name
	short := t.shortName()
	if _, exists := r.shortToID[short]; !exists {
		r.shortToID[short] = t.ID
	}
}

// fillFromResolver merges the types reported by the TypeResolver into the
// registry. It only ever runs once per registry.
func (r *typeRegistry) fillFromResolver() {
	if r.resolverDone || r.resolver == nil {
		return
	}
	r.resolverDone = true

	supported, err := r.resolver.SupportedVGPUTypes(r.pfAddress)
	if err != nil {
		r.resolverErr = err
		return
	}
	for id, name := range supported {
		r.add(vgpuType{ID: id, Name: name})
	}
}

// nameForID returns the short vGPU type name for a numeric type ID.
func (r *typeRegistry) nameForID(id int) (string, error) {
	if name, ok := r.idToName[id]; ok {
		return vgpuType{ID: id, Name: name}.shortName(), nil
	}
	r.fillFromResolver()
	if name, ok := r.idToName[id]; ok {
		return vgpuType{ID: id, Name: name}.shortName(), nil
	}
	if r.resolverErr != nil {
		return "", fmt.Errorf("vGPU type ID not found in sysfs, and the type resolver failed: %v", r.resolverErr)
	}
	return "", fmt.Errorf("unknown vGPU type ID")
}

// idForName returns the numeric type ID for a short vGPU type name, along
// with the name that was actually matched: if the name is only supported
// without its MIG attribute suffix, the stripped name is matched instead.
// The exact name is exhausted against all sources before the stripped name
// is considered, so a supported suffixed type is never silently substituted
// with its base type.
func (r *typeRegistry) idForName(name string) (int, string, error) {
	candidates := []string{name}
	if stripped := types.StripAttributeSuffix(name); stripped != name {
		candidates = append(candidates, stripped)
	}

	for _, candidate := range candidates {
		if id, ok := r.shortToID[candidate]; ok {
			return id, candidate, nil
		}
		r.fillFromResolver()
		if id, ok := r.shortToID[candidate]; ok {
			return id, candidate, nil
		}
	}

	if r.resolverErr != nil {
		return 0, "", fmt.Errorf("vGPU type not found in sysfs, and the type resolver failed: %v", r.resolverErr)
	}
	return 0, "", fmt.Errorf("unknown vGPU type")
}

// resolvedType is a vGPU config entry resolved against a typeRegistry.
type resolvedType struct {
	name  string
	id    int
	count int
}

// resolveVGPUConfig resolves every vGPU type in the config to its numeric
// type ID and matched name. Two config entries mapping to the same supported
// type are rejected: the counts of such entries have no well-defined
// combined meaning.
func resolveVGPUConfig(registry *typeRegistry, config types.VGPUConfig) ([]resolvedType, error) {
	matchedBy := make(map[string]string)
	var resolved []resolvedType
	for key, val := range config {
		id, name, err := registry.idForName(key)
		if err != nil {
			return nil, fmt.Errorf("vGPU type %s is not supported: %v", key, err)
		}
		if previous, exists := matchedBy[name]; exists {
			return nil, fmt.Errorf("vGPU types %s and %s map to the same supported vGPU type %s", previous, key, name)
		}
		matchedBy[name] = key
		resolved = append(resolved, resolvedType{name: name, id: id, count: val})
	}
	return resolved, nil
}

func containsTypeID(vgpuTypes []vgpuType, id int) bool {
	for _, t := range vgpuTypes {
		if t.ID == id {
			return true
		}
	}
	return false
}
