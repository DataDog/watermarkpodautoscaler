// +build !ignore_autogenerated

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Code generated by openapi-gen. DO NOT EDIT.

// This file was autogenerated by openapi-gen. Do not edit it manually!

package v1alpha1

import (
	spec "github.com/go-openapi/spec"
	common "k8s.io/kube-openapi/pkg/common"
)

func GetOpenAPIDefinitions(ref common.ReferenceCallback) map[string]common.OpenAPIDefinition {
	return map[string]common.OpenAPIDefinition{
		"./api/v1alpha1.CrossVersionObjectReference":  schema__api_v1alpha1_CrossVersionObjectReference(ref),
		"./api/v1alpha1.ExternalMetricSource":         schema__api_v1alpha1_ExternalMetricSource(ref),
		"./api/v1alpha1.MetricSpec":                   schema__api_v1alpha1_MetricSpec(ref),
		"./api/v1alpha1.ResourceMetricSource":         schema__api_v1alpha1_ResourceMetricSource(ref),
		"./api/v1alpha1.WatermarkPodAutoscaler":       schema__api_v1alpha1_WatermarkPodAutoscaler(ref),
		"./api/v1alpha1.WatermarkPodAutoscalerSpec":   schema__api_v1alpha1_WatermarkPodAutoscalerSpec(ref),
		"./api/v1alpha1.WatermarkPodAutoscalerStatus": schema__api_v1alpha1_WatermarkPodAutoscalerStatus(ref),
	}
}

func schema__api_v1alpha1_CrossVersionObjectReference(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "CrossVersionObjectReference contains enough information to let you identify the referred resource.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "Kind of the referent; More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds\"",
							Default:     "",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"name": {
						SchemaProps: spec.SchemaProps{
							Description: "Name of the referent; More info: http://kubernetes.io/docs/user-guide/identifiers#names",
							Default:     "",
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

func schema__api_v1alpha1_ExternalMetricSource(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "ExternalMetricSource indicates how to scale on a metric not associated with any Kubernetes object (for example length of queue in cloud messaging service, or QPS from loadbalancer running outside of cluster). Exactly one \"target\" type should be set.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"metricName": {
						SchemaProps: spec.SchemaProps{
							Description: "metricName is the name of the metric in question.",
							Default:     "",
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

func schema__api_v1alpha1_MetricSpec(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "MetricSpec specifies how to scale based on a single metric (only `type` and one other matching field should be set at once).",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"type": {
						SchemaProps: spec.SchemaProps{
							Description: "type is the type of metric source.  It should be one of \"Object\", \"Pods\" or \"Resource\", each mapping to a matching field in the object.",
							Default:     "",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"external": {
						SchemaProps: spec.SchemaProps{
							Description: "external refers to a global metric that is not associated with any Kubernetes object. It allows autoscaling based on information coming from components running outside of cluster (for example length of queue in cloud messaging service, or QPS from loadbalancer running outside of cluster).",
							Ref:         ref("./api/v1alpha1.ExternalMetricSource"),
						},
					},
					"resource": {
						SchemaProps: spec.SchemaProps{
							Description: "resource refers to a resource metric (such as those specified in requests and limits) known to Kubernetes describing each pod in the current scale target (e.g. CPU or memory). Such metrics are built in to Kubernetes, and have special scaling options on top of those available to normal per-pod metrics using the \"pods\" source.",
							Ref:         ref("./api/v1alpha1.ResourceMetricSource"),
						},
					},
				},
				Required: []string{"type"},
			},
		},
		Dependencies: []string{
			"./api/v1alpha1.ExternalMetricSource", "./api/v1alpha1.ResourceMetricSource"},
	}
}

func schema__api_v1alpha1_ResourceMetricSource(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "ResourceMetricSource indicates how to scale on a resource metric known to Kubernetes, as specified in requests and limits, describing each pod in the current scale target (e.g. CPU or memory).  The values will be averaged together before being compared to the target.  Such metrics are built in to Kubernetes, and have special scaling options on top of those available to normal per-pod metrics using the \"pods\" source.  Only one \"target\" type should be set.",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"name": {
						SchemaProps: spec.SchemaProps{
							Description: "name is the name of the resource in question.",
							Default:     "",
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
				Required: []string{"name"},
			},
		},
		Dependencies: []string{
			"k8s.io/apimachinery/pkg/api/resource.Quantity", "k8s.io/apimachinery/pkg/apis/meta/v1.LabelSelector"},
	}
}

func schema__api_v1alpha1_WatermarkPodAutoscaler(ref common.ReferenceCallback) common.OpenAPIDefinition {
	return common.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Description: "WatermarkPodAutoscaler is the Schema for the watermarkpodautoscalers API",
				Type:        []string{"object"},
				Properties: map[string]spec.Schema{
					"kind": {
						SchemaProps: spec.SchemaProps{
							Description: "Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"apiVersion": {
						SchemaProps: spec.SchemaProps{
							Description: "APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources",
							Type:        []string{"string"},
							Format:      "",
						},
					},
					"metadata": {
						SchemaProps: spec.SchemaProps{
							Default: map[string]interface{}{},
							Ref:     ref("k8s.io/apimachinery/pkg/apis/meta/v1.ObjectMeta"),
						},
					},
					"spec": {
						SchemaProps: spec.SchemaProps{
							Default: map[string]interface{}{},
							Ref:     ref("./api/v1alpha1.WatermarkPodAutoscalerSpec"),
						},
					},
					"status": {
						SchemaProps: spec.SchemaProps{
							Default: map[string]interface{}{},
							Ref:     ref("./api/v1alpha1.WatermarkPodAutoscalerStatus"),
						},
					},
				},
			},
		},
		Dependencies: []string{
			"./api/v1alpha1.WatermarkPodAutoscalerSpec", "./api/v1alpha1.WatermarkPodAutoscalerStatus", "k8s.io/apimachinery/pkg/apis/meta/v1.ObjectMeta"},
	}
}

func schema__api_v1alpha1_WatermarkPodAutoscalerSpec(ref common.ReferenceCallback) common.OpenAPIDefinition {
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
							Description: "Percentage of replicas that can be added in an upscale event. Parameter used to be a float, in order to support the transition seamlessly, we validate that it is [0;100] in the code. ScaleUpLimitFactor == 0 means that upscaling will not be allowed for the target.",
							Ref:         ref("k8s.io/apimachinery/pkg/api/resource.Quantity"),
						},
					},
					"scaleDownLimitFactor": {
						SchemaProps: spec.SchemaProps{
							Description: "Percentage of replicas that can be removed in an downscale event. Parameter used to be a float, in order to support the transition seamlessly, we validate that it is [0;100[ in the code. ScaleDownLimitFactor == 0 means that downscaling will not be allowed for the target.",
							Ref:         ref("k8s.io/apimachinery/pkg/api/resource.Quantity"),
						},
					},
					"tolerance": {
						SchemaProps: spec.SchemaProps{
							Description: "Parameter used to be a float, in order to support the transition seamlessly, we validate that it is ]0;1[ in the code.",
							Default:     map[string]interface{}{},
							Ref:         ref("k8s.io/apimachinery/pkg/api/resource.Quantity"),
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
							Description: "Whether planned scale changes are actually applied",
							Type:        []string{"boolean"},
							Format:      "",
						},
					},
					"scaleTargetRef": {
						SchemaProps: spec.SchemaProps{
							Description: "part of HorizontalPodAutoscalerSpec, see comments in the k8s-1.10.8 repo: staging/src/k8s.io/api/autoscaling/v1/types.go reference to scaled resource; horizontal pod autoscaler will learn the current resource consumption and will set the desired number of pods by using its Scale subresource.",
							Default:     map[string]interface{}{},
							Ref:         ref("./api/v1alpha1.CrossVersionObjectReference"),
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
										Default: map[string]interface{}{},
										Ref:     ref("./api/v1alpha1.MetricSpec"),
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
					"readinessDelaySeconds": {
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
			"./api/v1alpha1.CrossVersionObjectReference", "./api/v1alpha1.MetricSpec", "k8s.io/apimachinery/pkg/api/resource.Quantity"},
	}
}

func schema__api_v1alpha1_WatermarkPodAutoscalerStatus(ref common.ReferenceCallback) common.OpenAPIDefinition {
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
							Default: 0,
							Type:    []string{"integer"},
							Format:  "int32",
						},
					},
					"desiredReplicas": {
						SchemaProps: spec.SchemaProps{
							Default: 0,
							Type:    []string{"integer"},
							Format:  "int32",
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
										Default: map[string]interface{}{},
										Ref:     ref("k8s.io/api/autoscaling/v2beta1.MetricStatus"),
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
										Default: map[string]interface{}{},
										Ref:     ref("k8s.io/api/autoscaling/v2beta1.HorizontalPodAutoscalerCondition"),
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
