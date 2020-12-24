// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package e2e

import (
	goctx "context"
	"testing"

	v1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apis "github.com/DataDog/watermarkpodautoscaler/pkg/apis"
	"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	wpatest "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1/test"

	"github.com/DataDog/watermarkpodautoscaler/pkg/util"
	"github.com/DataDog/watermarkpodautoscaler/test/e2e/metricsserver"
	"github.com/DataDog/watermarkpodautoscaler/test/e2e/utils"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
)

func TestWPA(t *testing.T) {
	watermarkpodautoscalerList := &datadoghqv1alpha1.WatermarkPodAutoscalerList{}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, watermarkpodautoscalerList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}

	// create the fake custom metrics server
	testCtx := framework.NewContext(t)
	defer testCtx.Cleanup()
	metricsserver.InitMetricsServer(t, testCtx, "./test/e2e/metricsserver/deploy", "custom-metrics")

	// run subtests
	t.Run("wpa-group", func(t *testing.T) {
		t.Run("SimpleCase", SimpleCase)
	})
}

func SimpleCase(t *testing.T) {

	namespace, ctx, f := initTestFwkResources(t, "watermarkpodautoscaler")
	defer ctx.Cleanup()

	t.Logf("namespace: %s", namespace)
	cleanupOptions := &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval}

	// create Fake App Deployment
	fakeAppDep := utils.NewFakeAppDeployment(namespace, "fakeapp", nil)
	if err := f.Client.Create(goctx.TODO(), fakeAppDep, cleanupOptions); err != nil {
		t.Fatal(err)
	}

	// now create the WPA
	newWPAOptions := &wpatest.NewWatermarkPodAutoscalerOptions{
		Spec: &datadoghqv1alpha1.WatermarkPodAutoscalerSpec{
			ScaleTargetRef: datadoghqv1alpha1.CrossVersionObjectReference{
				Name:       fakeAppDep.Name,
				Kind:       "Deployment",
				APIVersion: "apps/v1",
			},
			MaxReplicas: 10,
			Metrics: []v1alpha1.MetricSpec{
				{
					Type: v1alpha1.ExternalMetricSourceType,
					External: &v1alpha1.ExternalMetricSource{
						MetricName:     "metric_name",
						MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
						HighWatermark:  resource.NewQuantity(100, resource.DecimalSI),
						LowWatermark:   resource.NewQuantity(50, resource.DecimalSI),
					},
				},
			},
		},
	}
	newWPA := wpatest.NewWatermarkPodAutoscaler(namespace, "app", newWPAOptions)
	if err := f.Client.Create(goctx.TODO(), newWPA, cleanupOptions); err != nil {
		t.Fatal(err)
	}
	t.Logf("newWPA create: %s/%s", namespace, "app")

	fakeMetrics := []util.FakeMetric{
		{
			Value:      "150",
			MetricName: "metric_name",
			MetricLabels: map[string]string{
				"label": "value",
			},
		},
	}

	fakeMetricsString, err := util.JSONEncode(fakeMetrics)
	if err != nil {
		t.Fatal(err)
	}

	// create configMap for the fake external metrics
	metricConfigMap := &corev1.ConfigMap{
		Data: map[string]string{
			"metric_name": fakeMetricsString,
		},
	}
	metricConfigMap.Name = metricsserver.ConfigMapName
	metricConfigMap.Namespace = namespace
	if err := f.Client.Create(goctx.TODO(), metricConfigMap, cleanupOptions); err != nil {
		t.Fatal(err)
	}
	t.Logf("metricConfigMap created: %s/%s", namespace, metricsserver.ConfigMapName)

	// check if WPA status is ok
	isOK := func(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) (bool, error) {
		for _, condition := range wpa.Status.Conditions {
			if condition.Type == autoscalingv2.ScalingActive && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	}

	if err := utils.WaitForFuncOnWatermarkPodAutoscaler(t, f.Client, namespace, newWPA.Name, isOK, retryInterval, timeout); err != nil {
		t.Fatal(err)
	}
	t.Logf("newWPA valid: %s/%s", namespace, "app")

	// check if WPA autoscaling is OK
	isAutoscalingOK := func(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, fakeapp *v1.Deployment) (bool, error) {
		target := int32(2)
		if wpa.Status.DesiredReplicas == target && fakeapp.Status.Replicas == target {
			return true, nil
		}
		return false, nil
	}

	if err := utils.WaitForFuncOnFakeAppScaling(t, f.Client, namespace, newWPA.Name, fakeAppDep.Name, isAutoscalingOK, retryInterval, timeout); err != nil {
		t.Fatal(err)
	}
	t.Log("Autoscaling valid")
}
