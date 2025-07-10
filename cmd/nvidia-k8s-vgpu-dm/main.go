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

package main

import (
	"fmt"
	"os"
	"os/exec"

	log "github.com/sirupsen/logrus"
	cli "github.com/urfave/cli/v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"

	migpartedv1 "github.com/NVIDIA/mig-parted/api/spec/v1"
	migreconfigure "github.com/NVIDIA/mig-parted/pkg/mig/reconfigure"

	v1 "github.com/NVIDIA/vgpu-device-manager/api/spec/v1"
	"github.com/NVIDIA/vgpu-device-manager/cmd/nvidia-vgpu-dm/assert"
	"github.com/NVIDIA/vgpu-device-manager/internal/info"
)

const (
	cliName              = "nvidia-vgpu-dm"
	resourceNodes        = "nodes"
	vGPUConfigLabel      = "nvidia.com/vgpu.config"
	vGPUConfigStateLabel = "nvidia.com/vgpu.config.state"
	pluginStateLabel     = "nvidia.com/gpu.deploy.sandbox-device-plugin"
	validatorStateLabel  = "nvidia.com/gpu.deploy.sandbox-validator"

	defaultDriverRootCtrPath         = "/driver-root"
	defaultHostRootMount             = "/host"
	defaultHostMigManagerStateFile   = "/etc/systemd/system/nvidia-mig-manager.service.d/override.conf"
	defaultHostKubeletSystemdService = "kubelet.service"
)

var (
	kubeconfigFlag        string
	nodeNameFlag          string
	namespaceFlag         string
	configFileFlag        string
	defaultVGPUConfigFlag string

	hostRootMountFlag              string
	hostMigManagerStateFileFlag    string
	hostKubeletSystemdServiceFlag  string
	driverRootCtrPathFlag          string
	gpuClientsFileFlag             string
	withRebootFlag                 bool
	withShutdownHostGPUClientsFlag bool

	pluginDeployed    string
	validatorDeployed string
)

type GPUClients struct {
	Version         string   `json:"version"          yaml:"version"`
	SystemdServices []string `json:"systemd-services" yaml:"systemd-services"`
}

// SyncableVGPUConfig is used to synchronize on changes to a configuration value.
// That is, callers of Get() will block until a call to Set() is made.
// Multiple calls to Set() do not queue, meaning that only calls to Get() made
// *before* a call to Set() will be notified.
type SyncableVGPUConfig struct {
	cond     *sync.Cond
	mutex    sync.Mutex
	current  string
	lastRead string
}

// NewSyncableVGPUConfig creates a new SyncableVGPUConfig
func NewSyncableVGPUConfig() *SyncableVGPUConfig {
	var m SyncableVGPUConfig
	m.cond = sync.NewCond(&m.mutex)
	return &m
}

// Set sets the value of the config.
// All callers of Get() before the Set() will be unblocked.
func (m *SyncableVGPUConfig) Set(value string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.current = value
	if m.current != "" {
		m.cond.Broadcast()
	}
}

// Get gets the value of the config.
// A call to Get() will block until a subsequent Set() call is made.
func (m *SyncableVGPUConfig) Get() string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.lastRead == m.current {
		m.cond.Wait()
	}
	m.lastRead = m.current
	return m.lastRead
}

func main() {
	c := cli.NewApp()
	c.Name = "nvidia-k8s-vgpu-dm"
	c.Before = validateFlags
	c.Action = start
	c.Version = info.GetVersionString()

	c.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:        "kubeconfig",
			Value:       "",
			Usage:       "absolute path to the kubeconfig file",
			Destination: &kubeconfigFlag,
			EnvVars:     []string{"KUBECONFIG"},
		},
		&cli.StringFlag{
			Name:        "node-name",
			Aliases:     []string{"n"},
			Value:       "",
			Usage:       "the name of the node to watch for label changes on",
			Destination: &nodeNameFlag,
			EnvVars:     []string{"NODE_NAME"},
		},
		&cli.StringFlag{
			Name:        "namespace",
			Aliases:     []string{"ns"},
			Value:       "",
			Usage:       "the namespace in which the GPU components are deployed",
			Destination: &namespaceFlag,
			EnvVars:     []string{"NAMESPACE"},
		},
		&cli.StringFlag{
			Name:        "config-file",
			Aliases:     []string{"f"},
			Value:       "",
			Usage:       "the path to the vGPU configuration file",
			Destination: &configFileFlag,
			EnvVars:     []string{"CONFIG_FILE"},
		},
		&cli.StringFlag{
			Name:        "default-vgpu-config",
			Aliases:     []string{"d"},
			Value:       "",
			Usage:       "the default vGPU config to use if no label is set",
			Destination: &defaultVGPUConfigFlag,
			EnvVars:     []string{"DEFAULT_VGPU_CONFIG"},
		},
		&cli.StringFlag{
			Name:        "host-root-mount",
			Aliases:     []string{"m"},
			Value:       defaultHostRootMount,
			Usage:       "container path where host root directory is mounted",
			Destination: &hostRootMountFlag,
			EnvVars:     []string{"HOST_ROOT_MOUNT"},
		},
		&cli.StringFlag{
			Name:        "host-mig-manager-state-file",
			Aliases:     []string{"o"},
			Value:       defaultHostMigManagerStateFile,
			Usage:       "host path where the host's systemd mig-manager state file is located",
			Destination: &hostMigManagerStateFileFlag,
			EnvVars:     []string{"HOST_MIG_MANAGER_STATE_FILE"},
		},
		&cli.StringFlag{
			Name:        "host-kubelet-systemd-service",
			Aliases:     []string{"k"},
			Value:       defaultHostKubeletSystemdService,
			Usage:       "name of the host's 'kubelet' systemd service which may need to be shutdown/restarted across a MIG mode reconfiguration",
			Destination: &hostKubeletSystemdServiceFlag,
			EnvVars:     []string{"HOST_KUBELET_SYSTEMD_SERVICE"},
		},
		&cli.StringFlag{
			Name:        "driver-root-ctr-path",
			Aliases:     []string{"a"},
			Value:       defaultDriverRootCtrPath,
			Usage:       "root path to the NVIDIA driver installation mounted in the container",
			Destination: &driverRootCtrPathFlag,
			EnvVars:     []string{"DRIVER_ROOT_CTR_PATH"},
		},
		&cli.StringFlag{
			Name:        "gpu-clients-file",
			Aliases:     []string{"g"},
			Value:       "",
			Usage:       "the path to the file listing the GPU clients that need to be shutdown across a MIG configuration",
			Destination: &gpuClientsFileFlag,
			EnvVars:     []string{"GPU_CLIENTS_FILE"},
		},
		&cli.BoolFlag{
			Name:        "with-reboot",
			Aliases:     []string{"r"},
			Value:       false,
			Usage:       "reboot the node if changing the MIG mode fails for any reason",
			Destination: &withRebootFlag,
			EnvVars:     []string{"WITH_REBOOT"},
		},
		&cli.BoolFlag{
			Name:        "with-shutdown-host-gpu-clients",
			Aliases:     []string{"w"},
			Value:       false,
			Usage:       "shutdown/restart any required host GPU clients across a MIG configuration",
			Destination: &withShutdownHostGPUClientsFlag,
			EnvVars:     []string{"WITH_SHUTDOWN_HOST_GPU_CLIENTS"},
		},
	}

	log.Infof("version: %s", c.Version)

	err := c.Run(os.Args)
	if err != nil {
		log.SetOutput(os.Stderr)
		log.Printf("Error: %v", err)
		os.Exit(1)
	}
}

func validateFlags(c *cli.Context) error {
	if nodeNameFlag == "" {
		return fmt.Errorf("invalid <node-name> flag: must not be empty string")
	}
	if namespaceFlag == "" {
		return fmt.Errorf("invalid <namespace> flag: must not be empty string")
	}
	if configFileFlag == "" {
		return fmt.Errorf("invalid <config-file> flag: must not be empty string")
	}
	if defaultVGPUConfigFlag == "" {
		return fmt.Errorf("invalid <default-vgpu-config> flag: must not be empty string")
	}
	return nil
}

func start(c *cli.Context) error {
	clientConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigFlag)
	if err != nil {
		return fmt.Errorf("error building kubernetes clientcmd config: %s", err)
	}

	clientset, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return fmt.Errorf("error building kubernetes clientset from config: %s", err)
	}

	vGPUConfig := NewSyncableVGPUConfig()

	stop := continuouslySyncVGPUConfigChanges(clientset, vGPUConfig)
	defer close(stop)

	// Apply initial vGPU configuration. If the node is not labeled with an
	// explicit config, apply the default configuration.
	selectedConfig, err := getNodeLabelValue(clientset, vGPUConfigLabel)
	if err != nil {
		return fmt.Errorf("unable to get vGPU config label: %v", err)
	}

	if selectedConfig == "" {
		log.Infof("No vGPU config specified for node. Proceeding with default config: %s", defaultVGPUConfigFlag)
		selectedConfig = defaultVGPUConfigFlag
	} else {
		selectedConfig = vGPUConfig.Get()
	}

	log.Infof("Updating to vGPU config: %s", selectedConfig)
	err = updateConfig(clientset, selectedConfig)
	if err != nil {
		log.Errorf("Failed to apply vGPU config: %v", err)
	} else {
		log.Infof("Successfully updated to vGPU config: %s", selectedConfig)
	}
	vGPUConfigStateValue := getVGPUConfigStateValue(err)
	log.Infof("Setting node label: %s=%s", vGPUConfigStateLabel, vGPUConfigStateValue)
	_ = setNodeLabelValue(clientset, vGPUConfigStateLabel, vGPUConfigStateValue)

	// Watch for configuration changes
	for {
		log.Infof("Waiting for change to '%s' label", vGPUConfigLabel)
		value := vGPUConfig.Get()
		log.Infof("Updating to vGPU config: %s", value)
		err = updateConfig(clientset, value)
		if err != nil {
			log.Errorf("Failed to apply vGPU config: %v", err)
		} else {
			log.Infof("Successfully updated to vGPU config: %s", value)
		}
		vGPUConfigStateValue = getVGPUConfigStateValue(err)
		log.Infof("Setting node label: %s=%s", vGPUConfigStateLabel, vGPUConfigStateValue)
		_ = setNodeLabelValue(clientset, vGPUConfigStateLabel, vGPUConfigStateValue)
	}
}

func continuouslySyncVGPUConfigChanges(clientset *kubernetes.Clientset, vGPUConfig *SyncableVGPUConfig) chan struct{} {
	listWatch := cache.NewListWatchFromClient(
		clientset.CoreV1().RESTClient(),
		resourceNodes,
		corev1.NamespaceAll,
		fields.OneTermEqualSelector("metadata.name", nodeNameFlag),
	)

	opts := cache.InformerOptions{
		ListerWatcher: listWatch,
		ObjectType:    &corev1.Node{},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				vGPUConfig.Set(obj.(*corev1.Node).Labels[vGPUConfigLabel])
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldLabel := oldObj.(*corev1.Node).Labels[vGPUConfigLabel]
				newLabel := newObj.(*corev1.Node).Labels[vGPUConfigLabel]
				if oldLabel != newLabel {
					vGPUConfig.Set(newLabel)
				}
			},
		},
		ResyncPeriod: 0,
	}
	_, controller := cache.NewInformerWithOptions(opts)
	stop := make(chan struct{})
	go controller.Run(stop)
	return stop
}

func updateConfig(clientset *kubernetes.Clientset, selectedConfig string) error {

	log.Info("Asserting that the requested configuration is present in the configuration file")
	err := assertValidConfig(selectedConfig)
	if err != nil {
		return fmt.Errorf("unable to validate the selected vGPU configuration")
	}

	log.Info("Checking if the selected vGPU device configuration is currently applied or not")
	err = assertConfig(selectedConfig)
	if err == nil {
		return nil
	}

	err = getNodeStateLabels(clientset)
	if err != nil {
		return fmt.Errorf("unable to get node state labels: %v", err)
	}

	log.Infof("Setting node label: %s=pending", vGPUConfigStateLabel)
	err = setNodeLabelValue(clientset, vGPUConfigStateLabel, "pending")
	if err != nil {
		return fmt.Errorf("error setting vGPU config state label: %v", err)
	}

	log.Info("Shutting down all GPU operands in Kubernetes by disabling their component-specific nodeSelector labels")
	err = shutdownGPUOperands(clientset)
	if err != nil {
		return fmt.Errorf("unable to shutdown gpu operands: %v", err)
	}

	if err := handleMIGConfiguration(clientset, selectedConfig); err != nil {
		return fmt.Errorf("unable to handle MIG configuration: %v", err)
	}

	log.Info("Applying the selected vGPU device configuration to the node")
	err = applyConfig(selectedConfig)
	if err != nil {
		return fmt.Errorf("unable to apply config '%s': %v", selectedConfig, err)
	}

	log.Info("Restarting all GPU operands previously shutdown in Kubernetes by enabling their component-specific nodeSelector labels")
	err = rescheduleGPUOperands(clientset)
	if err != nil {
		return fmt.Errorf("unable to reschedule gpu operands: %v", err)
	}

	return nil
}

func assertValidConfig(config string) error {
	args := []string{
		"assert",
		"--valid-config",
		"-f", configFileFlag,
		"-c", config,
	}
	cmd := exec.Command(cliName, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func assertConfig(config string) error {
	args := []string{
		"assert",
		"-f", configFileFlag,
		"-c", config,
	}
	cmd := exec.Command(cliName, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func applyConfig(config string) error {
	args := []string{
		"-d",
		"apply",
		"-f", configFileFlag,
		"-c", config,
	}
	cmd := exec.Command(cliName, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func getVGPUConfigStateValue(err error) string {
	if err != nil {
		return "failed"
	}
	return "success"
}

func getNodeStateLabels(clientset *kubernetes.Clientset) error {
	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeNameFlag, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to get node object: %v", err)
	}
	labels := node.GetLabels()

	log.Infof("Getting current value of '%s' node label", pluginStateLabel)
	pluginDeployed = labels[pluginStateLabel]
	log.Infof("Current value of '%s=%s'", pluginStateLabel, pluginDeployed)

	log.Infof("Getting current value of '%s' node label", validatorStateLabel)
	validatorDeployed = labels[validatorStateLabel]
	log.Infof("Current value of '%s=%s'", validatorStateLabel, validatorDeployed)

	return nil
}

func shutdownGPUOperands(clientset *kubernetes.Clientset) error {
	// shutdown components by updating their respective state labels.
	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeNameFlag, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to get node object: %v", err)
	}
	labels := node.GetLabels()

	pluginDeployed = maybeSetPaused(pluginDeployed)
	validatorDeployed = maybeSetPaused(validatorDeployed)
	labels[pluginStateLabel] = pluginDeployed
	labels[validatorStateLabel] = validatorDeployed

	node.SetLabels(labels)
	_, err = clientset.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("unable to update node object: %v", err)
	}

	// wait for pods to be deleted
	log.Infof("Waiting for sandbox-device-plugin to shutdown")
	err = waitForPodDeletion(clientset, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeNameFlag),
		LabelSelector: "app=nvidia-sandbox-device-plugin-daemonset",
	})
	if err != nil {
		return fmt.Errorf("error shutting down sandbox-device-plugin: %v", err)
	}

	log.Infof("Waiting for sandbox-validator to shutdown")
	err = waitForPodDeletion(clientset, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeNameFlag),
		LabelSelector: "app=nvidia-sandbox-validator",
	})
	if err != nil {
		return fmt.Errorf("error shutting down sandbox-validator: %v", err)
	}

	return nil
}

func waitForPodDeletion(clientset *kubernetes.Clientset, listOpts metav1.ListOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pollFunc := func(context.Context) (bool, error) {
		podList, err := clientset.CoreV1().Pods(namespaceFlag).List(ctx, listOpts)
		if apierrors.IsNotFound(err) {
			log.Infof("Pod was already deleted")
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if len(podList.Items) == 0 {
			return true, nil
		}
		return false, nil
	}

	err := wait.PollUntilContextCancel(ctx, 1*time.Second, true, pollFunc)
	if err != nil {
		return fmt.Errorf("error deleting pod: %v", err)
	}

	return nil
}

func rescheduleGPUOperands(clientset *kubernetes.Clientset) error {
	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeNameFlag, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to get node object: %v", err)
	}
	labels := node.GetLabels()

	labels[pluginStateLabel] = maybeSetTrue(pluginDeployed)
	labels[validatorStateLabel] = maybeSetTrue(validatorDeployed)

	node.SetLabels(labels)
	_, err = clientset.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("unable to update node object: %v", err)
	}

	return nil
}

func maybeSetPaused(currentValue string) string {
	if currentValue == "false" || currentValue == "" {
		return currentValue
	}
	return "paused-for-vgpu-change"
}

func maybeSetTrue(currentValue string) string {
	if currentValue == "false" || currentValue == "" {
		return currentValue
	}
	return "true"
}

func getNodeLabelValue(clientset *kubernetes.Clientset, label string) (string, error) {
	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeNameFlag, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("unable to get node object: %v", err)
	}

	value, ok := node.Labels[label]
	if !ok {
		return "", nil
	}

	return value, nil
}

func setNodeLabelValue(clientset *kubernetes.Clientset, label, value string) error {
	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeNameFlag, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to get node object: %v", err)
	}

	labels := node.GetLabels()
	labels[label] = value
	node.SetLabels(labels)
	_, err = clientset.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("unable to update node object: %v", err)
	}

	return nil
}

func handleMIGConfiguration(clientset kubernetes.Interface, selectedConfig string) error {
	driverRoot := root(driverRootCtrPathFlag)
	driverLibraryPath, err := driverRoot.getDriverLibraryPath()
	if err != nil {
		log.Errorf("Skipping MIG configuration: unable to find the driver library path: %v", err)
		return nil
	}

	migConfig, err := determineMIGConfig(selectedConfig)
	if err != nil {
		return err
	}

	configFile, err := saveMIGConfigToTempFile(migConfig)
	if err != nil {
		return fmt.Errorf("failed to save MIG config to temporary file: %w", err)
	}

	return updateMIGConfig(clientset.(*kubernetes.Clientset), driverLibraryPath, configFile, selectedConfig)
}

func determineMIGConfig(selectedConfig string) (*migpartedv1.Spec, error) {
	f := &assert.Flags{
		ConfigFile:     configFileFlag,
		SelectedConfig: selectedConfig,
		ValidConfig:    false, // We don't need to validate the config here, just parse it.
	}

	log.Debugf("Parsing vGPU config file...")
	spec, err := assert.ParseConfigFile(f)
	if err != nil {
		return nil, fmt.Errorf("error parsing config file: %w", err)
	}

	log.Debugf("Selecting specific vGPU config...")
	vgpuConfig, err := assert.GetSelectedVGPUConfig(f, spec)
	if err != nil {
		return nil, fmt.Errorf("error getting selected VGPU config: %w", err)
	}

	return convertToMIGConfig(vgpuConfig, selectedConfig)
}

func convertToMIGConfig(vgpuConfig v1.VGPUConfigSpecSlice, selectedConfig string) (*migpartedv1.Spec, error) {
	migConfigSpecSlice, err := vgpuConfig.ToMigConfigSpecSlice()
	if err != nil {
		return nil, fmt.Errorf("error converting vGPU config to MIG config: %w", err)
	}
	migSpec := &migpartedv1.Spec{
		Version: migpartedv1.Version,
		MigConfigs: map[string]migpartedv1.MigConfigSpecSlice{
			selectedConfig: migConfigSpecSlice,
		},
	}
	return migSpec, nil
}

func saveMIGConfigToTempFile(migConfig *migpartedv1.Spec) (string, error) {
	tempFile, err := os.CreateTemp("", "mig-parted-config-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer tempFile.Close()

	yamlData, err := yaml.Marshal(migConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal MIG config to YAML: %w", err)
	}

	if _, err := tempFile.Write(yamlData); err != nil {
		return "", fmt.Errorf("failed to write YAML data to temporary file: %w", err)
	}

	return tempFile.Name(), nil
}

func updateMIGConfig(clientset *kubernetes.Clientset, driverLibraryPath, migPartedConfigFile, selectedConfig string) error {
	defer func() {
		if err := os.Remove(migPartedConfigFile); err != nil {
			log.Errorf("Failed to remove temporary mig-parted config file %s: %v", migPartedConfigFile, err)
		}
	}()

	gpuClients, err := parseGPUCLientsFile(gpuClientsFileFlag)
	if err != nil {
		return fmt.Errorf("error parsing host's GPU clients file: %w", err)
	}

	r, err := migreconfigure.New(
		migreconfigure.WithAllowReboot(withRebootFlag),
		migreconfigure.WithClientset(clientset),
		migreconfigure.WithConfigStateLabel(vGPUConfigStateLabel),
		migreconfigure.WithDriverLibraryPath(driverLibraryPath),
		migreconfigure.WithHostGPUClientServices(gpuClients.SystemdServices...),
		migreconfigure.WithHostKubeletService(hostKubeletSystemdServiceFlag),
		migreconfigure.WithHostMIGManagerStateFile(hostMigManagerStateFileFlag),
		migreconfigure.WithHostRootMount(hostRootMountFlag),
		migreconfigure.WithMIGPartedConfigFile(migPartedConfigFile),
		migreconfigure.WithNodeName(nodeNameFlag),
		migreconfigure.WithSelectedMIGConfig(selectedConfig),
		migreconfigure.WithShutdownHostGPUClients(withShutdownHostGPUClientsFlag),
	)
	if err != nil {
		return err
	}

	return r.Reconfigure()
}

func parseGPUCLientsFile(file string) (*GPUClients, error) {
	var err error
	var yamlBytes []byte

	if file == "" {
		return &GPUClients{}, nil
	}

	yamlBytes, err = os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	var clients GPUClients
	err = yaml.Unmarshal(yamlBytes, &clients)
	if err != nil {
		return nil, fmt.Errorf("unmarshal error: %w", err)
	}

	return &clients, nil
}
