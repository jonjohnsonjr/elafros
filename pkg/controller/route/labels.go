/*
Copyright 2018 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package route

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/knative/serving/pkg/apis/serving"
	"github.com/knative/serving/pkg/apis/serving/v1alpha1"
	"github.com/knative/serving/pkg/controller/route/traffic"
	"github.com/knative/serving/pkg/logging"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (r *Reconciler) syncLabels(ctx context.Context, route *v1alpha1.Route, tc *traffic.TrafficConfig) error {
	if err := r.deleteLabelForOutsideOfGivenConfigurations(ctx, route, tc.Configurations); err != nil {
		return err
	}
	if err := r.setLabelForGivenConfigurations(ctx, route, tc.Configurations); err != nil {
		return err
	}
	return nil
}

func (r *Reconciler) setLabelForGivenConfigurations(
	ctx context.Context, route *v1alpha1.Route, configMap map[string]*v1alpha1.Configuration) error {
	logger := logging.FromContext(ctx)
	configClient := r.ServingClientSet.ServingV1alpha1().Configurations(route.Namespace)

	names := []string{}

	// Validate
	for _, config := range configMap {
		names = append(names, config.Name)
		routeName, ok := config.Labels[serving.RouteLabelKey]
		if !ok {
			continue
		}
		// TODO(yanweiguo): add a condition in status for this error
		if routeName != route.Name {
			errMsg := fmt.Sprintf("Configuration %q is already in use by %q, and cannot be used by %q",
				config.Name, routeName, route.Name)
			r.Recorder.Event(route, corev1.EventTypeWarning, "ConfigurationInUse", errMsg)
			logger.Error(errMsg)
			return errors.New(errMsg)
		}
	}
	// Sort the names to give things a deterministic ordering.
	sort.Strings(names)

	// Set label for newly added configurations as traffic target.
	for _, configName := range names {
		config := configMap[configName]
		if config.Labels == nil {
			config.Labels = make(map[string]string)
		} else if _, ok := config.Labels[serving.RouteLabelKey]; ok {
			continue
		}
		config.Labels[serving.RouteLabelKey] = route.Name
		if _, err := configClient.Update(config); err != nil {
			logger.Errorf("Failed to update Configuration %s: %s", config.Name, err)
			return err
		}
	}

	return nil
}

func (r *Reconciler) deleteLabelForOutsideOfGivenConfigurations(
	ctx context.Context, route *v1alpha1.Route, configMap map[string]*v1alpha1.Configuration) error {
	logger := logging.FromContext(ctx)
	ns := route.Namespace
	// Get Configurations set as traffic target before this sync.
	selector, err := labels.Parse(fmt.Sprintf("%s=%s", serving.RouteLabelKey, route.Name))
	if err != nil {
		return err
	}
	oldConfigsList, err := r.configurationLister.Configurations(ns).List(selector)
	if err != nil {
		logger.Errorf("Failed to fetch configurations with label '%s=%s': %s",
			serving.RouteLabelKey, route.Name, err)
		return err
	}

	// Delete label for newly removed configurations as traffic target.
	for _, config := range oldConfigsList {
		if _, ok := configMap[config.Name]; !ok {
			delete(config.Labels, serving.RouteLabelKey)
			if _, err := r.ServingClientSet.ServingV1alpha1().Configurations(ns).Update(config); err != nil {
				logger.Errorf("Failed to update Configuration %s: %s", config.Name, err)
				return err
			}
		}
	}

	return nil
}
