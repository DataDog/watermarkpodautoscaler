package datadoghq

import (
	"fmt"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

func metricNameForRecommender(spec *v1alpha1.WatermarkPodAutoscalerSpec) string {
	if spec.Recommender == nil {
		return ""
	}
	args := fmt.Sprintf("targetType:%s", spec.Recommender.TargetType)
	for k, v := range spec.Recommender.Settings {
		args += fmt.Sprintf(",%s:%s", k, v)
	}
	return fmt.Sprintf("recommender{%s}", args)
}
