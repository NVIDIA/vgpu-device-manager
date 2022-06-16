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

package devicemanager

import (
	"container/ring"
	"fmt"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gitlab.com/nvidia/cloud-native/go-nvlib/pkg/nvmdev"
	"gitlab.com/nvidia/cloud-native/vgpu-device-manager/api/spec/v1"
	"os"
	"sigs.k8s.io/yaml"
	"sync"
)

// VGPUDeviceManager is responsible for applying a desired vGPU configuration.
// A vGPU configuration is simply a list of desired vGPU types. Given a valid
// vGPU configuration, the VGPUDeviceManager will create vGPU devices of the desired
// types on the K8s worker node.
type VGPUDeviceManager struct {
	config                 *v1.Spec
	nvmdev                 nvmdev.Interface
	mutex                  sync.Mutex
	parentDevices          []*nvmdev.ParentDevice
	availableVGPUTypesMap  map[string][]string
	unconfiguredParentsMap map[string]*nvmdev.ParentDevice
}

// NewVGPUDeviceManager creates a new VGPUDeviceManager
func NewVGPUDeviceManager(configFile string) (*VGPUDeviceManager, error) {
	config, err := parseConfigFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("unable to parse config file: %v", err)
	}

	return &VGPUDeviceManager{
		config:                 config,
		nvmdev:                 nvmdev.New(),
		parentDevices:          []*nvmdev.ParentDevice{},
		availableVGPUTypesMap:  make(map[string][]string),
		unconfiguredParentsMap: make(map[string]*nvmdev.ParentDevice),
	}, nil
}

// AssertValidConfig asserts that the named vGPU config is present
// in the vGPU configuration file.
func (m *VGPUDeviceManager) AssertValidConfig(selectedConfig string) bool {
	_, ok := m.config.VGPUConfigs[selectedConfig]
	return ok
}

// ApplyConfig applies a named vGPU config.
func (m *VGPUDeviceManager) ApplyConfig(selectedConfig string) error {
	if !m.AssertValidConfig(selectedConfig) {
		return fmt.Errorf("%s is not a valid config", selectedConfig)
	}

	desiredTypes := m.config.VGPUConfigs[selectedConfig]
	err := m.reconcileVGPUDevices(desiredTypes)
	if err != nil {
		return fmt.Errorf("%v", err)
	}
	return nil
}

// reconcileVGPUDevices reconciles the list of desired vGPU types with the
// actual vGPU devices present on the node. No vGPU device on the node will
// will be of a type not present in the desired lsit of types.
//
// NOTE: Currently no pre-existing vGPU devices are retained on the node, and instead
// every invocation of 'reconcileVGPUDevices()' deletes all existing vGPU
// devices and create new ones based on the list of desired types.
//
// TODO: only delete existing vGPU devices if required.
func (m *VGPUDeviceManager) reconcileVGPUDevices(desiredTypes []string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	parentDevices, err := m.nvmdev.GetAllParentDevices()
	log.Debugf("Number of parent devices: %d", len(parentDevices))
	if err != nil {
		return fmt.Errorf("error getting all NVIDIA PCI devices: %v", err)
	}
	m.parentDevices = parentDevices

	log.Info("Deleting any existing vGPU devices...")
	err = m.deleteAllVGPUDevices()
	if err != nil {
		return fmt.Errorf("error deleting existing vGPU devices: %v", err)
	}

	log.Info("Discovering vGPU devices to configure...")
	err = m.discoverConfigurableVGPUTypes(desiredTypes)
	if err != nil {
		return fmt.Errorf("error discovering configurable vGPU types on the node: %v", err)
	}

	if (len(m.unconfiguredParentsMap) == 0) || (len(m.availableVGPUTypesMap) == 0) {
		log.Info("Nothing to configure")
		return nil
	}

	log.Info("Creating desired vGPU devices...")
	err = m.createDesiredVGPUDevices()
	if err != nil {
		return fmt.Errorf("error creating desired vGPU devices: %v", err)
	}

	return nil
}

// discoverConfigurableVGPUTypes discovers the overlap between the desired vGPU types
// from the config and the available vGPU types on the node. Based on this overlap,
// the necessary data structures are populated which are later used when creating
// vGPU devices.
func (m *VGPUDeviceManager) discoverConfigurableVGPUTypes(desiredTypes []string) error {
	for _, parent := range m.parentDevices {
		for _, desiredType := range desiredTypes {
			available, err := parent.IsMDEVTypeAvailable(desiredType)
			if err != nil {
				return fmt.Errorf("failure to detect if vGPU type %s is available on device %s: %v", desiredType, parent.Address, err)
			}
			if available {
				// availableVGPUTypesMap maps vGPU types to a list of parent devices
				// that can support vGPU devices of said types.
				parentsArray, exists := m.availableVGPUTypesMap[desiredType]
				if !exists {
					parentsArray = []string{}
				}
				parentsArray = append(parentsArray, parent.Address)
				m.availableVGPUTypesMap[desiredType] = parentsArray
				// unconfiguredParentsMap maps a parent PCI address to its
				// corresponding ParentDevice struct. Parent devices present
				// in the map do not have any vGPU devices created yet.
				m.unconfiguredParentsMap[parent.Address] = parent
			}
		}
	}
	return nil
}

// deleteAllVGPUDevices unconditionally deletes all vGPU devices
// present on the node. vGPU devices can only be deleted if they
// are not busy (e.g. assigned to a VM).
func (m *VGPUDeviceManager) deleteAllVGPUDevices() error {
	mdevs, err := m.nvmdev.GetAllDevices()
	if err != nil {
		return fmt.Errorf("unable to get all mdev devices: %v", err)
	}

	for _, device := range mdevs {
		err := device.Delete()
		if err != nil {
			return fmt.Errorf("failed to delete mdev: %v\n", err)
		}
		log.WithFields(log.Fields{
			"vGPUType": device.MDEVType,
			"uuid":     device.UUID,
		}).Info("Successfully deleted vGPU device")
	}

	return nil
}

// newVGPUTypesRing returns a new ring buffer containing vGPU types to configure.
func (m *VGPUDeviceManager) newVGPUTypesRing() *ring.Ring {
	r := ring.New(len(m.availableVGPUTypesMap))

	for vGPUType := range m.availableVGPUTypesMap {
		r.Value = vGPUType
		r = r.Next()
	}

	return r
}

// getNextAvailableParentDevice returns the next available parent device from a list
// of parent devices. Parent devices that are already configured (vGPU devices have
// been created) are skipped.
func (m *VGPUDeviceManager) getNextAvailableParentDevice(parents []string) (*nvmdev.ParentDevice, []string) {
	for i := 0; i <= len(parents); i++ {
		parent := parents[i]
		if dev, exists := m.unconfiguredParentsMap[parent]; exists {
			return dev, parents[i+1:]
		}
	}
	return nil, parents
}

// createDesiredVGPUDevices iterates over a vGPU type ring buffer and creates vGPU devices.
// The vGPU type ring buffer is initialized with a list of vGPU types -- the types form the
// overlap between the desired types and those that are available on the node. The algorithm
// continues until there are no more available parent devices or there are no more available
// vGPU types to create from the desired list.
//
// Example:
//      Given: Node has 3, A10 GPUs
//      Input: Desired list of vGPU types - [A10-4C, A10-8C]
//      Result:
//          - 6, A10-4C devices get created on the first GPU
//          - 3, A10-8C devices get created on the second GPU
//          - 6, A10-4C devices get created on the third GPU
func (m *VGPUDeviceManager) createDesiredVGPUDevices() error {
	r := m.newVGPUTypesRing()

	if r.Len() == 0 {
		log.Warn("No available vGPU types to create")
		return nil
	}

	for {
		vGPUType := r.Value.(string)
		if parents, ok := m.availableVGPUTypesMap[vGPUType]; ok {
			if len(parents) == 0 {
				log.Debugf("No available parent devices for vGPU type: %s\n", vGPUType)
				delete(m.availableVGPUTypesMap, vGPUType)
			}
			parentDevice, parents := m.getNextAvailableParentDevice(parents)
			availableInstances, err := parentDevice.GetAvailableMDEVInstances(vGPUType)
			if err != nil {
				return fmt.Errorf("unable to check if %s is available on device %s: %v", vGPUType, parentDevice.Address, err)
			}
			if availableInstances > 0 {
				log.Infof("Creating %d instance(s) of vGPU type %s on device %s", availableInstances, vGPUType, parentDevice.Address)
				for i := 0; i < availableInstances; i++ {
					uuid := uuid.New().String()
					err := parentDevice.CreateMDEVDevice(vGPUType, uuid)
					if err != nil {
						return fmt.Errorf("unable to create %s device on parent device %s: %v", vGPUType, parentDevice.Address, err)
					}
					log.WithFields(log.Fields{
						"vGPUType":   vGPUType,
						"pciAddress": parentDevice.Address,
						"uuid":       uuid,
					}).Info("Successfully created vGPU device")
				}
				delete(m.unconfiguredParentsMap, parentDevice.Address)
			}

			if len(parents) > 0 {
				m.availableVGPUTypesMap[vGPUType] = parents
			}
			if len(parents) == 0 {
				delete(m.availableVGPUTypesMap, vGPUType)
			}
		}
		r = r.Next()

		if (len(m.unconfiguredParentsMap) == 0) || (len(m.availableVGPUTypesMap) == 0) {
			break
		}
	}
	return nil
}

func parseConfigFile(configFile string) (*v1.Spec, error) {
	var err error
	var configYaml []byte
	configYaml, err = os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("read error: %v", err)
	}

	var spec v1.Spec
	err = yaml.Unmarshal(configYaml, &spec)
	if err != nil {
		return nil, fmt.Errorf("unmarshal error: %v", err)
	}

	return &spec, nil
}

func stringInSlice(slice []string, str string) bool {
	for _, value := range slice {
		if value == str {
			return true
		}
	}
	return false
}
