/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package deckhouse

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	// ModuleGroupVersion is the API group and version for Module resources
	ModuleGroupVersion = "deckhouse.io/v1alpha1"
	// ModuleResource is the resource name for Module
	ModuleResource = "modules"
	// ModuleConfigGroupVersion is the API group and version for ModuleConfig resources
	ModuleConfigGroupVersion = "deckhouse.io/v1alpha1"
	// ModuleConfigResource is the resource name for ModuleConfig
	ModuleConfigResource = "moduleconfigs"
	// ModulePullOverrideGroupVersion is the API group and version for ModulePullOverride resources
	ModulePullOverrideGroupVersion = "deckhouse.io/v1alpha2"
	// ModulePullOverrideResource is the resource name for ModulePullOverride
	ModulePullOverrideResource = "modulepulloverrides"
)

// GetModule retrieves detailed information about a single module by name
func GetModule(ctx context.Context, config *rest.Config, moduleName string) (*Module, error) {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "deckhouse.io",
		Version:  "v1alpha1",
		Resource: ModuleResource,
	}

	// Module is a cluster-scoped resource, so we use empty namespace
	unstructuredObj, err := client.Resource(gvr).Get(ctx, moduleName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get module %s: %w", moduleName, err)
	}

	module, err := unstructuredToModule(unstructuredObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured module %s: %w", moduleName, err)
	}

	return module, nil
}

// unstructuredToModule converts an unstructured.Unstructured object to a Module struct
func unstructuredToModule(obj *unstructured.Unstructured) (*Module, error) {
	module := &Module{}

	// Set TypeMeta
	module.APIVersion = obj.GetAPIVersion()
	module.Kind = obj.GetKind()

	// Set ObjectMeta
	module.ObjectMeta = metav1.ObjectMeta{
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		UID:               obj.GetUID(),
		ResourceVersion:   obj.GetResourceVersion(),
		Generation:        obj.GetGeneration(),
		CreationTimestamp: obj.GetCreationTimestamp(),
		Labels:            obj.GetLabels(),
		Annotations:       obj.GetAnnotations(),
	}

	// Extract properties
	if properties, found, err := unstructured.NestedMap(obj.Object, "properties"); err != nil {
		return nil, fmt.Errorf("failed to extract properties: %w", err)
	} else if found {
		if err := extractModuleProperties(properties, &module.Properties); err != nil {
			return nil, fmt.Errorf("failed to extract module properties: %w", err)
		}
	}

	// Extract status
	if status, found, err := unstructured.NestedMap(obj.Object, "status"); err != nil {
		return nil, fmt.Errorf("failed to extract status: %w", err)
	} else if found {
		if err := extractModuleStatus(status, &module.Status); err != nil {
			return nil, fmt.Errorf("failed to extract module status: %w", err)
		}
	}

	return module, nil
}

// extractModuleProperties extracts ModuleProperties from a map
func extractModuleProperties(data map[string]interface{}, props *ModuleProperties) error {
	if critical, found, err := unstructured.NestedBool(data, "critical"); err != nil {
		return err
	} else if found {
		props.Critical = critical
	}

	if disableOptions, found, err := unstructured.NestedMap(data, "disableOptions"); err != nil {
		return err
	} else if found && len(disableOptions) > 0 {
		props.DisableOptions = &DisableOptions{}
		if confirmation, found, err := unstructured.NestedBool(disableOptions, "confirmation"); err != nil {
			return err
		} else if found {
			props.DisableOptions.Confirmation = confirmation
		}
		if message, found, err := unstructured.NestedString(disableOptions, "message"); err != nil {
			return err
		} else if found {
			props.DisableOptions.Message = message
		}
	}

	if namespace, found, err := unstructured.NestedString(data, "namespace"); err != nil {
		return err
	} else if found {
		props.Namespace = namespace
	}

	if releaseChannel, found, err := unstructured.NestedString(data, "releaseChannel"); err != nil {
		return err
	} else if found {
		props.ReleaseChannel = releaseChannel
	}

	if source, found, err := unstructured.NestedString(data, "source"); err != nil {
		return err
	} else if found {
		props.Source = source
	}

	if stage, found, err := unstructured.NestedString(data, "stage"); err != nil {
		return err
	} else if found {
		props.Stage = stage
	}

	if subsystems, found, err := unstructured.NestedStringSlice(data, "subsystems"); err != nil {
		return err
	} else if found {
		props.Subsystems = subsystems
	}

	if version, found, err := unstructured.NestedString(data, "version"); err != nil {
		return err
	} else if found {
		props.Version = version
	}

	if weight, found, err := unstructured.NestedInt64(data, "weight"); err != nil {
		return err
	} else if found {
		props.Weight = int(weight)
	}

	return nil
}

// extractModuleStatus extracts ModuleStatus from a map
func extractModuleStatus(data map[string]interface{}, status *ModuleStatus) error {
	if conditions, found, err := unstructured.NestedSlice(data, "conditions"); err != nil {
		return err
	} else if found {
		status.Conditions = make([]ModuleCondition, 0, len(conditions))
		for _, cond := range conditions {
			condMap, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}
			condition := ModuleCondition{}
			if lastProbeTime, found, err := unstructured.NestedString(condMap, "lastProbeTime"); err != nil {
				return err
			} else if found {
				if t, err := time.Parse(time.RFC3339, lastProbeTime); err == nil {
					condition.LastProbeTime = metav1.NewTime(t)
				}
			}
			if lastTransitionTime, found, err := unstructured.NestedString(condMap, "lastTransitionTime"); err != nil {
				return err
			} else if found {
				if t, err := time.Parse(time.RFC3339, lastTransitionTime); err == nil {
					condition.LastTransitionTime = metav1.NewTime(t)
				}
			}
			if statusStr, found, err := unstructured.NestedString(condMap, "status"); err != nil {
				return err
			} else if found {
				condition.Status = statusStr
			}
			if typeStr, found, err := unstructured.NestedString(condMap, "type"); err != nil {
				return err
			} else if found {
				condition.Type = typeStr
			}
			status.Conditions = append(status.Conditions, condition)
		}
	}

	if hooksState, found, err := unstructured.NestedString(data, "hooksState"); err != nil {
		return err
	} else if found {
		status.HooksState = hooksState
	}

	if phase, found, err := unstructured.NestedString(data, "phase"); err != nil {
		return err
	} else if found {
		status.Phase = phase
	}

	return nil
}

// GetModuleConfig retrieves detailed information about a ModuleConfig by name
func GetModuleConfig(ctx context.Context, config *rest.Config, moduleName string) (*ModuleConfig, error) {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "deckhouse.io",
		Version:  "v1alpha1",
		Resource: ModuleConfigResource,
	}

	// ModuleConfig is a cluster-scoped resource, so we use empty namespace
	unstructuredObj, err := client.Resource(gvr).Get(ctx, moduleName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get moduleconfig %s: %w", moduleName, err)
	}

	moduleConfig, err := unstructuredToModuleConfig(unstructuredObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured moduleconfig %s: %w", moduleName, err)
	}

	return moduleConfig, nil
}

// GetModulePullOverride retrieves detailed information about a ModulePullOverride by name
func GetModulePullOverride(ctx context.Context, config *rest.Config, moduleName string) (*ModulePullOverride, error) {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "deckhouse.io",
		Version:  "v1alpha2",
		Resource: ModulePullOverrideResource,
	}

	// ModulePullOverride is a cluster-scoped resource, so we use empty namespace
	unstructuredObj, err := client.Resource(gvr).Get(ctx, moduleName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get modulepulloverride %s: %w", moduleName, err)
	}

	modulePullOverride, err := unstructuredToModulePullOverride(unstructuredObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured modulepulloverride %s: %w", moduleName, err)
	}

	return modulePullOverride, nil
}

// unstructuredToModuleConfig converts an unstructured.Unstructured object to a ModuleConfig struct
func unstructuredToModuleConfig(obj *unstructured.Unstructured) (*ModuleConfig, error) {
	moduleConfig := &ModuleConfig{}

	// Set TypeMeta
	moduleConfig.APIVersion = obj.GetAPIVersion()
	moduleConfig.Kind = obj.GetKind()

	// Set ObjectMeta
	moduleConfig.ObjectMeta = metav1.ObjectMeta{
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		UID:               obj.GetUID(),
		ResourceVersion:   obj.GetResourceVersion(),
		Generation:        obj.GetGeneration(),
		CreationTimestamp: obj.GetCreationTimestamp(),
		Labels:            obj.GetLabels(),
		Annotations:       obj.GetAnnotations(),
		Finalizers:        obj.GetFinalizers(),
	}

	// Extract spec
	if spec, found, err := unstructured.NestedMap(obj.Object, "spec"); err != nil {
		return nil, fmt.Errorf("failed to extract spec: %w", err)
	} else if found {
		if err := extractModuleConfigSpec(spec, &moduleConfig.Spec); err != nil {
			return nil, fmt.Errorf("failed to extract moduleconfig spec: %w", err)
		}
	}

	// Extract status
	if status, found, err := unstructured.NestedMap(obj.Object, "status"); err != nil {
		return nil, fmt.Errorf("failed to extract status: %w", err)
	} else if found {
		if err := extractModuleConfigStatus(status, &moduleConfig.Status); err != nil {
			return nil, fmt.Errorf("failed to extract moduleconfig status: %w", err)
		}
	}

	return moduleConfig, nil
}

// extractModuleConfigSpec extracts ModuleConfigSpec from a map
func extractModuleConfigSpec(data map[string]interface{}, spec *ModuleConfigSpec) error {
	if enabled, found, err := unstructured.NestedBool(data, "enabled"); err != nil {
		return err
	} else if found {
		spec.Enabled = enabled
	}

	if settings, found, err := unstructured.NestedMap(data, "settings"); err != nil {
		return err
	} else if found {
		spec.Settings = settings
	}

	if version, found, err := unstructured.NestedInt64(data, "version"); err != nil {
		return err
	} else if found {
		spec.Version = int(version)
	}

	return nil
}

// extractModuleConfigStatus extracts ModuleConfigStatus from a map
func extractModuleConfigStatus(data map[string]interface{}, status *ModuleConfigStatus) error {
	if message, found, err := unstructured.NestedString(data, "message"); err != nil {
		return err
	} else if found {
		status.Message = message
	}

	if version, found, err := unstructured.NestedString(data, "version"); err != nil {
		return err
	} else if found {
		status.Version = version
	}

	return nil
}

// unstructuredToModulePullOverride converts an unstructured.Unstructured object to a ModulePullOverride struct
func unstructuredToModulePullOverride(obj *unstructured.Unstructured) (*ModulePullOverride, error) {
	modulePullOverride := &ModulePullOverride{}

	// Set TypeMeta
	modulePullOverride.APIVersion = obj.GetAPIVersion()
	modulePullOverride.Kind = obj.GetKind()

	// Set ObjectMeta
	modulePullOverride.ObjectMeta = metav1.ObjectMeta{
		Name:              obj.GetName(),
		Namespace:         obj.GetNamespace(),
		UID:               obj.GetUID(),
		ResourceVersion:   obj.GetResourceVersion(),
		Generation:        obj.GetGeneration(),
		CreationTimestamp: obj.GetCreationTimestamp(),
		Labels:            obj.GetLabels(),
		Annotations:       obj.GetAnnotations(),
		Finalizers:        obj.GetFinalizers(),
	}

	// Extract spec
	if spec, found, err := unstructured.NestedMap(obj.Object, "spec"); err != nil {
		return nil, fmt.Errorf("failed to extract spec: %w", err)
	} else if found {
		if err := extractModulePullOverrideSpec(spec, &modulePullOverride.Spec); err != nil {
			return nil, fmt.Errorf("failed to extract modulepulloverride spec: %w", err)
		}
	}

	// Extract status
	if status, found, err := unstructured.NestedMap(obj.Object, "status"); err != nil {
		return nil, fmt.Errorf("failed to extract status: %w", err)
	} else if found {
		if err := extractModulePullOverrideStatus(status, &modulePullOverride.Status); err != nil {
			return nil, fmt.Errorf("failed to extract modulepulloverride status: %w", err)
		}
	}

	return modulePullOverride, nil
}

// extractModulePullOverrideSpec extracts ModulePullOverrideSpec from a map
func extractModulePullOverrideSpec(data map[string]interface{}, spec *ModulePullOverrideSpec) error {
	if imageTag, found, err := unstructured.NestedString(data, "imageTag"); err != nil {
		return err
	} else if found {
		spec.ImageTag = imageTag
	}

	if rollback, found, err := unstructured.NestedBool(data, "rollback"); err != nil {
		return err
	} else if found {
		spec.Rollback = rollback
	}

	if scanInterval, found, err := unstructured.NestedString(data, "scanInterval"); err != nil {
		return err
	} else if found {
		spec.ScanInterval = scanInterval
	}

	return nil
}

// extractModulePullOverrideStatus extracts ModulePullOverrideStatus from a map
func extractModulePullOverrideStatus(data map[string]interface{}, status *ModulePullOverrideStatus) error {
	if imageDigest, found, err := unstructured.NestedString(data, "imageDigest"); err != nil {
		return err
	} else if found {
		status.ImageDigest = imageDigest
	}

	if message, found, err := unstructured.NestedString(data, "message"); err != nil {
		return err
	} else if found {
		status.Message = message
	}

	if updatedAt, found, err := unstructured.NestedString(data, "updatedAt"); err != nil {
		return err
	} else if found {
		status.UpdatedAt = updatedAt
	}

	if weight, found, err := unstructured.NestedInt64(data, "weight"); err != nil {
		return err
	} else if found {
		status.Weight = int(weight)
	}

	return nil
}
