package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// WatermarkPodAutoscalerSpec defines the desired state of WatermarkPodAutoscaler
// +k8s:openapi-gen=true
type WatermarkPodAutoscalerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book.kubebuilder.io/beyond_basics/generating_crd.html
}

// WatermarkPodAutoscalerStatus defines the observed state of WatermarkPodAutoscaler
// +k8s:openapi-gen=true
type WatermarkPodAutoscalerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WatermarkPodAutoscaler is the Schema for the watermarkpodautoscalers API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
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
