// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

// A high watermark saturated by the stretch (5000T x 2 > MaxInt64 milli) must not overflow the tolerance math:
// usage 7e18 milli is below the low watermark 8000T (8e18 milli) minus 0.1%, so 10 replicas scale down to 8.
func TestReplicaCalcSaturatedStretchedWatermarks(t *testing.T) {
	spec := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "queue",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": podNamePrefix}},
			LowWatermark:   resource.NewQuantity(4000_000_000_000_000, resource.DecimalSI),
			HighWatermark:  resource.NewQuantity(5000_000_000_000_000, resource.DecimalSI),
		},
	}
	metric := effectiveMetrics([]v1alpha1.MetricSpec{spec}, 2000)[0]
	tc := replicaCalcTestCase{
		expectedReplicas: 8,
		readyReplicas:    10,
		pos:              metricPosition{isBelow: true},
		scale:            makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm:                    "absolute",
				Tolerance:                    *resource.NewMilliQuantity(1, resource.DecimalSI),
				Metrics:                      []v1alpha1.MetricSpec{metric},
				ReplicaScalingAbsoluteModulo: v1alpha1.NewInt32(1),
				MaxReplicas:                  100,
			},
		},
		metric: &metricInfo{
			spec:                metric,
			levels:              []int64{7_000_000_000_000_000_000},
			expectedUtilization: 7_000_000_000_000_000_000,
		},
	}
	tc.runTest(t)
}
