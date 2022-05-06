// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !e2e
// +build !e2e

package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/DataDog/watermarkpodautoscaler/api/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/api/v1alpha1/test"
)

var _ = Describe("WatermarkPodAutoscaler Controller", func() {
	const (
		timeout  = time.Second * 30
		interval = time.Second * 2
	)
	namespace := testConfig.namespace
	ctx := context.Background()

	Context("Initial deployment", func() {
		var err error
		name := "test-wpa"

		key := types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}
		It("Should handle WPA", func() {
			podList := &v1.PodList{}
			Eventually(func() bool {
				err = k8sClient.List(ctx, podList)
				if err != nil {
					fmt.Fprint(GinkgoWriter, err)
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue())

			wpaOptions := &test.NewWatermarkPodAutoscalerOptions{
				Spec: &v1alpha1.WatermarkPodAutoscalerSpec{},
			}
			wpa := test.NewWatermarkPodAutoscaler(namespace, name, wpaOptions)
			Expect(k8sClient.Create(ctx, wpa)).Should(Succeed())
			Eventually(func() bool {
				err = k8sClient.Get(ctx, key, wpa)
				if err != nil {
					fmt.Fprint(GinkgoWriter, err)
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue())
		})
	})
})
