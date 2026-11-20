// +kubebuilder:object:generate=true
package v1alpha1

import (
	ocmv1alpha1 "ocm.software/open-component-model/bindings/go/kubernetes/controller/api/v1alpha1"
)

type LabelConfig struct {
	// +kubebuilder:validation:Required
	Key string `json:"key"`
	// +kubebuilder:validation:Required
	Value string `json:"value"`
}

// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:MaxProperties=1
type ResourceConfig struct {
	// +kubebuilder:validation:Optional
	Name string `json:"name,omitempty"`
	// +kubebuilder:validation:Optional
	ResourceType string `json:"resourceType,omitempty"`
	// +kubebuilder:validation:Optional
	Label *LabelConfig `json:"label,omitempty"`
}

type SelectorConfig struct {
	// +kubebuilder:validation:Optional
	ComponentSelector *ocmv1alpha1.Selector `json:"componentSelector,omitempty"`
	// +kubebuilder:validation:Optional
	ResourceSelector *ocmv1alpha1.Selector `json:"resourceSelector,omitempty"`
}

type DiscoveryConfig struct {
	// +kubebuilder:validation:Optional
	SelectorConfig *SelectorConfig `json:"selectorConfig,omitempty"`
	// +kubebuilder:validation:Optional
	ComponentMatchLabelKey string `json:"componentMatchLabelKey,omitempty"`
	// +kubebuilder:validation:Optional
	ResourceConfig map[string]ResourceConfig `json:"resourceConfig,omitempty"`
}
