// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package utils

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
)

// NewFakeHPAOptions options struct for NewFakeHPA function
type NewFakeHPAOptions struct {
	Labels         map[string]string
	Annotations    map[string]string
	DeploymentName string
	MinReplicas    *int32
	MaxReplicas    *int32
}

// NewFakeHPA return new fake application deployment
func NewFakeHPA(ns, name string, options *NewFakeHPAOptions) *autoscalingv1.HorizontalPodAutoscaler {
	dep := &autoscalingv1.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
		Spec: autoscalingv1.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
				Kind:       "Deployment",
				Name:       "fakeapp",
				APIVersion: "apps/v1",
			},
		},
	}
	if options != nil {
		if options.DeploymentName != "" {
			dep.Spec.ScaleTargetRef.Name = options.DeploymentName
		}
		if options.MinReplicas != nil {
			dep.Spec.MinReplicas = options.MinReplicas
		}
		if options.MaxReplicas != nil {
			dep.Spec.MaxReplicas = *options.MaxReplicas
		}
		if options.Labels != nil {
			for k, v := range options.Labels {
				dep.Labels[k] = v
			}
		}
		if options.Annotations != nil {
			for k, v := range options.Annotations {
				dep.Annotations[k] = v
			}
		}
	}
	return dep
}
