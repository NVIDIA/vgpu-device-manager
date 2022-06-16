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
	log "github.com/sirupsen/logrus"
	cli "github.com/urfave/cli/v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"os"

	"context"
	dm "gitlab.com/nvidia/cloud-native/vgpu-device-manager/pkg/devicemanager"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"sync"
	"time"
)

const (
	resourceNodes        = "nodes"
	vGPUConfigLabel      = "nvidia.com/vgpu.config"
	vGPUConfigStateLabel = "nvidia.com/vgpu.config.state"
	pluginStateLabel     = "nvidia.com/gpu.deploy.sandbox-device-plugin"
	validatorStateLabel  = "nvidia.com/gpu.deploy.sandbox-validator"
)

var (
	kubeconfigFlag        string
	nodeNameFlag          string
	namespaceFlag         string
	configFileFlag        string
	defaultVGPUConfigFlag string

	pluginDeployed    string
	validatorDeployed string
	vGPUConfigState   string
)

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
	c.Before = validateFlags
	c.Action = start

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
	}

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

	m, err := dm.NewVGPUDeviceManager(configFileFlag)
	if err != nil {
		return fmt.Errorf("error creating new VGPUDeviceManager: %v", err)
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
	err = updateConfig(clientset, m, selectedConfig)
	if err != nil {
		log.Errorf("ERROR: %v", err)
	} else {
		log.Infof("Successfully updated to vGPU config: %s", selectedConfig)
	}

	// Watch for configuration changes
	for {
		log.Infof("Waiting for change to '%s' label", vGPUConfigLabel)
		value := vGPUConfig.Get()
		log.Infof("Updating to vGPU config: %s", value)
		err = updateConfig(clientset, m, value)
		if err != nil {
			log.Errorf("ERROR: %v", err)
			continue
		}
		log.Infof("Successfuly updated to vGPU config: %s", value)
	}
}

func continuouslySyncVGPUConfigChanges(clientset *kubernetes.Clientset, vGPUConfig *SyncableVGPUConfig) chan struct{} {
	listWatch := cache.NewListWatchFromClient(
		clientset.CoreV1().RESTClient(),
		resourceNodes,
		corev1.NamespaceAll,
		fields.OneTermEqualSelector("metadata.name", nodeNameFlag),
	)

	_, controller := cache.NewInformer(
		listWatch, &corev1.Node{}, 0,
		cache.ResourceEventHandlerFuncs{
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
	)

	stop := make(chan struct{})
	go controller.Run(stop)
	return stop
}

func updateConfig(clientset *kubernetes.Clientset, m *dm.VGPUDeviceManager, selectedConfig string) error {
	defer setVGPUConfigStateLabel(clientset)
	vGPUConfigState = "failed"

	log.Info("Asserting that the requested configuration is present in the configuration file")
	ok := m.AssertValidConfig(selectedConfig)
	if !ok {
		return fmt.Errorf("%s is not a valid config", selectedConfig)
	}

	err := getNodeStateLabels(clientset)
	if err != nil {
		return fmt.Errorf("unable to get node state labels: %v", err)
	}

	log.Infof("Changing the '%s' node label to 'pending'", vGPUConfigStateLabel)
	err = setNodeLabelValue(clientset, vGPUConfigStateLabel, "pending")
	if err != nil {
		return fmt.Errorf("error setting vGPU config state label: %v", err)
	}

	log.Info("Shutting down all GPU operands in Kubernetes by disabling their component-specific nodeSelector labels")
	err = shutdownGPUOperands(clientset)
	if err != nil {
		return fmt.Errorf("unable to shutdown gpu operands: %v", err)
	}

	err = m.ApplyConfig(selectedConfig)
	if err != nil {
		return fmt.Errorf("unable to apply config '%s': %v", selectedConfig, err)
	}

	log.Info("Restarting all GPU operands previously shutdown in Kubernetes by enabling their component-specific nodeSelector labels")
	err = rescheduleGPUOperands(clientset)
	if err != nil {
		return fmt.Errorf("unable to reschedule gpu operands: %v", err)
	}

	vGPUConfigState = "success"
	return nil
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

	log.Infof("Getting current value of '%s' node label", vGPUConfigStateLabel)
	vGPUConfigState = labels[vGPUConfigStateLabel]
	log.Infof("Current value of '%s=%s'", vGPUConfigStateLabel, vGPUConfigState)

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
		return fmt.Errorf("Error shutting down sandbox-device-plugin: %v", err)
	}

	log.Infof("Waiting for sandbox-validator to shutdown")
	err = waitForPodDeletion(clientset, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeNameFlag),
		LabelSelector: "app=nvidia-sandbox-validator",
	})
	if err != nil {
		return fmt.Errorf("Error shutting down sandbox-validator: %v", err)
	}

	return nil
}

func waitForPodDeletion(clientset *kubernetes.Clientset, listOpts metav1.ListOptions) error {
	pollFunc := func() (bool, error) {
		podList, err := clientset.CoreV1().Pods(namespaceFlag).List(context.TODO(), listOpts)
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

	err := wait.PollImmediate(1*time.Second, 120*time.Second, pollFunc)
	if err != nil {
		return fmt.Errorf("Error deleting pod: %v", err)
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

func setVGPUConfigStateLabel(clientset *kubernetes.Clientset) error {
	log.Infof("Changing the '%s' node label to '%s'", vGPUConfigStateLabel, vGPUConfigState)
	err := setNodeLabelValue(clientset, vGPUConfigStateLabel, vGPUConfigState)
	if err != nil {
		return fmt.Errorf("error setting vGPU config state label: %v", err)
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
