package watermarkpodautoscaler

import (
	"fmt"
	"k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
	emapi "k8s.io/metrics/pkg/apis/external_metrics/v1beta1"
	"testing"
	emfake "k8s.io/metrics/pkg/client/external_metrics/fake"
	core "k8s.io/client-go/testing"
	autoscalingv2 "k8s.io/api/autoscaling/v2beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"time"
	"github.com/stretchr/testify/assert"
)

type metricInfo struct {
	name         string
	levels       []int64
	singleObject *autoscalingv2.CrossVersionObjectReference
	selector     *metav1.LabelSelector

	targetUtilization       int64
	perPodTargetUtilization int64
	expectedUtilization     int64
}

type replicaCalcTestCase struct {
	currentReplicas  int32
	expectedReplicas int32
	expectedError    error

	timestamp time.Time

	metric   *metricInfo

}

func (tc *replicaCalcTestCase) prep(t *testing.T) *emfake.FakeExternalMetricsClient {
	fakeEMClient := &emfake.FakeExternalMetricsClient{}

	fakeEMClient.AddReactor("list", "*", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		fmt.Printf("fakeEMClient.AddReactor list *: %v\n", action)
		listAction, wasList := action.(core.ListAction)
		if !wasList {
			return true, nil, fmt.Errorf("expected a list-for action, got %v instead", action)
		}

		if tc.metric == nil {
			return true, nil, fmt.Errorf("no external metrics specified in test client")
		}

		assert.Equal(t, tc.metric.name, listAction.GetResource().Resource, "the metric requested should have matched the one specified")

		selector, err := metav1.LabelSelectorAsSelector(tc.metric.selector)
		if err != nil {
			return true, nil, fmt.Errorf("failed to convert label selector specified in test client")
		}
		assert.Equal(t, selector, listAction.GetListRestrictions().Labels, "the metric selector should have matched the one specified")

		metrics := emapi.ExternalMetricValueList{}

		for _, level := range tc.metric.levels {

			metric := emapi.ExternalMetricValue{
				Timestamp:  metav1.Time{Time: tc.timestamp},
				MetricName: tc.metric.name,
				Value:      *resource.NewMilliQuantity(level, resource.DecimalSI),
			}
			metrics.Items = append(metrics.Items, metric)
		}

		return true, &metrics, nil
	})

	return fakeEMClient
}

func (tc *replicaCalcTestCase) runTest(t *testing.T) {
	emClient := tc.prep(t)
	mClient := metrics.NewRESTMetricsClient(nil, nil, emClient)
	replicaCalculator := &ReplicaCalculator{
		metricsClient: mClient,
	}
	/*
	var outReplicas int32
		var outUtilization int64
		var outTimestamp time.Time
		var err error
		if tc.metric.selector != nil {
				outReplicas, outUtilization, outTimestamp, err = replicaCalculator.GetExternalMetricReplicas(tc.currentReplicas, tc.metric.targetUtilization, tc.metric.name, testNamespace, tc.metric.selector)
		}
	*/
}

func TestReplicaCalcScaleUpCMExternal(t *testing.T) {
	tc := replicaCalcTestCase{
		currentReplicas:  1,
		expectedReplicas: 2,
		metric: &metricInfo{
			name:                "qps",
			levels:              []int64{8600},
			targetUtilization:   4400,
			expectedUtilization: 8600,
			selector:            &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcScaleUpCMExternalNoLabels(t *testing.T) {
	tc := replicaCalcTestCase{
		currentReplicas:  1,
		expectedReplicas: 2,
		metric: &metricInfo{
			name:                "qps",
			levels:              []int64{8600},
			targetUtilization:   4400,
			expectedUtilization: 8600,
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcScaleDownCMExternal(t *testing.T) {
	tc := replicaCalcTestCase{
		currentReplicas:  5,
		expectedReplicas: 3,
		metric: &metricInfo{
			name:                "qps",
			levels:              []int64{8600},
			targetUtilization:   14334,
			expectedUtilization: 8600,
			selector:            &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
		},
	}
	tc.runTest(t)
}

func TestReplicaCalcToleranceCMExternal(t *testing.T) {
	tc := replicaCalcTestCase{
		currentReplicas:  3,
		expectedReplicas: 3,
		metric: &metricInfo{
			name:                "qps",
			levels:              []int64{8600},
			targetUtilization:   8888,
			expectedUtilization: 8600,
			selector:            &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
		},
	}
	tc.runTest(t)
}
