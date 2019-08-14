package e2e

import (
	goctx "context"
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2beta1"
	corev1 "k8s.io/api/core/v1"

	apis "github.com/DataDog/watermarkpodautoscaler/pkg/apis"
	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	wpatest "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1/test"

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
	testCtx := framework.NewTestCtx(t)
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
			// TODO: add metrics
		},
	}
	newWPA := wpatest.NewWatermarkPodAutoscaler(namespace, "app", newWPAOptions)
	if err := f.Client.Create(goctx.TODO(), newWPA, cleanupOptions); err != nil {
		t.Fatal(err)
	}
	t.Logf("newWPA create: %s/%s", namespace, "app")
	// TODO create configMap for the fake external metrics

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
	t.Logf("newWPA valide: %s/%s", namespace, "app")
}
