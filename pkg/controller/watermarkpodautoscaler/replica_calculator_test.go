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
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
	emapi "k8s.io/metrics/pkg/apis/external_metrics/v1beta1"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
	emfake "k8s.io/metrics/pkg/client/external_metrics/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

type resourceInfo struct {
	name     corev1.ResourceName
	requests []resource.Quantity
}

type metricInfo struct {
	spec                v1alpha1.MetricSpec
	levels              []int64
	expectedUtilization int64
}

type replicaCalcTestCase struct {
	expectedReplicas int32
	expectedError    error
	timestamp        time.Time

	namespace string
	metric    *metricInfo
	resource  *resourceInfo
	scale     *autoscalingv1.Scale
	wpa       *v1alpha1.WatermarkPodAutoscaler

	podCondition         []corev1.PodCondition
	podStartTime         []metav1.Time
	podPhase             []corev1.PodPhase
	podDeletionTimestamp []bool
}

const (
	testReplicaSetName  = "foo-bar-123-345"
	replicaSetKind      = "ReplicaSet"
	testDeploymentName  = "foo-bar-123"
	testNamespace       = "test-namespace"
	podNamePrefix       = "test-pod"
	numContainersPerPod = 1
	readinessDelay      = 10
)

func (tc *replicaCalcTestCase) getFakeResourceClient() *metricsfake.Clientset {
	// TODO add assertion similarly to the getFakeEMClient method.
	podLabels := map[string]string{"name": podNamePrefix}

	fakeClient := &metricsfake.Clientset{}
	fakeClient.AddWatchReactor("pods", func(action core.Action) (handled bool, ret watch.Interface, err error) { return true, nil, nil })
	fakeClient.AddReactor("list", "pods", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		podMetrics := &metricsapi.PodMetricsList{}
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
					Usage: corev1.ResourceList{
						corev1.ResourceCPU: *resource.NewMilliQuantity(
							cpu,
							resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(
							int64(1024*1024),
							resource.BinarySI),
					},
				}
				metric.Containers = append(metric.Containers, cm)
			}
			podMetrics.Items = append(podMetrics.Items, metric)
		}
		return true, podMetrics, nil
	})
	return fakeClient
}

func (tc *replicaCalcTestCase) getFakeEMClient(t *testing.T) *emfake.FakeExternalMetricsClient {
	fakeEMClient := &emfake.FakeExternalMetricsClient{}
	fakeEMClient.AddWatchReactor("pods", func(action core.Action) (handled bool, ret watch.Interface, err error) { return true, nil, nil })
	fakeEMClient.AddReactor("list", "pods", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		return false, nil, nil
	})

	fakeEMClient.AddReactor("list", "*", func(action core.Action) (handled bool, ret runtime.Object, err error) {
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

		extMetrics := emapi.ExternalMetricValueList{}

		for _, level := range tc.metric.levels {
			metric := emapi.ExternalMetricValue{
				Timestamp:  metav1.Time{Time: tc.timestamp},
				MetricName: tc.metric.spec.External.MetricName,
				Value:      *resource.NewMilliQuantity(level, resource.DecimalSI),
			}
			extMetrics.Items = append(extMetrics.Items, metric)
		}
		return true, &extMetrics, nil
	})
	return fakeEMClient
}

func (tc *replicaCalcTestCase) prepareTestClientSet() *fake.Clientset {
	fakeClient := &fake.Clientset{}
	fakeClient.AddWatchReactor("pods", func(action core.Action) (handled bool, ret watch.Interface, err error) { return false, nil, nil })
	fakeClient.AddReactor("list", "pods", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		obj := &corev1.PodList{}
		podsCount := int(tc.scale.Status.Replicas)
		// Failed pods are not included in tc.scale.Status.currentReplicas
		if tc.podPhase != nil && len(tc.podPhase) > podsCount {
			podsCount = len(tc.podPhase)
		}
		for i := 0; i < podsCount; i++ {
			podReadiness := corev1.ConditionTrue
			podTransitionTime := metav1.Now()
			if tc.podCondition != nil && i < len(tc.podCondition) {
				podReadiness = tc.podCondition[i].Status
				podTransitionTime = tc.podCondition[i].LastTransitionTime
			}
			var podStartTime metav1.Time
			if tc.podStartTime != nil {
				podStartTime = tc.podStartTime[i]
			}
			podPhase := corev1.PodRunning
			if tc.podPhase != nil {
				podPhase = tc.podPhase[i]
			}
			podDeletionTimestamp := false
			if tc.podDeletionTimestamp != nil {
				podDeletionTimestamp = tc.podDeletionTimestamp[i]
			}
			podName := fmt.Sprintf("%s-%d", podNamePrefix, i)
			pod := corev1.Pod{
				Status: corev1.PodStatus{
					Phase:     podPhase,
					StartTime: &podStartTime,
					Conditions: []corev1.PodCondition{
						{
							Type:               corev1.PodReady,
							Status:             podReadiness,
							LastTransitionTime: podTransitionTime,
						},
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: testNamespace,
					Labels: map[string]string{
						"name": podNamePrefix,
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind: replicaSetKind,
							Name: testReplicaSetName,
						},
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{}, {}},
				},
			}
			if podDeletionTimestamp {
				pod.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			}

			if tc.resource != nil && i < len(tc.resource.requests) {
				pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						tc.resource.name: tc.resource.requests[i],
					},
				}
				pod.Spec.Containers[1].Resources = corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						tc.resource.name: tc.resource.requests[i],
					},
				}
			}
			obj.Items = append(obj.Items, pod)
		}
		return true, obj, nil
	})
	return fakeClient
}

func (tc *replicaCalcTestCase) runTest(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	tc.namespace = testNamespace
	fakeClient := tc.prepareTestClientSet()

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	informer := informerFactory.Core().V1().Pods()

	emClient := tc.getFakeEMClient(t)

	rClient := tc.getFakeResourceClient()

	mClient := metrics.NewRESTMetricsClient(rClient.MetricsV1beta1(), nil, emClient)

	replicaCalculator := NewReplicaCalculator(mClient, informer.Lister())

	stop := make(chan struct{})
	defer close(stop)
	informerFactory.Start(stop)
	if !cache.WaitForNamedCacheSync("HPA", stop, informer.Informer().HasSynced) {
		return
	}

	var replicaCalculation ReplicaCalculation
	var err error
	if tc.metric.spec.Resource != nil {
		// Resource metric tests
		// Update with the correct labels.
		replicaCalculation, err = replicaCalculator.GetResourceReplicas(logf.Log, tc.scale, tc.metric.spec, tc.wpa)

		if tc.expectedError != nil {
			require.Error(t, err, "there should be an error calculating the replica count")
			assert.Contains(t, err.Error(), tc.expectedError.Error(), "the error message should have contained the expected error message")
			return
		}
	} else if tc.metric.spec.External != nil {
		// External metric tests
		replicaCalculation, err = replicaCalculator.GetExternalMetricReplicas(logf.Log, tc.scale, tc.metric.spec, tc.wpa)
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
		expectedError: fmt.Errorf("no metrics returned from resource metrics API"),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		scale: makeScale(testDeploymentName, 1, map[string]string{"name": "test-pod"}),
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{86000}, // We are higher than the HighWatermark
			expectedUtilization: 86000,
		},
	}
	tc.runTest(t)
}

func makeScale(_ string, currentReplicas int32, labelsMap map[string]string) *autoscalingv1.Scale {
	return &autoscalingv1.Scale{
		ObjectMeta: metav1.ObjectMeta{
			Name: testDeploymentName,
		},
		Status: autoscalingv1.ScaleStatus{
			Selector: labels.FormatLabels(labelsMap),
			Replicas: currentReplicas,
		},
	}
}

func TestReplicaCalcAbsoluteScaleUp(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 21,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(25, resource.DecimalSI), // 25m represents 2.5%
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
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 1,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
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
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 2,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
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
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 6,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				// With the absolute algorithm, we will have a utilization of 120k compared to a HWM of 48k (inc. tolerance)
				// There are 2 Running replicas.
				// The resulting amount of replicas is 2 * 120 / 40 -> 6
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodRunning, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 70000}, // We are higher than the HighWatermark
			expectedUtilization: 120000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpPendingLessScaleExtraReplica(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 7,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodRunning, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50001, 70000}, // We are higher than the HighWatermark
			expectedUtilization: 120001,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpPendingNoScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 3,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 40000}, // We are below the HighWatermark + threshold
			expectedUtilization: 40000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpPendingNoScaleStretchTolerance(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 3,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 45000}, // We are at the HighWatermark + tolerance
			expectedUtilization: 45000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpFailedLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 6,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodFailed, corev1.PodRunning, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 110000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAbsoluteScaleUpUnreadyLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 16,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm:             "absolute",
				Tolerance:             *resource.NewMilliQuantity(20, resource.DecimalSI),
				Metrics:               []v1alpha1.MetricSpec{metric1},
				ReadinessDelaySeconds: readinessDelay,
			},
		},
		podCondition: []corev1.PodCondition{
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: metav1.Now(),
			},
			{
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
			},
			{
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 210000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUp(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 7,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
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
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 1,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
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
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 2,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
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
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		expectedReplicas: 3,
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodRunning, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 55000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUpPendingNoScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 3,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 40000}, // We are at the HighWatermark
			expectedUtilization: 40000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUpPendingNoScaleStretchTolerance(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 2,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 45000}, // We are higher than the HighWatermark
			expectedUtilization: 45000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUpFailedLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 3,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodFailed, corev1.PodRunning, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 55000,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcAverageScaleUpUnreadyLessScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	metric1 := v1alpha1.MetricSpec{
		Type: v1alpha1.ResourceMetricSourceType,
		Resource: &v1alpha1.ResourceMetricSource{
			Name:           corev1.ResourceCPU,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "test-pod"}},
			HighWatermark:  resource.NewMilliQuantity(40000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(20000, resource.DecimalSI),
		},
	}

	tc := replicaCalcTestCase{
		expectedReplicas: 6,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm:             "average",
				Tolerance:             *resource.NewMilliQuantity(20, resource.DecimalSI),
				Metrics:               []v1alpha1.MetricSpec{metric1},
				ReadinessDelaySeconds: readinessDelay,
			},
		},
		podCondition: []corev1.PodCondition{
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: metav1.Now(),
			},
			{
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
			},
			{
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
			},
		},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{100000, 50000, 60000}, // We are higher than the HighWatermark
			expectedUtilization: 70000,
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
		expectedReplicas: 9,
		scale:            makeScale(testDeploymentName, 4, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
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
		expectedReplicas: 20,
		scale:            makeScale(testDeploymentName, 9, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
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
		expectedReplicas: 2,
		scale:            makeScale(testDeploymentName, 20, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(20, resource.DecimalSI),
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
		expectedReplicas: 9,
		scale:            makeScale(testDeploymentName, 9, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(200, resource.DecimalSI),
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
		expectedReplicas: 4,
		scale:            makeScale(testDeploymentName, 5, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(10, resource.DecimalSI),
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
		expectedReplicas: 3,
		scale:            makeScale(testDeploymentName, 4, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(10, resource.DecimalSI),
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
		expectedReplicas: 3,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(10, resource.DecimalSI),
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

// We have pods that are pending and not within an acceptable window.
func TestPendingtExpiredScale(t *testing.T) {
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
		expectedReplicas: 1,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(10, resource.DecimalSI),
				Metrics:   []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodRunning, corev1.PodRunning},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{10000}, // We are well under the low watermarks
			expectedUtilization: 10000,
		},
	}
	tc.runTest(t)
}

// We have pods that are pending and one is within an acceptable window.
func TestPendingNotExpiredScale(t *testing.T) {
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
	now := metav1.Now()
	startTime := metav1.Unix(now.Unix()-120, 0)
	withinDuration := metav1.Unix(now.Unix()-readinessDelay/2, 0)
	expired := metav1.Unix(now.Unix()-2*readinessDelay, 0)
	tc := replicaCalcTestCase{
		expectedReplicas: 1,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm:             "absolute",
				Tolerance:             *resource.NewMilliQuantity(10, resource.DecimalSI),
				ReadinessDelaySeconds: readinessDelay,
				Metrics:               []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodRunning},

		podCondition: []corev1.PodCondition{
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: withinDuration,
			},
			{
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
			},
		},

		// faking the start of the pod so that it appears to have been pending for less than readinessDelay.
		podStartTime: []metav1.Time{startTime, startTime, startTime},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{70000}, // We are under the low watermarks
			expectedUtilization: 70000,
		},
	}
	tc.runTest(t)
}

// We have pods that are expired and only one is above the HWM so we end up downscaling.
func TestPendingExpiredHigherWatermarkDownscale(t *testing.T) {
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
	now := metav1.Now()
	startTime := metav1.Unix(now.Unix()-120, 0)
	expired := metav1.Unix(now.Unix()-2*readinessDelay, 0)
	tc := replicaCalcTestCase{
		expectedReplicas: 2,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm:             "absolute",
				Tolerance:             *resource.NewMilliQuantity(10, resource.DecimalSI),
				ReadinessDelaySeconds: readinessDelay,
				Metrics:               []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodRunning},

		podCondition: []corev1.PodCondition{
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
			},
		},

		// faking the start of the pod so that it appears to have been pending for less than readinessDelay.
		podStartTime: []metav1.Time{startTime, startTime, startTime},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{90000}, // We are within the watermarks
			expectedUtilization: 90000,
		},
	}
	tc.runTest(t)
}

// We have pods that are pending and one is within an acceptable window.
func TestPendingNotExpiredWithinBoundsNoScale(t *testing.T) {
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
	now := metav1.Now()
	startTime := metav1.Unix(now.Unix()-120, 0)
	withinDuration := metav1.Unix(now.Unix()-readinessDelay/2, 0)
	expired := metav1.Unix(now.Unix()-2*readinessDelay, 0)
	tc := replicaCalcTestCase{
		expectedReplicas: 3,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm:             "absolute",
				Tolerance:             *resource.NewMilliQuantity(10, resource.DecimalSI),
				ReadinessDelaySeconds: readinessDelay,
				Metrics:               []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodRunning},

		podCondition: []corev1.PodCondition{
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: withinDuration,
			},
			{
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
			},
		},

		// faking the start of the pod so that it appears to have been pending for less than readinessDelay.
		podStartTime: []metav1.Time{startTime, startTime, startTime},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{80000}, // We are within the watermarks
			expectedUtilization: 80000,
		},
	}
	tc.runTest(t)
}

// We have pods that are pending and one is within an acceptable window.
func TestPendingNotOverlyScaling(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	wpaMetricSpec := v1alpha1.MetricSpec{
		Type: v1alpha1.ExternalMetricSourceType,
		External: &v1alpha1.ExternalMetricSource{
			MetricName:     "loadbalancer.request.per.seconds",
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			HighWatermark:  resource.NewMilliQuantity(85000, resource.DecimalSI),
			LowWatermark:   resource.NewMilliQuantity(75000, resource.DecimalSI),
		},
	}
	now := metav1.Now()
	startTime := metav1.Unix(now.Unix()-120, 0)
	withinDuration := metav1.Unix(now.Unix()-readinessDelay/2, 0)
	expired := metav1.Unix(now.Unix()-2*readinessDelay, 0)
	tc := replicaCalcTestCase{
		expectedReplicas: 19,
		scale:            makeScale(testDeploymentName, 7, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm:             "absolute",
				Tolerance:             *resource.NewMilliQuantity(10, resource.DecimalSI),
				ReadinessDelaySeconds: readinessDelay,
				Metrics:               []v1alpha1.MetricSpec{wpaMetricSpec},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodPending, corev1.PodPending, corev1.PodPending, corev1.PodPending, corev1.PodRunning},

		podCondition: []corev1.PodCondition{
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: withinDuration,
			},
			{
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
			},
		},

		// faking the start of the pod so that it appears to have been pending for less than readinessDelay.
		podStartTime: []metav1.Time{startTime, startTime, startTime, startTime, startTime, startTime, startTime},
		metric: &metricInfo{
			spec:                wpaMetricSpec,
			levels:              []int64{800000},
			expectedUtilization: 800000,
		},
	}
	tc.runTest(t)
}

// We have pods that are pending and one is within an acceptable window.
func TestPendingUnprotectedOverlyScaling(t *testing.T) {
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
	now := metav1.Now()
	startTime := metav1.Unix(now.Unix()-120, 0)
	withinDuration := metav1.Unix(now.Unix()-readinessDelay/2, 0)
	expired := metav1.Unix(now.Unix()-2*readinessDelay, 0)
	tc := replicaCalcTestCase{
		expectedReplicas: 66,
		scale:            makeScale(testDeploymentName, 7, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "absolute",
				Tolerance: *resource.NewMilliQuantity(10, resource.DecimalSI),
				// High to force the consideration of pending pods as running
				ReadinessDelaySeconds: 6000,
				Metrics:               []v1alpha1.MetricSpec{metric1},
			},
		},
		podPhase: []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodPending, corev1.PodPending, corev1.PodPending, corev1.PodPending, corev1.PodRunning},

		podCondition: []corev1.PodCondition{
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: expired,
			},
			{
				Status:             corev1.ConditionFalse,
				LastTransitionTime: withinDuration,
			},
			{
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
			},
		},

		// faking the start of the pod so that it appears to have been pending for less than readinessDelay.
		podStartTime: []metav1.Time{startTime, startTime, startTime, startTime, startTime, startTime, startTime},
		metric: &metricInfo{
			spec:                metric1,
			levels:              []int64{800000},
			expectedUtilization: 800000,
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
		expectedReplicas: 5,
		scale:            makeScale(testDeploymentName, 3, map[string]string{"name": "test-pod"}),
		wpa: &v1alpha1.WatermarkPodAutoscaler{
			Spec: v1alpha1.WatermarkPodAutoscalerSpec{
				Algorithm: "average",
				Tolerance: *resource.NewMilliQuantity(10, resource.DecimalSI),
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
		targetName          string
		pods                []*corev1.Pod
		metrics             metrics.PodMetricsInfo
		resource            corev1.ResourceName
		expectReadyPodCount int
		expectIgnoredPods   sets.String
	}{
		{
			"void",
			"",
			[]*corev1.Pod{},
			metrics.PodMetricsInfo{},
			corev1.ResourceCPU,
			0,
			sets.NewString(),
		},
		{
			"count in a ready pod - memory",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bentham",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
					},
				},
			},
			metrics.PodMetricsInfo{
				"bentham": metrics.PodMetric{Value: 1, Timestamp: time.Now(), Window: time.Minute},
			},
			corev1.ResourceMemory,
			1,
			sets.NewString(),
		},
		{
			"ignore a pod without ready condition - CPU",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "lucretius",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now(),
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
			},
			corev1.ResourceCPU,
			0,
			sets.NewString("lucretius"),
		},
		{
			"count in a ready pod with fresh metrics during initialization period - CPU",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bentham",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now().Add(-1 * time.Minute),
						},
						Conditions: []corev1.PodCondition{
							{
								Type:               corev1.PodReady,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
								Status:             corev1.ConditionTrue,
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"bentham": metrics.PodMetric{Value: 1, Timestamp: time.Now(), Window: 30 * time.Second},
			},
			corev1.ResourceCPU,
			1,
			sets.NewString(),
		},
		{
			"ignore an unready pod during initialization period - CPU",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "lucretius",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now().Add(-10 * time.Minute),
						},
						Conditions: []corev1.PodCondition{
							{
								Type:               corev1.PodReady,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-9*time.Minute - 54*time.Second)},
								Status:             corev1.ConditionFalse,
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
			},
			corev1.ResourceCPU,
			0,
			sets.NewString("lucretius"),
		},
		{
			"count in a ready pod without fresh metrics after initialization period - CPU",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bentham",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now().Add(-3 * time.Minute),
						},
						Conditions: []corev1.PodCondition{
							{
								Type:               corev1.PodReady,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-3 * time.Minute)},
								Status:             corev1.ConditionTrue,
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"bentham": metrics.PodMetric{Value: 1, Timestamp: time.Now().Add(-2 * time.Minute), Window: time.Minute},
			},
			corev1.ResourceCPU,
			1,
			sets.NewString(),
		},

		{
			"count in an unready pod that was ready after initialization period - CPU",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "lucretius",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now().Add(-10 * time.Minute),
						},
						Conditions: []corev1.PodCondition{
							{
								Type:               corev1.PodReady,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-9 * time.Minute)},
								Status:             corev1.ConditionFalse,
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
			},
			corev1.ResourceCPU,
			1,
			sets.NewString(),
		},
		{
			"ignore pod that has never been ready after initialization period - CPU",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "lucretius",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now().Add(-10 * time.Minute),
						},
						Conditions: []corev1.PodCondition{
							{
								Type:               corev1.PodReady,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-9*time.Minute - 55*time.Second)},
								Status:             corev1.ConditionFalse,
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
			},
			corev1.ResourceCPU,
			0,
			sets.NewString("lucretius"),
		},
		{
			"a missing pod",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "epicurus",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now().Add(-3 * time.Minute),
						},
					},
				},
			},
			metrics.PodMetricsInfo{},
			corev1.ResourceCPU,
			0,
			sets.NewString(),
		},
		{
			"several pods",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "lucretius",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now(),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "niccolo",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now().Add(-3 * time.Minute),
						},
						Conditions: []corev1.PodCondition{
							{
								Type:               corev1.PodReady,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-3 * time.Minute)},
								Status:             corev1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "epicurus",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now().Add(-3 * time.Minute),
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"lucretius": metrics.PodMetric{Value: 1},
				"niccolo":   metrics.PodMetric{Value: 1},
			},
			corev1.ResourceCPU,
			1,
			sets.NewString("lucretius"),
		},
		{
			"too many pods in scope with labels",
			testDeploymentName,
			[]*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "lucretius",
						OwnerReferences: []metav1.OwnerReference{{
							Name: "not-the-right-replicaset",
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now(),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "niccolo",
						OwnerReferences: []metav1.OwnerReference{{
							Name: "not-the-right-replicaset",
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now().Add(-3 * time.Minute),
						},
						Conditions: []corev1.PodCondition{
							{
								Type:               corev1.PodReady,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-3 * time.Minute)},
								Status:             corev1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "epicurus",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
						StartTime: &metav1.Time{
							Time: time.Now(),
						},
						Conditions: []corev1.PodCondition{
							{
								Type:               corev1.PodReady,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-3 * time.Minute)},
								Status:             corev1.ConditionTrue,
							},
						},
					},
				},
			},
			metrics.PodMetricsInfo{
				"epicurus":  metrics.PodMetric{Value: 1},
				"lucretius": metrics.PodMetric{Value: 1},
				"niccolo":   metrics.PodMetric{Value: 1},
			},
			corev1.ResourceCPU,
			1,
			sets.NewString(),
		},
		{
			name: "pending pods are ignored",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "unscheduled",
						OwnerReferences: []metav1.OwnerReference{{
							Name: testReplicaSetName,
							Kind: replicaSetKind,
						}},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
					},
				},
			},
			metrics:             metrics.PodMetricsInfo{},
			resource:            corev1.ResourceCPU,
			expectReadyPodCount: 0,
			expectIgnoredPods:   sets.NewString("unscheduled"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readyPods, ignoredPods := groupPods(logf.Log, tc.pods, tc.targetName, tc.metrics, tc.resource, time.Duration(readinessDelay)*time.Second)
			readyPodCount := len(readyPods)
			assert.Equal(t, tc.expectReadyPodCount, readyPodCount, "%s got readyPodCount %d, expected %d", tc.name, readyPodCount, tc.expectReadyPodCount)
			assert.EqualValues(t, tc.expectIgnoredPods, ignoredPods, "%s got unreadyPods %v, expected %v", tc.name, ignoredPods, tc.expectIgnoredPods)
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
			ts:      timeSec,
			window:  time.Duration(300),
			val:     5,
		},
		{
			podName: "pod2",
			ts:      timeSec,
			window:  time.Duration(300),
			val:     5,
		},
		{
			podName: "pod3",
			ts:      timeSec,
			window:  time.Duration(300),
			val:     5,
		},
		{
			podName: "pod4",
			ts:      timeSec,
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

func TestGetReadyPodsCount(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	now := metav1.Now()
	startTime := metav1.Unix(now.Unix()-120, 0)
	readyTolerated := metav1.Unix(now.Unix()-readinessDelay/2, 0)
	expired := metav1.Unix(now.Unix()-2*readinessDelay, 0)

	tests := []struct {
		name          string
		selector      map[string]string
		phases        []corev1.PodPhase
		conditions    []corev1.PodCondition
		startTimes    []metav1.Time
		expected      int32
		errorExpected error
	}{
		{
			name:     "All Pods Running",
			selector: labels.Set{"name": "test-pod"},
			conditions: []corev1.PodCondition{
				{
					Status:             corev1.ConditionTrue,
					LastTransitionTime: readyTolerated, // LastTransitionTime does not matter in this case.
				},
				{
					Status:             corev1.ConditionTrue,
					LastTransitionTime: expired, // Since the pod is already ready we do not look at the LastTransitionTime
				},
				{
					Status:             corev1.ConditionTrue,
					LastTransitionTime: now,
				},
			},
			startTimes:    []metav1.Time{startTime, startTime, startTime},
			phases:        []corev1.PodPhase{corev1.PodRunning, corev1.PodRunning, corev1.PodRunning},
			expected:      3,
			errorExpected: nil,
		},
		{
			name:     "One Pod Pending but recent, one expired",
			selector: labels.Set{"name": "test-pod"},
			conditions: []corev1.PodCondition{
				{
					Status:             corev1.ConditionFalse,
					LastTransitionTime: readyTolerated,
				},
				{
					Status:             corev1.ConditionFalse,
					LastTransitionTime: expired,
				},
				{
					Status:             corev1.ConditionTrue,
					LastTransitionTime: now,
				},
			},
			startTimes:    []metav1.Time{startTime, startTime, startTime},
			phases:        []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodRunning},
			expected:      2,
			errorExpected: nil,
		},
		{
			name:     "All Pods Failed",
			selector: labels.Set{"name": "test-pod"},
			conditions: []corev1.PodCondition{
				{
					Status:             corev1.ConditionFalse,
					LastTransitionTime: readyTolerated,
				},
				{
					Status:             corev1.ConditionFalse,
					LastTransitionTime: expired,
				},
				{
					Status:             corev1.ConditionTrue,
					LastTransitionTime: now,
				},
			},
			startTimes:    []metav1.Time{startTime, startTime, startTime},
			phases:        []corev1.PodPhase{corev1.PodFailed, corev1.PodFailed, corev1.PodFailed},
			expected:      0,
			errorExpected: fmt.Errorf("among the %d pods, none is ready. Skipping recommendation", 3),
		},
		{
			name:     "No matching pods",
			selector: labels.Set{"name": "wrong"},
			conditions: []corev1.PodCondition{
				{
					Status:             corev1.ConditionTrue,
					LastTransitionTime: now, // LastTransitionTime does not matter.
				},
				{
					Status:             corev1.ConditionTrue,
					LastTransitionTime: now,
				},
				{
					Status:             corev1.ConditionTrue,
					LastTransitionTime: now,
				},
			},
			startTimes:    []metav1.Time{startTime, startTime, startTime},
			phases:        []corev1.PodPhase{corev1.PodRunning, corev1.PodRunning, corev1.PodRunning},
			expected:      0,
			errorExpected: fmt.Errorf("no pods returned by selector while calculating replica count"),
		},
		{
			name:     "No ready pods",
			selector: labels.Set{"name": "test-pod"},
			conditions: []corev1.PodCondition{
				{
					Status:             corev1.ConditionFalse,
					LastTransitionTime: startTime,
				},
				{
					Status:             corev1.ConditionFalse,
					LastTransitionTime: startTime,
				},
				{
					Status:             corev1.ConditionFalse,
					LastTransitionTime: startTime,
				},
			},
			startTimes:    []metav1.Time{startTime, startTime, startTime},
			phases:        []corev1.PodPhase{corev1.PodPending, corev1.PodPending, corev1.PodPending},
			expected:      0,
			errorExpected: fmt.Errorf("among the 3 pods, none is ready. Skipping recommendation"),
		},
		{
			name:     "pod stuck in pending and containerCreating",
			selector: labels.Set{"name": "test-pod"},
			conditions: []corev1.PodCondition{
				{
					Status:             corev1.ConditionTrue,
					LastTransitionTime: startTime,
				},
				{
					Status:             corev1.ConditionFalse,
					LastTransitionTime: readyTolerated, // Pending but tolerated
				},
				{
					Status:             corev1.ConditionFalse, // This would be stuck in containerCreating
					LastTransitionTime: startTime,
				},
			},
			startTimes:    []metav1.Time{startTime, startTime, startTime},
			phases:        []corev1.PodPhase{corev1.PodRunning, corev1.PodPending, corev1.PodPending},
			expected:      2,
			errorExpected: nil,
		},
	}

	for _, f := range tests {
		t.Run(f.name, func(t *testing.T) {
			tc := replicaCalcTestCase{
				podCondition: f.conditions,
				podPhase:     f.phases,
				podStartTime: f.startTimes,
				scale:        makeScale(testDeploymentName, 3, f.selector),
				namespace:    testNamespace,
			}
			fakeClient := tc.prepareTestClientSet()

			informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			informer := informerFactory.Core().V1().Pods()

			replicaCalculator := NewReplicaCalculator(nil, informer.Lister())

			stop := make(chan struct{})
			defer close(stop)
			informerFactory.Start(stop)
			if !cache.WaitForNamedCacheSync("HPA", stop, informer.Informer().HasSynced) {
				return
			}
			val, err := replicaCalculator.getReadyPodsCount(tc.scale, labels.SelectorFromSet(f.selector), readinessDelay*time.Second)
			assert.Equal(t, f.expected, val)
			if f.errorExpected != nil {
				assert.EqualError(t, f.errorExpected, err.Error())
			}
		})
	}
}

func TestGetPodCondition(t *testing.T) {
	tests := []struct {
		name               string
		status             *corev1.PodStatus
		conditionType      corev1.PodConditionType
		expectIndex        int
		expectPodCondition *corev1.PodCondition
	}{
		{
			"pod is ready",
			&corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady},
				},
			},
			corev1.PodReady,
			0,
			&corev1.PodCondition{Type: corev1.PodReady},
		},
		{
			"pod is scheduled (not ready)",
			&corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodScheduled},
				},
			},
			corev1.PodReady,
			-1,
			nil,
		},
		{
			"pod is initialized (not ready)",
			&corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodInitialized},
				},
			},
			corev1.PodReady,
			-1,
			nil,
		},
		{
			"pod is ready, searching for a different condition type",
			&corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady},
				},
			},
			corev1.PodScheduled,
			-1,
			nil,
		},
		{
			"pod was initialized and ready, searching for ready",
			&corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodInitialized},
					{Type: corev1.PodReady},
				},
			},
			corev1.PodReady,
			1,
			&corev1.PodCondition{Type: corev1.PodReady},
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
