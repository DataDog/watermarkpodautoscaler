package v1alpha1

import (
	autoscalingv2 "k8s.io/api/autoscaling/v2beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"

)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// CrossVersionObjectReference contains enough information to let you identify the referred resource.
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

	// +kubebuilder:validation:Minimum=0.01
	// +kubebuilder:validation:Maximum=0.99
	Tolerance float64 `json:"tolerance,omitempty"`

	Algorithm string `json:"algorithm,omitempty"`

	// part of HorizontalPodAutoscalerSpec, see comments in the k8s-1.10.8 repo: staging/src/k8s.io/api/autoscaling/v1/types.go
	// reference to scaled resource; horizontal pod autoscaler will learn the current resource consumption
	// and will set the desired number of pods by using its Scale subresource.
	ScaleTargetRef CrossVersionObjectReference `json:"scaleTargetRef"`
	// specifications that will be used to calculate the desired replica count
	Metrics []MetricSpec `json:"metrics,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	MinReplicas *int32 `json:"minReplicas,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

}

// ExternalMetricSource indicates how to scale on a metric not associated with
// any Kubernetes object (for example length of queue in cloud
// messaging service, or QPS from loadbalancer running outside of cluster).
// Exactly one "target" type should be set.
type ExternalMetricSource struct {
	// metricName is the name of the metric in question.
	MetricName string `json:"metricName" protobuf:"bytes,1,name=metricName"`
	// metricSelector is used to identify a specific time series
	// within a given metric.
	// +optional
	MetricSelector *metav1.LabelSelector `json:"metricSelector,omitempty" protobuf:"bytes,2,opt,name=metricSelector"`

	HighWatermark *resource.Quantity  `json:"highWatermark,omitempty"`
	LowWatermark *resource.Quantity  `json:"lowWatermark,omitempty"`
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
)

// MetricSpec specifies how to scale based on a single metric
// (only `type` and one other matching field should be set at once).
type MetricSpec struct {
	// type is the type of metric source.  It should be one of "Object",
	// "Pods" or "Resource", each mapping to a matching field in the object.
	Type MetricSourceType `json:"type" protobuf:"bytes,1,name=type"`
	// external refers to a global metric that is not associated
	// with any Kubernetes object. It allows autoscaling based on information
	// coming from components running outside of cluster
	// (for example length of queue in cloud messaging service, or
	// QPS from loadbalancer running outside of cluster).
	// +optional
	External *ExternalMetricSource `json:"external,omitempty" protobuf:"bytes,5,opt,name=external"`
}


// WatermarkPodAutoscalerStatus defines the observed state of WatermarkPodAutoscaler
// +k8s:openapi-gen=true
type WatermarkPodAutoscalerStatus struct {
	ObservedGeneration *int64                                           `json:"observedGeneration,omitempty"`
	LastScaleTime      *metav1.Time                                     `json:"lastScaleTime,omitempty"`
	CurrentReplicas    int32                                            `json:"currentReplicas"`
	DesiredReplicas    int32                                            `json:"desiredReplicas"`
	CurrentMetrics     []autoscalingv2.MetricStatus                     `json:"currentMetrics"`
	Conditions         []autoscalingv2.HorizontalPodAutoscalerCondition `json:"conditions"`

	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WatermarkPodAutoscaler is the Schema for the watermarkpodautoscalers API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="status",type="string",JSONPath=".status.LastScaleTime"
// +kubebuilder:printcolumn:name="age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="min replicas",type="integer",JSONPath=".spec.minReplicas"
// +kubebuilder:printcolumn:name="max replicas",type="integer",JSONPath=".spec.maxReplicas"
// +kubebuilder:resource:path=watermarkpodautoscaler,shortName=wpa
type WatermarkPodAutoscaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WatermarkPodAutoscalerSpec   `json:"spec,omitempty"`
	Status WatermarkPodAutoscalerStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WatermarkPodAutoscalerList contains a list of WatermarkPodAutoscaler
type WatermarkPodAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WatermarkPodAutoscaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WatermarkPodAutoscaler{}, &WatermarkPodAutoscalerList{})
}
