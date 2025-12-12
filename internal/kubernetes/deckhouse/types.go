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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Module represents a Deckhouse Module custom resource
type Module struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Properties        ModuleProperties `json:"properties,omitzero"`
	Status            ModuleStatus     `json:"status,omitzero"`
}

// ModuleProperties contains the properties of a Module
type ModuleProperties struct {
	Critical       bool            `json:"critical,omitempty"`
	DisableOptions *DisableOptions `json:"disableOptions,omitzero"`
	Namespace      string          `json:"namespace,omitempty"`
	ReleaseChannel string          `json:"releaseChannel,omitempty"`
	Source         string          `json:"source,omitempty"`
	Stage          string          `json:"stage,omitempty"`
	Subsystems     []string        `json:"subsystems,omitempty"`
	Version        string          `json:"version,omitempty"`
	Weight         int             `json:"weight,omitempty"`
}

// DisableOptions contains options for disabling a module
type DisableOptions struct {
	Confirmation bool   `json:"confirmation,omitempty"`
	Message      string `json:"message,omitempty"`
}

// ModuleStatus contains the status of a Module
type ModuleStatus struct {
	Conditions []ModuleCondition `json:"conditions,omitzero"`
	HooksState string            `json:"hooksState,omitempty"`
	Phase      string            `json:"phase,omitempty"`
}

// ModuleCondition represents a condition of a Module
type ModuleCondition struct {
	LastProbeTime      metav1.Time `json:"lastProbeTime,omitzero"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitzero"`
	Status             string      `json:"status,omitempty"`
	Type               string      `json:"type,omitempty"`
}

// ModuleConfig represents a Deckhouse ModuleConfig custom resource
type ModuleConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ModuleConfigSpec   `json:"spec,omitzero"`
	Status            ModuleConfigStatus `json:"status,omitzero"`
}

// ModuleConfigSpec contains the specification of a ModuleConfig
type ModuleConfigSpec struct {
	Enabled  bool                   `json:"enabled,omitempty"`
	Settings map[string]interface{} `json:"settings,omitempty"`
	Version  int                    `json:"version,omitempty"`
}

// ModuleConfigStatus contains the status of a ModuleConfig
type ModuleConfigStatus struct {
	Message string `json:"message,omitempty"`
	Version string `json:"version,omitempty"`
}

// ModulePullOverride represents a Deckhouse ModulePullOverride custom resource
type ModulePullOverride struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ModulePullOverrideSpec   `json:"spec,omitzero"`
	Status            ModulePullOverrideStatus `json:"status,omitzero"`
}

// ModulePullOverrideSpec contains the specification of a ModulePullOverride
type ModulePullOverrideSpec struct {
	ImageTag     string `json:"imageTag,omitempty"`
	Rollback     bool   `json:"rollback,omitempty"`
	ScanInterval string `json:"scanInterval,omitempty"`
}

// ModulePullOverrideStatus contains the status of a ModulePullOverride
type ModulePullOverrideStatus struct {
	ImageDigest string `json:"imageDigest,omitempty"`
	Message     string `json:"message,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Weight      int    `json:"weight,omitempty"`
}
