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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	testNodeName = "test-node"
	testConfig   = "test-config"
)

type fakeConfigOperations struct {
	assertValidFunc            func(string) error
	assertAppliedFunc          func(string) error
	applyFunc                  func(string) error
	configureMIGFunc           func(string) error
	waitForOperandShutdownFunc func(context.Context, string) error
}

func (o *fakeConfigOperations) assertValid(config string) error {
	return o.assertValidFunc(config)
}

func (o *fakeConfigOperations) assertApplied(config string) error {
	return o.assertAppliedFunc(config)
}

func (o *fakeConfigOperations) apply(config string) error {
	return o.applyFunc(config)
}

func (o *fakeConfigOperations) configureMIG(config string) error {
	return o.configureMIGFunc(config)
}

func (o *fakeConfigOperations) waitForOperandShutdown(ctx context.Context, labelSelector string) error {
	return o.waitForOperandShutdownFunc(ctx, labelSelector)
}

func TestPauseGPUOperandLabelsUsesStrictTransitions(t *testing.T) {
	testCases := []struct {
		name             string
		labels           map[string]string
		expected         map[string]string
		expectRecovery   bool
		expectPauseError bool
	}{
		{
			name: "enabled operands are paused",
			labels: map[string]string{
				pluginStateLabel:    "true",
				validatorStateLabel: "true",
			},
			expected: map[string]string{
				pluginStateLabel:    pausedForVGPUChange,
				validatorStateLabel: pausedForVGPUChange,
			},
		},
		{
			name: "disabled and absent operands are preserved",
			labels: map[string]string{
				pluginStateLabel: "false",
			},
			expected: map[string]string{
				pluginStateLabel: "false",
			},
		},
		{
			name: "owned pauses are idempotent",
			labels: map[string]string{
				pluginStateLabel:    pausedForVGPUChange,
				validatorStateLabel: pausedForVGPUChange,
			},
			expected: map[string]string{
				pluginStateLabel:    pausedForVGPUChange,
				validatorStateLabel: pausedForVGPUChange,
			},
			expectRecovery: true,
		},
		{
			name: "another operation's pause is preserved",
			labels: map[string]string{
				pluginStateLabel:    "paused-for-driver-upgrade",
				validatorStateLabel: "true",
			},
			expected: map[string]string{
				pluginStateLabel:    "paused-for-driver-upgrade",
				validatorStateLabel: "true",
			},
			expectPauseError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clientset := newTestClientset(t, testNode(tc.labels))

			result, err := pauseGPUOperandLabels(context.Background(), clientset, testNodeName)
			if tc.expectPauseError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectRecovery, result.recovering())
			}

			require.Equal(t, tc.expected, nodeLabels(t, clientset))
		})
	}
}

func TestRestoreGPUOperandLabelsRestoresOnlyOwnedPauses(t *testing.T) {
	clientset := newTestClientset(t, testNode(map[string]string{
		pluginStateLabel:    pausedForVGPUChange,
		validatorStateLabel: "paused-for-driver-upgrade",
		"unrelated":         "unchanged",
	}))

	err := restoreGPUOperandLabels(context.Background(), clientset, testNodeName, true, true)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		pluginStateLabel:    "true",
		validatorStateLabel: "paused-for-driver-upgrade",
		"unrelated":         "unchanged",
	}, nodeLabels(t, clientset))
}

func TestUpdateConfigRecoversOwnedPausesWhenConfigIsAlreadyApplied(t *testing.T) {
	clientset := newTestClientset(t, testNode(map[string]string{
		vGPUConfigLabel:     testConfig,
		pluginStateLabel:    pausedForVGPUChange,
		validatorStateLabel: pausedForVGPUChange,
	}))
	reconciler, operations := testConfigReconciler(clientset)
	operations.assertAppliedFunc = func(string) error { return nil }
	operations.applyFunc = func(string) error {
		t.Fatal("apply must not run when the requested configuration is already applied")
		return nil
	}

	require.NoError(t, reconciler.updateConfig(context.Background(), testConfig))
	require.Equal(t, "true", nodeLabels(t, clientset)[pluginStateLabel])
	require.Equal(t, "true", nodeLabels(t, clientset)[validatorStateLabel])
}

func TestUpdateConfigApplyFailureRestoresGPUOperands(t *testing.T) {
	clientset := newTestClientset(t, testNode(map[string]string{
		vGPUConfigLabel:     testConfig,
		pluginStateLabel:    "true",
		validatorStateLabel: "true",
	}))
	reconciler, operations := testConfigReconciler(clientset)
	operations.assertAppliedFunc = func(string) error { return errors.New("not applied") }
	operations.applyFunc = func(string) error { return errors.New("apply failed") }

	err := reconciler.updateConfig(context.Background(), testConfig)
	require.ErrorContains(t, err, "apply failed")
	require.False(t, isRetryableConfigError(err))
	require.Equal(t, "true", nodeLabels(t, clientset)[pluginStateLabel])
	require.Equal(t, "true", nodeLabels(t, clientset)[validatorStateLabel])
}

func TestUpdateConfigWithRetryUsesLatestDesiredConfig(t *testing.T) {
	clientset := newTestClientset(t, testNode(map[string]string{
		vGPUConfigLabel:     testConfig,
		pluginStateLabel:    "true",
		validatorStateLabel: "true",
	}))
	reconciler, operations := testConfigReconciler(clientset)

	applied := false
	var assertedConfigs []string
	operations.assertValidFunc = func(config string) error {
		assertedConfigs = append(assertedConfigs, config)
		return nil
	}
	operations.assertAppliedFunc = func(string) error {
		if applied {
			return nil
		}
		return errors.New("not applied")
	}
	operations.applyFunc = func(string) error {
		applied = true
		return nil
	}

	waitCalls := 0
	operations.waitForOperandShutdownFunc = func(ctx context.Context, _ string) error {
		waitCalls++
		if waitCalls != 1 {
			return nil
		}

		node, err := clientset.CoreV1().Nodes().Get(ctx, testNodeName, metav1.GetOptions{})
		require.NoError(t, err)
		node.Labels[vGPUConfigLabel] = "new-config"
		_, err = clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
		require.NoError(t, err)
		return errors.New("transient shutdown failure")
	}

	selectedConfig, err := updateConfigWithRetry(
		context.Background(),
		reconciler,
		testConfig,
		wait.Backoff{Steps: 2},
	)
	require.NoError(t, err)
	require.Equal(t, "new-config", selectedConfig)
	require.Equal(t, []string{testConfig, "new-config"}, assertedConfigs)
}

func TestUpdateConfigWithRetryIsBounded(t *testing.T) {
	clientset := newTestClientset(t, testNode(map[string]string{
		vGPUConfigLabel:     testConfig,
		pluginStateLabel:    "true",
		validatorStateLabel: "true",
	}))
	reconciler, operations := testConfigReconciler(clientset)

	attempts := 0
	operations.assertValidFunc = func(string) error {
		attempts++
		return nil
	}
	operations.waitForOperandShutdownFunc = func(context.Context, string) error {
		return errors.New("transient shutdown failure")
	}

	_, err := updateConfigWithRetry(
		context.Background(),
		reconciler,
		testConfig,
		wait.Backoff{Steps: 3},
	)
	require.ErrorContains(t, err, "transient shutdown failure")
	require.True(t, isRetryableConfigError(err))
	require.Equal(t, 3, attempts)
}

func TestUpdateConfigRestoresOperandsOnlyAfterExactVerification(t *testing.T) {
	clientset := newTestClientset(t, testNode(map[string]string{
		vGPUConfigLabel:     testConfig,
		pluginStateLabel:    "true",
		validatorStateLabel: "true",
	}))
	reconciler, operations := testConfigReconciler(clientset)
	assertCalls := 0
	operations.assertAppliedFunc = func(string) error {
		assertCalls++
		if assertCalls == 1 {
			return errors.New("not applied")
		}
		return nil
	}

	require.NoError(t, reconciler.updateConfig(context.Background(), testConfig))
	require.Equal(t, 2, assertCalls)
	require.Equal(t, "true", nodeLabels(t, clientset)[pluginStateLabel])
	require.Equal(t, "true", nodeLabels(t, clientset)[validatorStateLabel])
}

func TestUpdateConfigRestoresOperandsIfDesiredConfigChanges(t *testing.T) {
	clientset := newTestClientset(t, testNode(map[string]string{
		vGPUConfigLabel:     testConfig,
		pluginStateLabel:    "true",
		validatorStateLabel: "true",
	}))
	reconciler, operations := testConfigReconciler(clientset)
	assertCalls := 0
	operations.assertAppliedFunc = func(string) error {
		assertCalls++
		if assertCalls == 1 {
			return errors.New("not applied")
		}
		return nil
	}
	operations.applyFunc = func(string) error {
		node, err := clientset.CoreV1().Nodes().Get(context.Background(), testNodeName, metav1.GetOptions{})
		require.NoError(t, err)
		node.Labels[vGPUConfigLabel] = "new-config"
		_, err = clientset.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
		require.NoError(t, err)
		return nil
	}

	err := reconciler.updateConfig(context.Background(), testConfig)
	require.ErrorContains(t, err, `vGPU config changed from "test-config" to "new-config"`)
	require.True(t, isRetryableConfigError(err))
	require.Equal(t, "true", nodeLabels(t, clientset)[pluginStateLabel])
	require.Equal(t, "true", nodeLabels(t, clientset)[validatorStateLabel])
}

func TestShutdownFailureRollsBackOnlyNewlyAcquiredPause(t *testing.T) {
	testCases := []struct {
		name           string
		initialPlugin  string
		expectedPlugin string
	}{
		{
			name:           "new pause is rolled back",
			initialPlugin:  "true",
			expectedPlugin: "true",
		},
		{
			name:           "recovered pause remains",
			initialPlugin:  pausedForVGPUChange,
			expectedPlugin: pausedForVGPUChange,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clientset := newTestClientset(t, testNode(map[string]string{
				pluginStateLabel:    tc.initialPlugin,
				validatorStateLabel: "true",
			}))
			reconciler, operations := testConfigReconciler(clientset)
			operations.waitForOperandShutdownFunc = func(context.Context, string) error {
				return errors.New("timed out")
			}

			_, err := reconciler.shutdownGPUOperands(context.Background())
			require.ErrorContains(t, err, "timed out")
			require.True(t, isRetryableConfigError(err))
			require.Equal(t, tc.expectedPlugin, nodeLabels(t, clientset)[pluginStateLabel])
			require.Equal(t, "true", nodeLabels(t, clientset)[validatorStateLabel])
		})
	}
}

func testConfigReconciler(clientset kubernetes.Interface) (*configReconciler, *fakeConfigOperations) {
	operations := &fakeConfigOperations{
		assertValidFunc:            func(string) error { return nil },
		assertAppliedFunc:          func(string) error { return errors.New("not applied") },
		applyFunc:                  func(string) error { return nil },
		configureMIGFunc:           func(string) error { return nil },
		waitForOperandShutdownFunc: func(context.Context, string) error { return nil },
	}
	reconciler := &configReconciler{
		clientset:         clientset,
		operations:        operations,
		defaultVGPUConfig: "default",
		nodeName:          testNodeName,
	}
	return reconciler, operations
}

func testNode(labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   testNodeName,
			Labels: labels,
		},
	}
}

func nodeLabels(t *testing.T, clientset kubernetes.Interface) map[string]string {
	t.Helper()
	node, err := clientset.CoreV1().Nodes().Get(context.Background(), testNodeName, metav1.GetOptions{})
	require.NoError(t, err)
	return node.Labels
}

func newTestClientset(t *testing.T, node *corev1.Node) *kubernetes.Clientset {
	t.Helper()

	var mutex sync.Mutex
	storedNode := node.DeepCopy()
	storedNode.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Node"}
	storedNode.ResourceVersion = "1"

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")

		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/nodes/"+testNodeName:
			mutex.Lock()
			defer mutex.Unlock()
			_ = json.NewEncoder(writer).Encode(storedNode)
		case request.Method == http.MethodPut && request.URL.Path == "/api/v1/nodes/"+testNodeName:
			var updated corev1.Node
			if err := json.NewDecoder(request.Body).Decode(&updated); err != nil {
				http.Error(writer, err.Error(), http.StatusBadRequest)
				return
			}

			mutex.Lock()
			storedNode = updated.DeepCopy()
			mutex.Unlock()
			_ = json.NewEncoder(writer).Encode(storedNode)
		case request.Method == http.MethodGet &&
			request.URL.Path == "/api/v1/namespaces/test-namespace/pods":
			_ = json.NewEncoder(writer).Encode(&corev1.PodList{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
			})
		default:
			http.Error(writer, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	clientset, err := kubernetes.NewForConfig(&rest.Config{
		Host: server.URL,
		ContentConfig: rest.ContentConfig{
			AcceptContentTypes: "application/json",
			ContentType:        "application/json",
		},
	})
	require.NoError(t, err)
	return clientset
}
