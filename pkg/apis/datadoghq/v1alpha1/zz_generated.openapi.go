// +build !ignore_autogenerated

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// This file was autogenerated by openapi-gen. Do not edit it manually!

package v1alpha1

import (
	spec "github.com/go-openapi/spec"
	common "k8s.io/kube-openapi/pkg/common"
)

func GetOpenAPIDefinitions(ref common.ReferenceCallback) map[string]common.OpenAPIDefinition {
	return map[string]common.OpenAPIDefinition{
		"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.CrossVersionObjectReference":  schema_pkg_apis_datadoghq_v1alpha1_CrossVersionObjectReference(ref),
		"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.ExternalMetricSource":         schema_pkg_apis_datadoghq_v1alpha1_ExternalMetricSource(ref),
		"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.MetricSpec":                   schema_pkg_apis_datadoghq_v1alpha1_MetricSpec(ref),
		"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscaler":       schema_pkg_apis_datadoghq_v1alpha1_WatermarkPodAutoscaler(ref),
		"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscalerList":   schema_pkg_apis_datadoghq_v1alpha1_WatermarkPodAutoscalerList(ref),
		"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscalerSpec":   schema_pkg_apis_datadoghq_v1alpha1_WatermarkPodAutoscalerSpec(ref),
		"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscalerStatus": schema_pkg_apis_datadoghq_v1alpha1_WatermarkPodAutoscalerStatus(ref),
	}
}

func schema_pkg_apis_datadoghq_v1alpha1_CrossVersionObjectReference(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "CrossVersionObjectReference contains enough information to let you identify the referred resource.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "Kind of the referent; More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds\"",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"name": {
						SchemaProps: spec.SchemaProps{
							Description: "Name of the referent; More info: http://kubernetes.io/docs/user-guide/identifiers#names",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"apiVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "API version of the referent",
							Type:        []string{"string"},
							Format:      "",
						},
					},
				},
				Required: []string{"kind", "name"},
			},
		},
	}
}

func schema_pkg_apis_datadoghq_v1alpha1_ExternalMetricSource(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "ExternalMetricSource indicates how to scale on a metric not associated with any Kubernetes object (for example length of queue in cloud messaging service, or QPS from loadbalancer running outside of cluster). Exactly one \"target\" type should be set.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"metricName": {
						SchemaProps: spec.SchemaProps{
							Description: "metricName is the name of the metric in question.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"metricSelector": {
						SchemaProps: spec.SchemaProps{
							Description: "metricSelector is used to identify a specific time series within a given metric.",
							Ref:         ref("k8s.io/apimachinery/pkg/apis/meta/v1.LabelSelector"),
						},
					},
					"highWatermark": {
						SchemaProps: spec.SchemaProps{
							Ref: ref("k8s.io/apimachinery/pkg/api/resource.Quantity"),
						},
					},
					"lowWatermark": {
						SchemaProps: spec.SchemaProps{
							Ref: ref("k8s.io/apimachinery/pkg/api/resource.Quantity"),
						},
					},
				},
				Required: []string{"metricName"},
			},
		},
		Dependencies: []string{
			"k8s.io/apimachinery/pkg/api/resource.Quantity", "k8s.io/apimachinery/pkg/apis/meta/v1.LabelSelector"},
	}
}

func schema_pkg_apis_datadoghq_v1alpha1_MetricSpec(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "MetricSpec specifies how to scale based on a single metric (only `type` and one other matching field should be set at once).",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"type": {
						SchemaProps: spec.SchemaProps{
							Description: "type is the type of metric source.  It should be one of \"Object\", \"Pods\" or \"Resource\", each mapping to a matching field in the object.",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"external": {
						SchemaProps: spec.SchemaProps{
							Description: "external refers to a global metric that is not associated with any Kubernetes object. It allows autoscaling based on information coming from components running outside of cluster (for example length of queue in cloud messaging service, or QPS from loadbalancer running outside of cluster).",
							Ref:         ref("github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.ExternalMetricSource"),
						},
					},
				},
				Required: []string{"type"},
			},
		},
		Dependencies: []string{
			"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.ExternalMetricSource"},
	}
}

func schema_pkg_apis_datadoghq_v1alpha1_WatermarkPodAutoscaler(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "WatermarkPodAutoscaler is the Schema for the watermarkpodautoscalers API",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"apiVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#resources",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"metadata": {
						SchemaProps: spec.SchemaProps{
							Ref: ref("k8s.io/apimachinery/pkg/apis/meta/v1.ObjectMeta"),
						},
					},
					"spec": {
						SchemaProps: spec.SchemaProps{
							Ref: ref("github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscalerSpec"),
						},
					},
					"status": {
						SchemaProps: spec.SchemaProps{
							Ref: ref("github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscalerStatus"),
						},
					},
				},
			},
		},
		Dependencies: []string{
			"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscalerSpec", "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscalerStatus", "k8s.io/apimachinery/pkg/apis/meta/v1.ObjectMeta"},
	}
}

func schema_pkg_apis_datadoghq_v1alpha1_WatermarkPodAutoscalerList(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "WatermarkPodAutoscalerList contains a list of WatermarkPodAutoscaler",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"apiVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#resources",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"metadata": {
						SchemaProps: spec.SchemaProps{
							Ref: ref("k8s.io/apimachinery/pkg/apis/meta/v1.ListMeta"),
						},
					},
					"items": {
						VendorExtensible: spec.VendorExtensible{
							Extensions: spec.Extensions{
								"x-kubernetes-list-type": "set",
							},
						},
						SchemaProps: spec.SchemaProps{
							Type: []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Ref: ref("github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscaler"),
									},
								},
							},
						},
					},
				},
				Required: []string{"items"},
			},
		},
		Dependencies: []string{
			"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.WatermarkPodAutoscaler", "k8s.io/apimachinery/pkg/apis/meta/v1.ListMeta"},
	}
}

func schema_pkg_apis_datadoghq_v1alpha1_WatermarkPodAutoscalerSpec(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "WatermarkPodAutoscalerSpec defines the desired state of WatermarkPodAutoscaler",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"downscaleForbiddenWindowSeconds": {
						SchemaProps: spec.SchemaProps{
							Description: "part of HorizontalController, see comments in the k8s repo: pkg/controller/podautoscaler/horizontal.go",
							Type:        []string{"integer"},
							Format:      "int32",
						},
					},
					"upscaleForbiddenWindowSeconds": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"integer"},
							Format: "int32",
						},
					},
					"scaleUpLimitFactor": {
						SchemaProps: spec.SchemaProps{
							Description: "Percentage of replicas that can be added in an upscale event. Max value will set the limit at the Maximum number of Replicas.",
							Type:        []string{"number"},
							Format:      "double",
						},
					},
					"scaleDownLimitFactor": {
						SchemaProps: spec.SchemaProps{
							Description: "Percentage of replicas that can be added in an upscale event. Max value will set the limit at the Maximum number of Replicas.",
							Type:        []string{"number"},
							Format:      "double",
						},
					},
					"tolerance": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"number"},
							Format: "double",
						},
					},
					"algorithm": {
						SchemaProps: spec.SchemaProps{
							Description: "computed values take the # of replicas into account",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"dryRun": {
						SchemaProps: spec.SchemaProps{
							Description: "Wether planned scale changes are actually applied",
							Type:        []string{"boolean"},
							Format:      "",
						},
					},
					"scaleTargetRef": {
						SchemaProps: spec.SchemaProps{
							Description: "part of HorizontalPodAutoscalerSpec, see comments in the k8s-1.10.8 repo: staging/src/k8s.io/api/autoscaling/v1/types.go reference to scaled resource; horizontal pod autoscaler will learn the current resource consumption and will set the desired number of pods by using its Scale subresource.",
							Ref:         ref("github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.CrossVersionObjectReference"),
						},
					},
					"metrics": {
						VendorExtensible: spec.VendorExtensible{
							Extensions: spec.Extensions{
								"x-kubernetes-list-type": "set",
							},
						},
						SchemaProps: spec.SchemaProps{
							Description: "specifications that will be used to calculate the desired replica count",
							Type:        []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Ref: ref("github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.MetricSpec"),
									},
								},
							},
						},
					},
					"minReplicas": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"integer"},
							Format: "int32",
						},
					},
					"maxReplicas": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"integer"},
							Format: "int32",
						},
					},
				},
				Required: []string{"scaleTargetRef"},
			},
		},
		Dependencies: []string{
			"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.CrossVersionObjectReference", "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1.MetricSpec"},
	}
}

func schema_pkg_apis_datadoghq_v1alpha1_WatermarkPodAutoscalerStatus(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "WatermarkPodAutoscalerStatus defines the observed state of WatermarkPodAutoscaler",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"observedGeneration": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"integer"},
							Format: "int64",
						},
					},
					"lastScaleTime": {
						SchemaProps: spec.SchemaProps{
							Ref: ref("k8s.io/apimachinery/pkg/apis/meta/v1.Time"),
						},
					},
					"currentReplicas": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"integer"},
							Format: "int32",
						},
					},
					"desiredReplicas": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"integer"},
							Format: "int32",
						},
					},
					"currentMetrics": {
						VendorExtensible: spec.VendorExtensible{
							Extensions: spec.Extensions{
								"x-kubernetes-list-type": "set",
							},
						},
						SchemaProps: spec.SchemaProps{
							Type: []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Ref: ref("k8s.io/api/autoscaling/v2beta1.MetricStatus"),
									},
								},
							},
						},
					},
					"conditions": {
						VendorExtensible: spec.VendorExtensible{
							Extensions: spec.Extensions{
								"x-kubernetes-list-type": "set",
							},
						},
						SchemaProps: spec.SchemaProps{
							Type: []string{"array"},
							Items: &spec.SchemaOrArray{
								Schema: &spec.Schema{
									SchemaProps: spec.SchemaProps{
										Ref: ref("k8s.io/api/autoscaling/v2beta1.HorizontalPodAutoscalerCondition"),
									},
								},
							},
						},
					},
				},
				Required: []string{"currentReplicas", "desiredReplicas", "currentMetrics", "conditions"},
			},
		},
		Dependencies: []string{
			"k8s.io/api/autoscaling/v2beta1.HorizontalPodAutoscalerCondition", "k8s.io/api/autoscaling/v2beta1.MetricStatus", "k8s.io/apimachinery/pkg/apis/meta/v1.Time"},
	}
}
