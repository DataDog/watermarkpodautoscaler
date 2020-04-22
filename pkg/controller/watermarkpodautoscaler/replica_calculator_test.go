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
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1fake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
	emapi "k8s.io/metrics/pkg/apis/external_metrics/v1beta1"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
	emfake "k8s.io/metrics/pkg/client/external_metrics/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

type metricInfo struct {
	podPhase     []v1.PodPhase
	podCondition []v1.ConditionStatus

	spec                v1alpha1.MetricSpec
	levels              []int64
	expectedUtilization int64
}

type replicaCalcTestCase struct {
	currentReplicas  int32
	expectedReplicas int32
	expectedError    error
	timestamp        time.Time

	namespace string
	metric    *metricInfo
	selector  labels.Selector
	wpa       *v1alpha1.WatermarkPodAutoscaler
}

const (
	testNamespace       = "test-namespace"
	podNamePrefix       = "test-pod"
	numContainersPerPod = 1
	readinessDelay      = 10
)

// Helper function to assemble a v1.Pod
func buildPod(namespace, podName string, podLabels map[string]string, phase v1.PodPhase, podCondition v1.ConditionStatus, request string, timestamp time.Time) v1.Pod {
	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels:    podLabels,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU: resource.MustParse(request),
						},
					},
				},
			},
		},
		Status: v1.PodStatus{
			Phase: phase,
			Conditions: []v1.PodCondition{
				{
					Type:               v1.PodReady,
					Status:             podCondition,
					LastTransitionTime: metav1.Time{Time: timestamp},
				},
			},
			StartTime: &metav1.Time{Time: timestamp},
		},
	}
}

func (tc *replicaCalcTestCase) getFakeCoreV1Client(t *testing.T) *corev1fake.Clientset {
	podLabels := map[string]string{"name": podNamePrefix}
	fakeClient := &corev1fake.Clientset{}
	fakeClient.AddReactor("list", "pods", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		obj := &v1.PodList{}
		for i := 0; i < int(tc.currentReplicas); i++ {
			podName := fmt.Sprintf("%s-%d", podNamePrefix, i)
			var podPhase v1.PodPhase
			if len(tc.metric.podPhase) > 0 {
				podPhase = tc.metric.podPhase[i]
			} else {
				podPhase = v1.PodRunning
			}
			var podCondition v1.ConditionStatus
			if len(tc.metric.podCondition) > 0 {
				podCondition = tc.metric.podCondition[i]
			} else {
				podCondition = v1.ConditionTrue
			}
			pod := buildPod(tc.namespace, podName, podLabels, podPhase, podCondition, "1024", tc.timestamp)
			obj.Items = append(obj.Items, pod)
		}
		return true, obj, nil
	})
	return fakeClient
}

func (tc *replicaCalcTestCase) getFakeResourceClient(t *testing.T) *metricsfake.Clientset {
	podLabels := map[string]string{"name": podNamePrefix}
	selector := &metav1.LabelSelector{}
	tc.selector, _ = metav1.LabelSelectorAsSelector(selector)

	fakeClient := &metricsfake.Clientset{}

	fakeClient.AddReactor("list", "pods", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		metrics := &metricsapi.PodMetricsList{}
		for i, cpu := range tc.metric.levels {
			metric := metricsapi.PodMetrics{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-%d", podNamePrefix, i),
					Namespace: tc.namespace,
					Labels:    podLabels,
				},
				Timestamp:  metav1.Time{Time: tc.timestamp},
				Window:     metav1.Duration{Duration: 60},
				Containers: []metricsapi.ContainerMetrics{},
			}

			for j := 0; j < numContainersPerPod; j++ {
				cm := metricsapi.ContainerMetrics{
					Name: fmt.Sprintf("%s-%d-container-%d", podNamePrefix, i, j),
					Usage: v1.ResourceList{
						v1.ResourceCPU: *resource.NewMilliQuantity(
							cpu,
							resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(
							int64(1024*1024),
							resource.BinarySI),
					},
				}
				metric.Containers = append(metric.Containers, cm)
			}
			metrics.Items = append(metrics.Items, metric)
		}
		return true, metrics, nil
	})
	return fakeClient
}

func (tc *replicaCalcTestCase) getFakeEMClient(t *testing.T) *emfake.FakeExternalMetricsClient {
	fakeEMClient := &emfake.FakeExternalMetricsClient{}

	fakeEMClient.AddReactor("list", "*", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		t.Logf("fakeEMClient.AddReactor list *: %v\n", action)
		listAction, wasList := action.(core.ListAction)
		if !wasList {
			return true, nil, fmt.Errorf("expected a list-for action, got %v instead", action)
		}

		if tc.metric.spec.External == nil {
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
	tc.namespace = testNamespace
	coreClient := tc.getFakeCoreV1Client(t)
	emClient := tc.getFakeEMClient(t)
	rClient := tc.getFakeResourceClient(t)
	mClient := metrics.NewRESTMetricsClient(rClient.MetricsV1beta1(), nil, emClient)

	replicaCalculator := NewReplicaCalculator(mClient, coreClient.CoreV1())

	var replicaCalculation ReplicaCalculation
	var err error
	if tc.metric.spec.Resource != nil {
		// Resource metric tests
		replicaCalculation, err = replicaCalculator.GetResourceReplicas(logf.Log, tc.currentReplicas, tc.metric.spec, tc.wpa)

		if tc.expectedError != nil {
			require.Error(t, err, "there should be an error calculating the replica count")
			assert.Contains(t, err.Error(), tc.expectedError.Error(), "the error message should have contained the expected error message")
			return
		}
	} else if tc.metric.spec.External != nil {
		// External metric tests
		replicaCalculation, err = replicaCalculator.GetExternalMetricReplicas(logf.Log, tc.currentReplicas, tc.metric.spec, tc.wpa)
		if tc.expectedError != nil {
			require.Error(t, err, "there should be an error calculating the replica count")
			assert.Contains(t, err.Error(), tc.expectedError.Error(), "the error message should have contained the expected error message")
			return
		}
	}

	require.NoError(t, err, "there should not have been an error calculating the replica count")
	assert.Equal(t, tc.expectedReplicas, replicaCalculation.replicaCount, "replicas should be as expected")
	assert.Equal(t, tc.metric.expectedUtilization, replicaCalculation.utilization, "utilization should be as expected")
	assert.True(t, tc.timestamp.Equal(replicaCalculation.timestamp), "timestamp should be as expected")
	return
}

func TestReplicaCalcDisjointResourcesMetrics(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           "deadbeef",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas: 1,
		expectedError:   fmt.Errorf("no metrics returned from resource metrics API"),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{86000}, // We are higher than the HighWatermark
			expectedUtilization: 86000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUp(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 21,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{90000, 90000, 90000}, // We are higher than the HighWatermark
			expectedUtilization: 270000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleDown(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 1,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{4000, 4000, 4000}, // We are below the LowWatermark
			expectedUtilization: 12000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleDownLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
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
			levels:              []int64{5000, 5000, 5000}, // We are below the LowWatermark
			expectedUtilization: 15000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpPendingLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
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
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 110000,
			podPhase:            []v1.PodPhase{v1.PodPending, v1.PodRunning, v1.PodRunning},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpPendingNoScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 3,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 40000}, // We are at the HighWatermark
			expectedUtilization: 40000,
			podPhase:            []v1.PodPhase{v1.PodPending, v1.PodPending, v1.PodRunning},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpPendingNoScaleStretchTolerance(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 3,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 45000}, // We are at the HighWatermark + tolerance
			expectedUtilization: 45000,
			podPhase:            []v1.PodPhase{v1.PodPending, v1.PodPending, v1.PodRunning},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpFailedLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
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
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 110000,
			podPhase:            []v1.PodPhase{v1.PodFailed, v1.PodRunning, v1.PodRunning},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpUnreadyLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 9,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm:             "absolute",
				Tolerance:             0.2,
				Metrics:               []v1alpha1.MetricSpec{metric1},
				ReadinessDelaySeconds: readinessDelay,
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 110000,
			podCondition:        []v1.ConditionStatus{v1.ConditionFalse, v1.ConditionTrue, v1.ConditionTrue},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUp(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 7,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{90000, 90000, 90000}, // We are higher than the HighWatermark
			expectedUtilization: 90000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleDown(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 1,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{5000, 5000, 5000},
			expectedUtilization: 5000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleDownLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 2,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{15000, 15000, 15000}, // We are below low watermark
			expectedUtilization: 15000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUpPendingLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 5,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 55000,
			podPhase:            []v1.PodPhase{v1.PodPending, v1.PodRunning, v1.PodRunning},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUpPendingNoScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 3,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 40000}, // We are at the HighWatermark
			expectedUtilization: 40000,
			podPhase:            []v1.PodPhase{v1.PodPending, v1.PodPending, v1.PodRunning},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUpPendingNoScaleStretchTolerance(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 3,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 45000}, // We are higher than the HighWatermark
			expectedUtilization: 45000,
			podPhase:            []v1.PodPhase{v1.PodPending, v1.PodPending, v1.PodRunning},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUpFailedLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 5,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: 0.2,
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 55000,
			podPhase:            []v1.PodPhase{v1.PodFailed, v1.PodRunning, v1.PodRunning},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUpUnreadyLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           v1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 5,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm:             "average",
				Tolerance:             0.2,
				Metrics:               []v1alpha1.MetricSpec{metric1},
				ReadinessDelaySeconds: readinessDelay,
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 55000,
			podCondition:        []v1.ConditionStatus{v1.ConditionFalse, v1.ConditionTrue, v1.ConditionTrue},
		},
	}
	tc.runTest(t)
}

// Start of External Metric Tests
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

// Similarly if we the average CPU consumption is down to 2% (ten times smaller than the low watermark), we want to divide by 10 the number of replicas.
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

func TestGroupPods(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	tests := []struct {
		name                string
		pods                *v1.PodList
		metrics             metrics.PodMetricsInfo
		resource            v1.ResourceName
		expectReadyPodCount int
		expectIgnoredPods   sets.String
	}{
		{
			"void",
			&v1.PodList{},
			metrics.PodMetricsInfo{},
			v1.ResourceCPU,
			0,
			sets.NewString(),
		},
		{
			"count in a ready pod - memory",
			&v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "bentham",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"bentham": metrics.PodMetric{Value: 1, Timestamp: time.Now(), Window: time.Minute},
			},
			v1.ResourceMemory,
			1,
			sets.NewString(),
		},
		{
			"ignore a pod without ready condition - CPU",
			&v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "lucretius",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now(),
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
			},
			v1.ResourceCPU,
			0,
			sets.NewString("lucretius"),
		},
		{
			"count in a ready pod with fresh metrics during initialization period - CPU",
			&v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "bentham",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now().Add(-1 * time.Minute),
							},
							Conditions: []v1.PodCondition{
								{
									Type:               v1.PodReady,
									LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
									Status:             v1.ConditionTrue,
								},
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"bentham": metrics.PodMetric{Value: 1, Timestamp: time.Now(), Window: 30 * time.Second},
			},
			v1.ResourceCPU,
			1,
			sets.NewString(),
		},
		{
			"ignore an unready pod during initialization period - CPU",
			&v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "lucretius",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now().Add(-10 * time.Minute),
							},
							Conditions: []v1.PodCondition{
								{
									Type:               v1.PodReady,
									LastTransitionTime: metav1.Time{Time: time.Now().Add(-9*time.Minute - 54*time.Second)},
									Status:             v1.ConditionFalse,
								},
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
			},
			v1.ResourceCPU,
			0,
			sets.NewString("lucretius"),
		},
		{
			"count in a ready pod without fresh metrics after initialization period - CPU",
			&v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "bentham",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now().Add(-3 * time.Minute),
							},
							Conditions: []v1.PodCondition{
								{
									Type:               v1.PodReady,
									LastTransitionTime: metav1.Time{Time: time.Now().Add(-3 * time.Minute)},
									Status:             v1.ConditionTrue,
								},
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"bentham": metrics.PodMetric{Value: 1, Timestamp: time.Now().Add(-2 * time.Minute), Window: time.Minute},
			},
			v1.ResourceCPU,
			1,
			sets.NewString(),
		},

		{
			"count in an unready pod that was ready after initialization period - CPU",
			&v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "lucretius",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now().Add(-10 * time.Minute),
							},
							Conditions: []v1.PodCondition{
								{
									Type:               v1.PodReady,
									LastTransitionTime: metav1.Time{Time: time.Now().Add(-9 * time.Minute)},
									Status:             v1.ConditionFalse,
								},
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
			},
			v1.ResourceCPU,
			1,
			sets.NewString(),
		},
		{
			"ignore pod that has never been ready after initialization period - CPU",
			&v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "lucretius",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now().Add(-10 * time.Minute),
							},
							Conditions: []v1.PodCondition{
								{
									Type:               v1.PodReady,
									LastTransitionTime: metav1.Time{Time: time.Now().Add(-9*time.Minute - 55*time.Second)},
									Status:             v1.ConditionFalse,
								},
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
			},
			v1.ResourceCPU,
			0,
			sets.NewString("lucretius"),
		},
		{
			"a missing pod",
			&v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "epicurus",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now().Add(-3 * time.Minute),
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{},
			v1.ResourceCPU,
			0,
			sets.NewString(),
		},
		{
			"several pods",
			&v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "lucretius",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now(),
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "niccolo",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now().Add(-3 * time.Minute),
							},
							Conditions: []v1.PodCondition{
								{
									Type:               v1.PodReady,
									LastTransitionTime: metav1.Time{Time: time.Now().Add(-3 * time.Minute)},
									Status:             v1.ConditionTrue,
								},
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "epicurus",
						},
						Status: v1.PodStatus{
							Phase: v1.PodSucceeded,
							StartTime: &metav1.Time{
								Time: time.Now().Add(-3 * time.Minute),
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
				"niccolo":   metrics.PodMetric{Value: 1},
			},
			v1.ResourceCPU,
			1,
			sets.NewString("lucretius"),
		},
		{
			name: "pending pods are ignored",
			pods: &v1.PodList{
				Items: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "unscheduled",
						},
						Status: v1.PodStatus{
							Phase: v1.PodPending,
						},
					},
				},
			},
			metrics:             metrics.PodMetricsInfo{},
			resource:            v1.ResourceCPU,
			expectReadyPodCount: 0,
			expectIgnoredPods:   sets.NewString("unscheduled"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readyPods, ignoredPods := groupPods(logf.Log, tc.pods, tc.metrics, tc.resource, time.Duration(readinessDelay)*time.Second)
			readyPodCount := len(readyPods)
			assert.Equal(t, readyPodCount, tc.expectReadyPodCount, "%s got readyPodCount %d, expected %d", tc.name, readyPodCount, tc.expectReadyPodCount)
			assert.EqualValues(t, ignoredPods, tc.expectIgnoredPods, "%s got unreadyPods %v, expected %v", tc.name, ignoredPods, tc.expectIgnoredPods)
		})
	}
}

type fakeMetric struct {
	podName string
	ts      time.Time
	window  time.Duration
	val     int64
}

func TestRemoveMetricsForPods(t *testing.T) {
	timeSec := time.Unix(123456789, 0)
	fakeMetrics := []fakeMetric{
		{
			podName: "pod1",
			ts:      time.Time(timeSec),
			window:  time.Duration(300),
			val:     5,
		},
		{
			podName: "pod2",
			ts:      time.Time(timeSec),
			window:  time.Duration(300),
			val:     5,
		},
		{
			podName: "pod3",
			ts:      time.Time(timeSec),
			window:  time.Duration(300),
			val:     5,
		},
		{
			podName: "pod4",
			ts:      time.Time(timeSec),
			window:  time.Duration(300),
			val:     5,
		},
	}

	fakePodMetrics := make(metrics.PodMetricsInfo, len(fakeMetrics))
	for _, m := range fakeMetrics {
		fakePodMetrics[m.podName] = metrics.PodMetric{
			Timestamp: m.ts,
			Window:    m.window,
			Value:     m.val,
		}
	}

	podsToRm := sets.NewString("pod2", "pod3")

	t.Run("test remove metrics for pods", func(t *testing.T) {
		removeMetricsForPods(fakePodMetrics, podsToRm)
		if len(fakePodMetrics) != 2 {
			t.Errorf("Expected PodMetricsInfo to be of length %d but got %d", 2, len(fakePodMetrics))
		}
	})
}

func TestGetPodCondition(t *testing.T) {
	tests := []struct {
		name               string
		status             *v1.PodStatus
		conditionType      v1.PodConditionType
		expectIndex        int
		expectPodCondition *v1.PodCondition
	}{
		{
			"pod is ready",
			&v1.PodStatus{
				Conditions: []v1.PodCondition{
					{Type: v1.PodReady},
				},
			},
			v1.PodReady,
			0,
			&v1.PodCondition{Type: v1.PodReady},
		},
		{
			"pod is scheduled (not ready)",
			&v1.PodStatus{
				Conditions: []v1.PodCondition{
					{Type: v1.PodScheduled},
				},
			},
			v1.PodReady,
			-1,
			nil,
		},
		{
			"pod is initialized (not ready)",
			&v1.PodStatus{
				Conditions: []v1.PodCondition{
					{Type: v1.PodInitialized},
				},
			},
			v1.PodReady,
			-1,
			nil,
		},
		{
			"pod is ready, searching for a different condition type",
			&v1.PodStatus{
				Conditions: []v1.PodCondition{
					{Type: v1.PodReady},
				},
			},
			v1.PodScheduled,
			-1,
			nil,
		},
		{
			"pod was initialized and ready, searching for ready",
			&v1.PodStatus{
				Conditions: []v1.PodCondition{
					{Type: v1.PodInitialized},
					{Type: v1.PodReady},
				},
			},
			v1.PodReady,
			1,
			&v1.PodCondition{Type: v1.PodReady},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			index, podCondition := getPodCondition(tc.status, tc.conditionType)
			assert.Equal(t, index, tc.expectIndex, "%s got index %d, expected %d", tc.name, index, tc.expectIndex)
			assert.EqualValues(t, podCondition, tc.expectPodCondition, "%s got podCondition %v, expected %v", tc.name, podCondition, tc.expectPodCondition)
		})
	}

}
