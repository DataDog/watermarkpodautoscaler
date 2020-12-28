// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package v1alpha1

import (
	autoscalingv2 "k8s.io/api/autoscaling/v2beta1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WatermarkPodAutoscaler is the Schema for the watermarkpodautoscalers API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="value",type="string",JSONPath=".status.currentMetrics[*].external.currentValue.."
// +kubebuilder:printcolumn:name="high watermark",type="string",JSONPath=".spec.metrics[*].external.highWatermark.."
// +kubebuilder:printcolumn:name="low watermark",type="string",JSONPath=".spec.metrics[*].external.lowWatermark.."
// +kubebuilder:printcolumn:name="age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="min replicas",type="integer",JSONPath=".spec.minReplicas"
// +kubebuilder:printcolumn:name="max replicas",type="integer",JSONPath=".spec.maxReplicas"
// +kubebuilder:printcolumn:name="dry-run",type="string",JSONPath=".spec.dryRun"
// +kubebuilder:resource:path=watermarkpodautoscalers,shortName=wpa
type WatermarkPodAutoscaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WatermarkPodAutoscalerSpec   `json:"spec,omitempty"`
	Status WatermarkPodAutoscalerStatus `json:"status,omitempty"`
}

// CrossVersionObjectReference contains enough information to let you identify the referred resource.
// +k8s:openapi-gen=true
type CrossVersionObjectReference struct {
	// Kind of the referent; More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds"
	Kind string `json:"kind"`
	// Name of the referent; More info: http://kubernetes.io/docs/user-guide/identifiers#names
	Name string `json:"name"`
	// API version of the referent
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`
}

// WatermarkPodAutoscalerSpec defines the desired state of WatermarkPodAutoscaler
// +k8s:openapi-gen=true
type WatermarkPodAutoscalerSpec struct {

	// part of HorizontalController, see comments in the k8s repo: pkg/controller/podautoscaler/horizontal.go
	// +kubebuilder:validation:Minimum=1
	DownscaleForbiddenWindowSeconds int32 `json:"downscaleForbiddenWindowSeconds,omitempty"`

	// +kubebuilder:validation:Minimum=1
	UpscaleForbiddenWindowSeconds int32 `json:"upscaleForbiddenWindowSeconds,omitempty"`

	// Percentage of replicas that can be added in an upscale event.
	// Parameter used to be a float, in order to support the transition seamlessly, we validate that it is ]0;100] in the code.
	ScaleUpLimitFactor resource.Quantity `json:"scaleUpLimitFactor,omitempty"`

	// Percentage of replicas that can be removed in an downscale event.
	// Parameter used to be a float, in order to support the transition seamlessly, we validate that it is ]0;100[ in the code.
	ScaleDownLimitFactor resource.Quantity `json:"scaleDownLimitFactor,omitempty"`

	// Parameter used to be a float, in order to support the transition seamlessly, we validate that it is ]0;1[ in the code.
	Tolerance resource.Quantity `json:"tolerance,omitempty"`

	// computed values take the # of replicas into account
	Algorithm string `json:"algorithm,omitempty"`

	// Whether planned scale changes are actually applied
	DryRun bool `json:"dryRun,omitempty"`

	// part of HorizontalPodAutoscalerSpec, see comments in the k8s-1.10.8 repo: staging/src/k8s.io/api/autoscaling/v1/types.go
	// reference to scaled resource; horizontal pod autoscaler will learn the current resource consumption
	// and will set the desired number of pods by using its Scale subresource.
	ScaleTargetRef CrossVersionObjectReference `json:"scaleTargetRef"`
	// specifications that will be used to calculate the desired replica count
	// +listType=set
	Metrics []MetricSpec `json:"metrics,omitempty"`
	// +kubebuilder:validation:Minimum=1
	MinReplicas *int32 `json:"minReplicas,omitempty"`
	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas,omitempty"`
	// +kubebuilder:validation:Minimum=1
	ReadinessDelaySeconds int32 `json:"readinessDelay,omitempty"`
}

// ExternalMetricSource indicates how to scale on a metric not associated with
// any Kubernetes object (for example length of queue in cloud
// messaging service, or QPS from loadbalancer running outside of cluster).
// Exactly one "target" type should be set.
// +k8s:openapi-gen=true
type ExternalMetricSource struct {
	// metricName is the name of the metric in question.
	MetricName string `json:"metricName"`
	// metricSelector is used to identify a specific time series
	// within a given metric.
	// +optional
	MetricSelector *metav1.LabelSelector `json:"metricSelector,omitempty"`

	HighWatermark *resource.Quantity `json:"highWatermark,omitempty"`
	LowWatermark  *resource.Quantity `json:"lowWatermark,omitempty"`
}

// ResourceMetricSource indicates how to scale on a resource metric known to
// Kubernetes, as specified in requests and limits, describing each pod in the
// current scale target (e.g. CPU or memory).  The values will be averaged
// together before being compared to the target.  Such metrics are built in to
// Kubernetes, and have special scaling options on top of those available to
// normal per-pod metrics using the "pods" source.  Only one "target" type
// should be set.
// +k8s:openapi-gen=true
type ResourceMetricSource struct {
	// name is the name of the resource in question.
	Name v1.ResourceName `json:"name"`
	// metricSelector is used to identify a specific time series
	// within a given metric.
	// +optional
	MetricSelector *metav1.LabelSelector `json:"metricSelector,omitempty"`

	HighWatermark *resource.Quantity `json:"highWatermark,omitempty"`
	LowWatermark  *resource.Quantity `json:"lowWatermark,omitempty"`
}

// MetricSourceType indicates the type of metric.
type MetricSourceType string

var (
	// ExternalMetricSourceType is a global metric that is not associated
	// with any Kubernetes object. It allows autoscaling based on information
	// coming from components running outside of cluster
	// (for example length of queue in cloud messaging service, or
	// QPS from loadbalancer running outside of cluster).
	ExternalMetricSourceType MetricSourceType = "External"

	// ResourceMetricSourceType is a resource metric known to Kubernetes, as
	// specified in requests and limits, describing each pod in the current
	// scale target (e.g. CPU or memory).  Such metrics are built in to
	// Kubernetes, and have special scaling options on top of those available
	// to normal per-pod metrics (the "pods" source).
	ResourceMetricSourceType MetricSourceType = "Resource"
)

// MetricSpec specifies how to scale based on a single metric
// (only `type` and one other matching field should be set at once).
// +k8s:openapi-gen=true
type MetricSpec struct {
	// type is the type of metric source.  It should be one of "Object",
	// "Pods" or "Resource", each mapping to a matching field in the object.
	Type MetricSourceType `json:"type"`
	// external refers to a global metric that is not associated
	// with any Kubernetes object. It allows autoscaling based on information
	// coming from components running outside of cluster
	// (for example length of queue in cloud messaging service, or
	// QPS from loadbalancer running outside of cluster).
	// +optional
	External *ExternalMetricSource `json:"external,omitempty"`
	// resource refers to a resource metric (such as those specified in
	// requests and limits) known to Kubernetes describing each pod in the
	// current scale target (e.g. CPU or memory). Such metrics are built in to
	// Kubernetes, and have special scaling options on top of those available
	// to normal per-pod metrics using the "pods" source.
	// +optional
	Resource *ResourceMetricSource `json:"resource,omitempty"`
}

// WatermarkPodAutoscalerStatus defines the observed state of WatermarkPodAutoscaler
// +k8s:openapi-gen=true
type WatermarkPodAutoscalerStatus struct {
	ObservedGeneration *int64       `json:"observedGeneration,omitempty"`
	LastScaleTime      *metav1.Time `json:"lastScaleTime,omitempty"`
	CurrentReplicas    int32        `json:"currentReplicas"`
	DesiredReplicas    int32        `json:"desiredReplicas"`
	// +listType=set
	CurrentMetrics []autoscalingv2.MetricStatus `json:"currentMetrics"`
	// +listType=set
	Conditions []autoscalingv2.HorizontalPodAutoscalerCondition `json:"conditions"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WatermarkPodAutoscalerList contains a list of WatermarkPodAutoscaler
// +k8s:openapi-gen=true
type WatermarkPodAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// +listType=set
	Items []WatermarkPodAutoscaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WatermarkPodAutoscaler{}, &WatermarkPodAutoscalerList{})
}
