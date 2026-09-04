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
	"k8s.io/client-go/util/retry"

	migpartedv1 "github.com/NVIDIA/mig-parted/api/spec/v1"

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
)

type GPUClients struct {
	Version         string   `json:"version"          yaml:"version"`
	SystemdServices []string `json:"systemd-services" yaml:"systemd-services"`
}

type configOperations interface {
	assertValid(string) error
	assertApplied(string) error
	apply(string) error
	configureMIG(string) error
	waitForOperandShutdown(context.Context, string) error
}

type defaultConfigOperations struct {
	clientset kubernetes.Interface
	nodeName  string
	namespace string
}

func (o *defaultConfigOperations) assertValid(config string) error {
	return assertValidConfig(config)
}

func (o *defaultConfigOperations) assertApplied(config string) error {
	return assertConfig(config)
}

func (o *defaultConfigOperations) apply(config string) error {
	return applyConfig(config)
}

func (o *defaultConfigOperations) configureMIG(config string) error {
	return handleMIGConfiguration(o.clientset, config)
}

func (o *defaultConfigOperations) waitForOperandShutdown(ctx context.Context, labelSelector string) error {
	return waitForPodDeletion(ctx, o.clientset, o.namespace, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", o.nodeName),
		LabelSelector: labelSelector,
	})
}

type configReconciler struct {
	clientset         kubernetes.Interface
	operations        configOperations
	defaultVGPUConfig string
	nodeName          string
}

func newConfigReconciler(clientset kubernetes.Interface) *configReconciler {
	return &configReconciler{
		clientset: clientset,
		operations: &defaultConfigOperations{
			clientset: clientset,
			nodeName:  nodeNameFlag,
			namespace: namespaceFlag,
		},
		defaultVGPUConfig: defaultVGPUConfigFlag,
		nodeName:          nodeNameFlag,
	}
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
	reconciler := newConfigReconciler(clientset)

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

	retryBackoff := wait.Backoff{
		Duration: 2 * time.Second,
		Factor:   2,
		Jitter:   0.1,
		Steps:    4,
		Cap:      15 * time.Second,
	}
	for {
		log.Infof("Updating to vGPU config: %s", selectedConfig)
		selectedConfig, err = updateConfigWithRetry(c.Context, reconciler, selectedConfig, retryBackoff)
		if err != nil {
			log.Errorf("Failed to apply vGPU config: %v", err)
		} else {
			log.Infof("Successfully updated to vGPU config: %s", selectedConfig)
		}
		vGPUConfigStateValue := getVGPUConfigStateValue(err)
		log.Infof("Setting node label: %s=%s", vGPUConfigStateLabel, vGPUConfigStateValue)
		_ = setNodeLabelValueForNode(c.Context, clientset, nodeNameFlag, vGPUConfigStateLabel, vGPUConfigStateValue)
		if c.Err() != nil {
			return c.Err()
		}
		if isRetryableConfigError(err) {
			log.Error("Retryable vGPU configuration error persisted after bounded retries")
			return err
		}

		log.Infof("Waiting for change to '%s' label", vGPUConfigLabel)
		selectedConfig = vGPUConfig.Get()
	}
}

func updateConfigWithRetry(
	ctx context.Context,
	reconciler *configReconciler,
	selectedConfig string,
	backoff wait.Backoff,
) (string, error) {
	var lastErr error
	attempt := 0

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		attempt++
		if attempt > 1 {
			currentConfig, err := getNodeLabelValueForNode(ctx, reconciler.clientset, reconciler.nodeName, vGPUConfigLabel)
			if err != nil {
				lastErr = newRetryableConfigError(fmt.Errorf("unable to re-read vGPU config before retry: %w", err))
				return false, nil
			}
			if currentConfig == "" {
				currentConfig = reconciler.defaultVGPUConfig
			}
			selectedConfig = currentConfig
			log.Infof("Retrying with current desired vGPU config: %s", selectedConfig)
		}

		lastErr = reconciler.updateConfig(ctx, selectedConfig)
		if lastErr == nil || !isRetryableConfigError(lastErr) {
			return true, nil
		}

		log.Warnf("Retryable vGPU configuration error on attempt %d of %d: %v", attempt, backoff.Steps, lastErr)
		return false, nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return selectedConfig, ctx.Err()
		}
		if !wait.Interrupted(err) {
			return selectedConfig, err
		}
	}
	return selectedConfig, lastErr
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

func (r *configReconciler) updateConfig(ctx context.Context, selectedConfig string) (returnErr error) {
	log.Info("Asserting that the requested configuration is present in the configuration file")
	err := r.operations.assertValid(selectedConfig)
	if err != nil {
		return fmt.Errorf("unable to validate the selected vGPU configuration")
	}

	log.Info("Checking if the selected vGPU device configuration is currently applied or not")
	err = r.operations.assertApplied(selectedConfig)
	if err == nil {
		log.Info("Restoring any GPU operands left paused by an interrupted vGPU configuration change")
		if err := restoreGPUOperandLabels(ctx, r.clientset, r.nodeName, true, true); err != nil {
			return newRetryableConfigError(fmt.Errorf("unable to recover paused GPU operands: %w", err))
		}
		return nil
	}

	log.Infof("Setting node label: %s=pending", vGPUConfigStateLabel)
	err = setNodeLabelValueForNode(ctx, r.clientset, r.nodeName, vGPUConfigStateLabel, "pending")
	if err != nil {
		return newRetryableConfigError(fmt.Errorf("error setting vGPU config state label: %w", err))
	}

	log.Info("Shutting down all GPU operands in Kubernetes by disabling their component-specific nodeSelector labels")
	_, err = r.shutdownGPUOperands(ctx)
	if err != nil {
		return fmt.Errorf("unable to shutdown GPU operands: %w", err)
	}
	defer func() {
		log.Info("Restarting GPU operands paused for the vGPU configuration change")
		if err := restoreGPUOperandLabels(ctx, r.clientset, r.nodeName, true, true); err != nil {
			restoreErr := fmt.Errorf("unable to restore GPU operands: %w", err)
			if returnErr != nil {
				restoreErr = fmt.Errorf("%w; %w", returnErr, restoreErr)
			}
			returnErr = newRetryableConfigError(restoreErr)
		}
	}()

	if err := r.operations.configureMIG(selectedConfig); err != nil {
		return fmt.Errorf("unable to handle MIG configuration: %w", err)
	}

	log.Info("Applying the selected vGPU device configuration to the node")
	err = r.operations.apply(selectedConfig)
	if err != nil {
		return fmt.Errorf("unable to apply config %q: %w", selectedConfig, err)
	}

	log.Info("Verifying that the selected vGPU device configuration was applied")
	if err := r.operations.assertApplied(selectedConfig); err != nil {
		return fmt.Errorf("applied vGPU configuration %q does not match the requested configuration: %w", selectedConfig, err)
	}

	currentConfig, err := getNodeLabelValueForNode(ctx, r.clientset, r.nodeName, vGPUConfigLabel)
	if err != nil {
		return newRetryableConfigError(fmt.Errorf("unable to re-read vGPU config label before restoring GPU operands: %w", err))
	}
	if currentConfig == "" {
		currentConfig = r.defaultVGPUConfig
	}
	if currentConfig != selectedConfig {
		return newRetryableConfigError(
			fmt.Errorf("vGPU config changed from %q to %q while applying it", selectedConfig, currentConfig),
		)
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

func (r *configReconciler) shutdownGPUOperands(ctx context.Context) (operandPauseResult, error) {
	pauseResult, err := pauseGPUOperandLabels(ctx, r.clientset, r.nodeName)
	if err != nil {
		return operandPauseResult{}, newRetryableConfigError(err)
	}

	log.Info("Waiting for sandbox-device-plugin to shutdown")
	err = r.operations.waitForOperandShutdown(ctx, "app=nvidia-sandbox-device-plugin-daemonset")
	if err == nil {
		log.Info("Waiting for sandbox-validator to shutdown")
		err = r.operations.waitForOperandShutdown(ctx, "app=nvidia-sandbox-validator")
	}
	if err == nil {
		return pauseResult, nil
	}

	// If this process acquired the pause, no hardware operation has started and
	// both labels can be rolled back. If the pause predated this process, the
	// hardware state is unknown and the device plugin must remain paused.
	restorePlugin := !pauseResult.recovering()
	if restoreErr := restoreGPUOperandLabels(ctx, r.clientset, r.nodeName, restorePlugin, true); restoreErr != nil {
		return pauseResult, newRetryableConfigError(
			fmt.Errorf("GPU operand shutdown failed: %w; unable to roll back operand labels: %w", err, restoreErr),
		)
	}

	return pauseResult, newRetryableConfigError(fmt.Errorf("GPU operand shutdown failed: %w", err))
}

func waitForPodDeletion(
	parent context.Context,
	clientset kubernetes.Interface,
	namespace string,
	listOpts metav1.ListOptions,
) error {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	pollFunc := func(context.Context) (bool, error) {
		podList, err := clientset.CoreV1().Pods(namespace).List(ctx, listOpts)
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

func getNodeLabelValue(clientset kubernetes.Interface, label string) (string, error) {
	return getNodeLabelValueForNode(context.TODO(), clientset, nodeNameFlag, label)
}

func getNodeLabelValueForNode(
	ctx context.Context,
	clientset kubernetes.Interface,
	nodeName string,
	label string,
) (string, error) {
	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("unable to get node object: %v", err)
	}

	value, ok := node.Labels[label]
	if !ok {
		return "", nil
	}

	return value, nil
}

func setNodeLabelValueForNode(
	ctx context.Context,
	clientset kubernetes.Interface,
	nodeName string,
	label string,
	value string,
) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		labels := node.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[label] = value
		node.SetLabels(labels)
		_, err = clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
		return err
	})
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

	opts := &reconfigureMIGOptions{
		NodeName:                   nodeNameFlag,
		MIGPartedConfigFile:        migPartedConfigFile,
		SelectedMIGConfig:          selectedConfig,
		DriverLibraryPath:          driverLibraryPath,
		WithReboot:                 withRebootFlag,
		WithShutdownHostGPUClients: withShutdownHostGPUClientsFlag,
		HostRootMount:              hostRootMountFlag,
		HostMIGManagerStateFile:    hostMigManagerStateFileFlag,
		HostGPUClientServices:      gpuClients.SystemdServices,
		HostKubeletService:         hostKubeletSystemdServiceFlag,
	}

	return reconfigureMIG(clientset, opts)
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
