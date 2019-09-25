// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package utils

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	framework "github.com/operator-framework/operator-sdk/pkg/test"

	dynclient "sigs.k8s.io/controller-runtime/pkg/client"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
)

// WaitForFuncOnWatermarkPodAutoscaler used to wait a valid condition on a WatermarkPodAutoscaler
func WaitForFuncOnWatermarkPodAutoscaler(t *testing.T, client framework.FrameworkClient, namespace, name string, f func(dd *datadoghqv1alpha1.WatermarkPodAutoscaler) (bool, error), retryInterval, timeout time.Duration) error {
	return wait.Poll(retryInterval, timeout, func() (bool, error) {
		objKey := dynclient.ObjectKey{
			Namespace: namespace,
			Name:      name,
		}
		WatermarkPodAutoscaler := &datadoghqv1alpha1.WatermarkPodAutoscaler{}
		err := client.Get(context.TODO(), objKey, WatermarkPodAutoscaler)
		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("Waiting for availability of %s WatermarkPodAutoscaler\n", name)
				return false, nil
			}
			return false, err
		}

		ok, err := f(WatermarkPodAutoscaler)
		t.Logf("Waiting for condition function to be true ok for %s WatermarkPodAutoscaler (%t/%v)\n", name, ok, err)
		return ok, err
	})
}

// WaitForFuncOnFakeAppScaling used to wait for autoscaling the fake up with the desired replicas
func WaitForFuncOnFakeAppScaling(t *testing.T, client framework.FrameworkClient, namespace, name, fakeAppName string, f func(dd *datadoghqv1alpha1.WatermarkPodAutoscaler, fakeApp *v1.Deployment) (bool, error), retryInterval, timeout time.Duration) error {
	return wait.Poll(retryInterval, timeout, func() (bool, error) {
		objKey := dynclient.ObjectKey{
			Namespace: namespace,
			Name:      name,
		}
		watermarkPodAutoscaler := &datadoghqv1alpha1.WatermarkPodAutoscaler{}
		err := client.Get(context.TODO(), objKey, watermarkPodAutoscaler)
		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("Waiting for availability of %s WatermarkPodAutoscaler\n", name)
				return false, nil
			}
			return false, err
		}

		objKey = dynclient.ObjectKey{
			Namespace: namespace,
			Name:      fakeAppName,
		}
		fakeApp := &v1.Deployment{}
		err = client.Get(context.TODO(), objKey, fakeApp)
		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("Waiting for availability of %s FakeAppDeployment\n", name)
				return false, nil
			}
			return false, err
		}

		ok, err := f(watermarkPodAutoscaler, fakeApp)
		t.Logf("Waiting for condition function to be true ok for %s WatermarkPodAutoscaler (%t/%v)\n", name, ok, err)
		t.Logf("Waiting for condition function to be true ok for %s FakeAppDeployment (%t/%v)\n", fakeAppName, ok, err)
		return ok, err
	})
}
