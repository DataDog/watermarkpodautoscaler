// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package watermarkpodautoscaler

import (
	"fmt"
	"testing"
	"time"

	"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	core "k8s.io/client-go/testing"
	"k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
	emapi "k8s.io/metrics/pkg/apis/external_metrics/v1beta1"
	emfake "k8s.io/metrics/pkg/client/external_metrics/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

type metricInfo struct {
	spec                v1alpha1.MetricSpec
	levels              []int64
	expectedUtilization int64
}

type replicaCalcTestCase struct {
	currentReplicas  int32
	expectedReplicas int32
	expectedError    error
	timestamp        time.Time

	metric *metricInfo
	wpa    *v1alpha1.WatermarkPodAutoscaler
}

func (tc *replicaCalcTestCase) prep(t *testing.T) *emfake.FakeExternalMetricsClient {
	fakeEMClient := &emfake.FakeExternalMetricsClient{}

	fakeEMClient.AddReactor("list", "*", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		t.Logf("fakeEMClient.AddReactor list *: %v\n", action)
		listAction, wasList := action.(core.ListAction)
		if !wasList {
			return true, nil, fmt.Errorf("expected a list-for action, got %v instead", action)
		}

		if tc.metric == nil {
			return true, nil, fmt.Errorf("no external metrics specified in test client")
		}

		assert.Equal(t, tc.metric.spec.External.MetricName, listAction.GetResource().Resource, "the metric requested should have matched the one specified")

		selector, err := metav1.LabelSelectorAsSelector(tc.metric.spec.External.MetricSelector)
		if err != nil {
			return true, nil, fmt.Errorf("failed to convert label selector specified in test client")
		}
		assert.Equal(t, selector, listAction.GetListRestrictions().Labels, "the metric selector should have matched the one specified")

		metrics := emapi.ExternalMetricValueList{}

		for _, level := range tc.metric.levels {

			metric := emapi.ExternalMetricValue{
				Timestamp:  metav1.Time{Time: tc.timestamp},
				MetricName: tc.metric.spec.External.MetricName,
				Value:      *resource.NewMilliQuantity(level, resource.DecimalSI),
			}
			metrics.Items = append(metrics.Items, metric)
		}

		return true, &metrics, nil
	})

	return fakeEMClient
}

func (tc *replicaCalcTestCase) runTest(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	emClient := tc.prep(t)
	mClient := metrics.NewRESTMetricsClient(nil, nil, emClient)
	replicaCalculator := &ReplicaCalculator{
		metricsClient: mClient,
	}
	var outReplicas int32
	var outUtilization int64
	var outTimestamp time.Time
	var err error
	if tc.metric.spec.External.MetricSelector != nil {
		outReplicas, outUtilization, outTimestamp, err = replicaCalculator.GetExternalMetricReplicas(logf.Log, tc.currentReplicas, tc.metric.spec, tc.wpa)
	}
	if tc.expectedError != nil {
		require.Error(t, err, "there should be an error calculating the replica count")
		assert.Contains(t, err.Error(), tc.expectedError.Error(), "the error message should have contained the expected error message")
		return
	}
	require.NoError(t, err, "there should not have been an error calculating the replica count")
	assert.Equal(t, tc.expectedReplicas, outReplicas, "replicas should be as expected")
	assert.Equal(t, tc.metric.expectedUtilization, outUtilization, "utilization should be as expected")
	assert.True(t, tc.timestamp.Equal(outTimestamp), "timestamp should be as expected")
}

// Test Upscale1, Upscale2 and Upscale3 showcase the absolute algorithm.
// Use case is: "My application should run between LM to HM on average"
// We show that we scale proportionally to the usage ratio and the current number of replicas.
// This is a good use case for the average CPU usage of an application for instance.
// Here one replicas can handle between 20% and 40 % of CPU usage and we currently have 4.
// If we see that the application is running at 86% of CPU we need to at least double the number of replicas. (Upscale1 and Upscale2)
func TestReplicaCalcAboveAbsoluteExternal_Upscale1(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "deadbeef",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(4000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(2000, resource.DecimalSI),
		},
	}
	tc := replicaCalcTestCase{
		currentReplicas:  4,
		expectedReplicas: 9,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{8600}, // We are higher than the HighWatermark
			expectedUtilization: 8600,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAboveAbsoluteExternal_Upscale2(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "deadbeef",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(4000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(2000, resource.DecimalSI),
		},
	}
	tc := replicaCalcTestCase{
		currentReplicas:  9,
		expectedReplicas: 20,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{8600}, // We are still higher than the HighWatermark
			expectedUtilization: 8600,
		},
	}
	tc.runTest(t)
}

// Similarly if we the average CPU consuuption is down to 2% (ten times smaller than the low watermark), we want to divide by 10 the number of replicas.
func TestReplicaCalcAboveAbsoluteExternal_Upscale3(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "deadbeef",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(4000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(2000, resource.DecimalSI),
		},
	}
	tc := replicaCalcTestCase{
		currentReplicas:  20,
		expectedReplicas: 2,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{200}, // We are 10 times lower than the LowWatermark, we divide by 10 the # of replicas.
			expectedUtilization: 200,
		},
	}
	tc.runTest(t)
}

// TestReplicaCalcWithinAbsoluteExternal shows the impact of the tolerance. Here we are just below the Adjusted High Watermark
func TestReplicaCalcWithinAbsoluteExternal(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "deadbeef",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(4000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(2000, resource.DecimalSI),
		},
	}
	tc := replicaCalcTestCase{
		currentReplicas:  9,
		expectedReplicas: 9,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{4799}, // We are between the Watermarks
			expectedUtilization: 4799,
		},
	}
	tc.runTest(t)
}

// Test Downscale1, Downscale2, Downscale3 and Downscale4 showcase the average algorithm.
// Use case is: "1 replica of my application can handle LM to HM"
// We show that going from X to Y to X again, we end up with the same number of replicas.
// Here one replicas can handle between 75 and 85 qps and we currently have 5 (which means we should serve between 375-425 qps at the LB level)
// Going to 370 we only need 4 replicas and we can handle between 300-340 qps.
func TestReplicaCalcBelowAverageExternal_Downscale1(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "loadbalancer.request.per.seconds",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(85000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(75000, resource.DecimalSI),
		},
	}
	tc := replicaCalcTestCase{
		currentReplicas:  5,
		expectedReplicas: 4,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.01,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{370000}, // We are below the LowWatermark we downscale to 4.
			expectedUtilization: 74000,           // utilization was 370/5 = 74
		},
	}
	tc.runTest(t)
}

// We see fewer qps, down to 240, which would yield 3 replicas.
func TestReplicaCalcBelowAverageExternal_Downscale2(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "loadbalancer.request.per.seconds",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(85000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(75000, resource.DecimalSI),
		},
	}
	tc := replicaCalcTestCase{
		currentReplicas:  4,
		expectedReplicas: 3,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.01,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{240000}, // We are below the LowWatermark we downscale
			expectedUtilization: 60000,
		},
	}
	tc.runTest(t)
}

// We keep seeing the same number of qps to the load balancer, we stay at 3 replicas
func TestReplicaCalcBelowAverageExternal_Downscale3(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "loadbalancer.request.per.seconds",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(85000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(75000, resource.DecimalSI),
		},
	}
	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 3,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.01,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{240000}, // We are within the watermarks
			expectedUtilization: 80000,
		},
	}
	tc.runTest(t)
}

// We now see a surge back to the initial value of 375, which means 5 replicas
func TestReplicaCalcBelowAverageExternal_Downscale4(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "loadbalancer.request.per.seconds",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(85000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(75000, resource.DecimalSI),
		},
	}
	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 5,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.01,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{375000}, // We are above the highWatermark we upscale back to 5 (case Downscale1)
			expectedUtilization: 125000,
		},
	}
	tc.runTest(t)
}
