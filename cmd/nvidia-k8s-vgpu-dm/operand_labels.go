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
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

const pausedForVGPUChange = "paused-for-vgpu-change"

type retryableConfigError struct {
	err error
}

func (e *retryableConfigError) Error() string {
	return e.err.Error()
}

func (e *retryableConfigError) Unwrap() error {
	return e.err
}

func newRetryableConfigError(err error) error {
	return &retryableConfigError{err: err}
}

func isRetryableConfigError(err error) bool {
	var target *retryableConfigError
	return errors.As(err, &target)
}

type operandPauseResult struct {
	preexisting bool
}

func (r operandPauseResult) recovering() bool {
	return r.preexisting
}

func pauseGPUOperandLabels(ctx context.Context, clientset kubernetes.Interface, nodeName string) (operandPauseResult, error) {
	var result operandPauseResult

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		labels := node.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}

		attempt := operandPauseResult{
			preexisting: labels[pluginStateLabel] == pausedForVGPUChange ||
				labels[validatorStateLabel] == pausedForVGPUChange,
		}

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
				return fmt.Errorf("cannot pause %q: label is owned by another operation with value %q", key, value)
			}
		}

		if changed {
			node.SetLabels(labels)
			if _, err := clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
				return err
			}
		}

		result = attempt
		return nil
	})
	if err != nil {
		return operandPauseResult{}, fmt.Errorf("unable to pause GPU operand labels: %w", err)
	}

	return result, nil
}

func restoreGPUOperandLabels(
	ctx context.Context,
	clientset kubernetes.Interface,
	nodeName string,
	restorePlugin bool,
	restoreValidator bool,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			return err
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
		_, err = clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
		return err
	})
}
