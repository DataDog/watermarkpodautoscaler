package e2e

import (
	"testing"

	apis "github.com/DataDog/watermarkpodautoscaler/pkg/apis"
	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
)

func TestWPA(t *testing.T) {
	watermarkpodautoscalerList := &datadoghqv1alpha1.WatermarkPodAutoscalerList{}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, watermarkpodautoscalerList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}
	// run subtests
	t.Run("wpa-group", func(t *testing.T) {
		t.Run("SimpleCase", SimpleCase)
	})
}

func SimpleCase(t *testing.T) {

}
