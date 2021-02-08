// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package utils

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewFakeAppDeploymentOptions options struct for NewFakeAppDeployment function
type NewFakeAppDeploymentOptions struct {
	Image       string
	Replicas    int32
	Labels      map[string]string
	Annotations map[string]string
}

// NewFakeAppDeployment return new fake application deployment
func NewFakeAppDeployment(ns, name string, options *NewFakeAppDeploymentOptions) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name, "fakeapp": "true"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name, "fakeapp": "true"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image: "k8s.gcr.io/pause:latest",
						Name:  "main",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 80,
							Name:          "http",
						}},
					}},
				},
			},
		},
	}
	if options != nil {
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
		if options.Replicas > 0 {
			dep.Spec.Replicas = &options.Replicas
		}
		if options.Image != "" {
			dep.Spec.Template.Spec.Containers[0].Image = options.Image
		}
	}
	return dep
}
