package vgpu

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"os/exec"
	"bufio"


	"github.com/NVIDIA/vgpu-device-manager/internal/nvlib"
	"github.com/NVIDIA/vgpu-device-manager/pkg/types"
)

const (
	PCIDevicesRoot = "/sys/bus/pci/devices"
)

func GetAllNvidiaGPUDevices() ([]*nvpci.NvidiaPCIDevice, error) {
	var nvdevices []*nvpci.NvidiaPCIDevice
	deviceDirs, err := os.ReadDir(PCIDevicesRoot)
	if err != nil {
		return nil, fmt.Errorf("unable to read parent PCI bus devices: %v", err)
	}
	for _, deviceDir := range deviceDirs {
		deviceAddress := deviceDir.Name()
		nvdevice, err := m.nvlib.Nvpci.GetGPUByPciBusID(deviceAddress)
		if err != nil || nvdevice == nil {
			continue
		}
		if nvdevice.IsGPU() {
			nvdevices = append(nvdevices, nvdevice)
		}
	}
	return nvdevices, nil
}

func GetVGPUConfig(gpu int) (types.VGPUConfig, error) {
	nvdevice, err := m.nvlib.Nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return nil, fmt.Errorf("unable to get GPU by index %d: %v", gpu, err)
	}
	GPUDevices, err := m.nvlib.Nvpci.GetGPUs()
	if err != nil {
		return nil, fmt.Errorf("unable to get all NVIDIA GPU devices: %v", err)
	}

	vgpuConfig := types.VGPUConfig{}
	for _, device := range GPUDevices {
		if device.Address == nvdevice.Address {
			VFnum := 0
			totalVF := m.nvlib.Nvpci.getSriovInfoForPhysicalFunction(device.Path).TotalVFs
			for VFnum < totalVF {
				VFAddr := PCIDevicesRoot + "/" + device.Address + "/virtfn" + strconv.Itoa(VFnum) + "/nvidia"
				if _, err := os.Stat(VFAddr); err == nil {
					VGPUTypeNumberBytes, err := os.ReadFile(VFAddr + "/current_vgpu_type")
					if err != nil {
						return nil, fmt.Errorf("unable to read current vGPU type: %v", err)
					}
					VGPUTypeNumber, err := strconv.Atoi(string(VGPUTypeNumberBytes))
					if err != nil {
						return nil, fmt.Errorf("unable to convert current vGPU type to int: %v", err)
					}
					VGPUTypeName, err := getVGPUTypeName(VFAddr + "/creatable_vgpu_types", VGPUTypeNumber)
					if err != nil {
						return nil, fmt.Errorf("unable to get vGPU type name: %v", err)
					}
					vgpuConfig[VGPUTypeName]++
				}
				VFnum++
			}
		}
	}
	
	return vgpuConfig, nil
}

//// Set the vGPU config for each GPU if it is in nvdevices 
func setVGPUConfig(gpu int, config types.VGPUConfig) error {
	nvdevice, err := m.nvlib.Nvpci.GetGPUByIndex(gpu)
	if err != nil {
		return fmt.Errorf("unable to get GPU by index %d: %v", gpu, err)
	}
	
	GPUDevices, err := m.nvlib.Nvpci.GetGPUs()
	if err != nil {
		return fmt.Errorf("unable to get all NVIDIA GPU devices: %v", err)
	}
	
	deviceFound := false
	for _, device := range GPUDevices {
		if device.Address == nvdevice.Address {
			deviceFound = true
			break
		}
	}
	if !deviceFound {
		return fmt.Errorf("GPU at index %d not found in available NVIDIA devices", gpu)
	}

	err = m.ClearVGPUConfig(gpu)
	if err != nil {
		return fmt.Errorf("error clearing VGPUConfig: %v", err)
	}

	cmd := exec.Command("/run/nvidia/driver/usr/lib/nvidia/sriov-manage", "-e", nvdevice.Address)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("unable to execute sriov-manage: %v", err)
	}

	for key, val := range types.VGPUConfig {
		remainingToCreate := val
		VFnum := 0
		for remainingToCreate > 0 {
			VFAddr := PCIDevicesRoot + "/" + nvdevice.Address + "/virtfn" + strconv.Itoa(VFnum) + "/nvidia"
			number, err := getVGPUTypeNumber(VFAddr + "/creatable_vgpu_types", key)
			if err != nil {
				return fmt.Errorf("unable to get vGPU type number: %v", err)
			}
			err = os.WriteFile(VFAddr + "/current_vgpu_type", []byte(strconv.Itoa(number)), 0644)
			if err != nil {
				return fmt.Errorf("unable to write current vGPU type: %v", err)
			}
			VFnum++
			remainingToCreate--
		}
	}
	return nil
}

func getVGPUTypeNumber(filePath string, vgpyTypeName string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("unable to open file %s: %v", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		number := fields[0]
		name := fields[:len(fields)-1]
		if name == vgpyTypeName {
			return strconv.Atoi(number), nil
		}
	}
	return 0, fmt.Errorf("vGPU type %s not found in file %s", vgpyTypeName, filePath)
}

func getVGPUTypeName(filePath string, vgpyTypeNumber int) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("unable to open file %s: %v", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		number := fields[0]
		name := fields[:len(fields)-1]
		if number == vgpyTypeNumber {
			return name, nil
		}
	}
	return "", fmt.Errorf("vGPU type %d not found in file %s", vgpyTypeNumber, filePath)
}

func IsUbuntu2404() (bool, error) {
    data, err := os.ReadFile("/etc/os-release")
    if err != nil {
        return false, err
    }
    
    content := string(data)
    return strings.Contains(content, `ID=ubuntu`) && strings.Contains(content, `VERSION_ID="24.04"`), nil
}