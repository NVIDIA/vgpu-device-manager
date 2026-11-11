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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const pausedForVGPUChange = "paused-for-vgpu-change"

func pauseGPUOperandLabels(ctx context.Context, clientset kubernetes.Interface, nodeName string) (bool, error) {
	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("unable to get GPU operand labels: %w", err)
	}

	labels := node.GetLabels()
	pauseAlreadyPresent := labels[pluginStateLabel] == pausedForVGPUChange ||
		labels[validatorStateLabel] == pausedForVGPUChange
	changed := false

	for _, key := range []string{pluginStateLabel, validatorStateLabel} {
		value, exists := labels[key]
		switch {
		case !exists || value == "" || value == "false" || value == pausedForVGPUChange:
			continue
		case value == "true":
			labels[key] = pausedForVGPUChange
			changed = true
		default:
			return false, fmt.Errorf("cannot pause %q: label is owned by another operation with value %q", key, value)
		}
	}

	if changed {
		node.SetLabels(labels)
		if _, err := clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
			return false, fmt.Errorf("unable to pause GPU operand labels: %w", err)
		}
	}

	return pauseAlreadyPresent, nil
}

func restoreGPUOperandLabels(
	ctx context.Context,
	clientset kubernetes.Interface,
	nodeName string,
	restorePlugin bool,
	restoreValidator bool,
) error {
	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to get GPU operand labels: %w", err)
	}

	labels := node.GetLabels()
	changed := false
	if restorePlugin && labels[pluginStateLabel] == pausedForVGPUChange {
		labels[pluginStateLabel] = "true"
		changed = true
	}
	if restoreValidator && labels[validatorStateLabel] == pausedForVGPUChange {
		labels[validatorStateLabel] = "true"
		changed = true
	}

	if !changed {
		return nil
	}

	node.SetLabels(labels)
	if _, err := clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("unable to restore GPU operand labels: %w", err)
	}
	return nil
}

func patchNodeLabels(
	ctx context.Context,
	clientset kubernetes.Interface,
	nodeName string,
	labels map[string]string,
) error {
	if len(labels) == 0 {
		return nil
	}

	patch, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": labels,
		},
	})
	if err != nil {
		return fmt.Errorf("unable to create node label patch: %w", err)
	}

	if _, err := clientset.CoreV1().Nodes().Patch(
		ctx,
		nodeName,
		types.MergePatchType,
		patch,
		metav1.PatchOptions{},
	); err != nil {
		return fmt.Errorf("unable to patch node labels: %w", err)
	}

	return nil
}
