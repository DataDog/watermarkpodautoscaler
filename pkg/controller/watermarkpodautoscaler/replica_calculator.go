package watermarkpodautoscaler

import (
	"fmt"
	"math"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	metricsclient "k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
)



const (
	// defaultTestingTolerance is default value for calculating when to
	// scale up/scale down.
	defaultTestingTolerance = 0.1
)

// ReplicaCalculator is responsible for calculation of the number of replicas
// It contains all the needed information
type ReplicaCalculator struct {
	metricsClient metricsclient.MetricsClient
	podsGetter    v1coreclient.PodsGetter
	tolerance     float64
}

// NewReplicaCalculator returns a ReplicaCalculator object reference
func NewReplicaCalculator(metricsClient metricsclient.MetricsClient, podsGetter v1coreclient.PodsGetter, tolerance float64) *ReplicaCalculator {
	return &ReplicaCalculator{
		metricsClient: metricsClient,
		podsGetter:    podsGetter,
		tolerance:     tolerance,
	}
}
// GetExternalMetricReplicas calculates the desired replica count based on a
// target metric value (as a milli-value) for the external metric in the given
// namespace, and the current replica count.
func (c *ReplicaCalculator) GetExternalMetricReplicas(currentReplicas int32, lowMark int64, highMark int64,  metricName, namespace string, selector *metav1.LabelSelector) (replicaCount int32, utilization int64, timestamp time.Time, err error) {
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	log.Info(fmt.Sprintf("Using %v", labelSelector))
	// should do
	metrics, timestamp, err := c.metricsClient.GetExternalMetric(metricName, namespace, labelSelector)
	if err != nil {
		return 0, 0, time.Time{}, fmt.Errorf("unable to get external metric %s/%s/%+v: %s", namespace, metricName, selector, err)
	}
	utilization = 0
	for _, val := range metrics {
		utilization = utilization + val
	}
	var usageRatio float64

	log.Info(fmt.Sprintf("About to compare utilization %v vs LWM %v and HWM %v", utilization, lowMark, highMark))
	if float64(utilization) < float64(lowMark) {
		log.Info("Value is below lowMark so can consider downscaling.")
		usageRatio = float64(utilization) / float64(lowMark)

		if math.Abs(1.0-usageRatio) <= c.tolerance {
			// return the current replicas if the change would be too small
			return currentReplicas, utilization, timestamp, nil
		}

	} else if float64(utilization) > float64(highMark) {
		log.Info("Value is above highMark so can consider upscaling.")
		usageRatio = float64(utilization) / float64(highMark)

		if math.Abs(1.0-usageRatio) <= c.tolerance {
			// return the current replicas if the change would be too small
			return currentReplicas, utilization, timestamp, nil
		}

	} else {
		return currentReplicas, utilization, timestamp, nil
	}

	return int32(math.Ceil(usageRatio * float64(currentReplicas))), utilization, timestamp, nil
}
