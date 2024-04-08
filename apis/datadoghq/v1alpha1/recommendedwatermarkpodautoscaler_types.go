// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RecommendedWatermarkPodAutoscalerSpec defines the desired state of RecommendedWatermarkPodAutoscaler
type RecommendedWatermarkPodAutoscalerSpec struct {
	// part of HorizontalPodAutoscalerSpec, see comments in the k8s-1.10.8 repo: staging/src/k8s.io/api/autoscaling/v1/types.go
	// reference to scaled resource; horizontal pod autoscaler will learn the current resource consumption
	// and will set the desired number of pods by using its Scale subresource.
	ScaleTargetRef CrossVersionObjectReference `json:"scaleTargetRef"`

	// Profile is the scaling profile to use for the recommended watermark pod autoscaler configuration (default: cpu)
	// +kubebuilder:default=cpu
	// +optional
	Profile ScalingProfile `json:"profile,omitempty"`

	// CommonLabelsSelector is the common labels selector to use for the recommended watermark pod autoscaler configuration
	LabelsSelector metav1.LabelSelector `json:"commonLabelsSelector"`

	ExtraTags []string `json:"extraTags,omitempty"`

	// MultiClustersBias is the configuration for the multi-clusters bias
	// +optional
	MultiClustersBias *MultiClustersBias `json:"multiClustersBias,omitempty"`
}

// RecommendedWatermarkPodAutoscalerStatus defines the observed state of RecommendedWatermarkPodAutoscaler
type RecommendedWatermarkPodAutoscalerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// RecommendedWatermarkPodAutoscalerConditionType is the type of the recommended watermark pod autoscaler condition
type RecommendedWatermarkPodAutoscalerConditionType string

const (
	RecommendedWatermarkPodAutoscalerConditionTypeReconcileSuccess RecommendedWatermarkPodAutoscalerConditionType = "Success"
	RecommendedWatermarkPodAutoscalerConditionTypeReconcileMetrics RecommendedWatermarkPodAutoscalerConditionType = "ReconcileDatadogMetric"
	RecommendedWatermarkPodAutoscalerConditionTypeReconcileWPA     RecommendedWatermarkPodAutoscalerConditionType = "ReconcileWPA"
)

// ScalingProfile is the scaling profile to use for the recommended watermark pod autoscaler configuration
type ScalingProfile string

const (
	// ScalingProfileDefault is the default scaling profile
	ScalingProfileCPU ScalingProfile = "cpu"
)

type MultiClustersBias struct {
	// Enabled is the flag to enable the multi-clusters bias support
	Enabled bool `json:"enabled"`

	// CommonLabelsSelector is the common labels selector to use for the recommended watermark pod autoscaler configuration
	CommonLabelsSelector metav1.LabelSelector `json:"commonLabelsSelector"`

	// CommonTags is the common tags to use for the recommended watermark pod autoscaler configuration
	// +optional
	CommonTags []string `json:"commonTags,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// RecommendedWatermarkPodAutoscaler is the Schema for the recommendedwatermarkpodautoscalers API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="valide",type="string",JSONPath=".status.conditions[?(@.type==\"Valid\")].status"
type RecommendedWatermarkPodAutoscaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RecommendedWatermarkPodAutoscalerSpec   `json:"spec,omitempty"`
	Status RecommendedWatermarkPodAutoscalerStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RecommendedWatermarkPodAutoscalerList contains a list of RecommendedWatermarkPodAutoscaler
type RecommendedWatermarkPodAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RecommendedWatermarkPodAutoscaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RecommendedWatermarkPodAutoscaler{}, &RecommendedWatermarkPodAutoscalerList{})
}
