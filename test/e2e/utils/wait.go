package utils

import (
	"context"
	"testing"
	"time"

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
