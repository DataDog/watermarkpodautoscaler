// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package test contains a set of test helper functions.
package test

import (
	"time"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	apiVersion = v1alpha1.SchemeGroupVersion.String()
)

// NewWatermarkPodAutoscalerOptions used to provide initiation options for NewWatermarkPodAutoscaler method
type NewWatermarkPodAutoscalerOptions struct {
	Status       *v1alpha1.WatermarkPodAutoscalerStatus
	Spec         *v1alpha1.WatermarkPodAutoscalerSpec
	CreationTime *time.Time
	Labels       map[string]string
	Annotations  map[string]string
}

// NewWatermarkPodAutoscaler return new instance of *v1alpha1.WatermarkPodAutoscaler
func NewWatermarkPodAutoscaler(ns, name string, options *NewWatermarkPodAutoscalerOptions) *v1alpha1.WatermarkPodAutoscaler {
	wpa := &v1alpha1.WatermarkPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			Kind:       "WatermarkPodAutoscaler",
			APIVersion: apiVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
	}
	if options != nil {
		if options.CreationTime != nil {
			wpa.CreationTimestamp = metav1.NewTime(*options.CreationTime)
		}
		if options.Status != nil {
			wpa.Status = *options.Status
		}
		if options.Spec != nil {
			wpa.Spec = *options.Spec
		}
		if options.Labels != nil {
			for k, v := range options.Labels {
				wpa.Labels[k] = v
			}
		}
		if options.Annotations != nil {
			for k, v := range options.Annotations {
				wpa.Annotations[k] = v
			}
		}
	}
	return wpa
}
