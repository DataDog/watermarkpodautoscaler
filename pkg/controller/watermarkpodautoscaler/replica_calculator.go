package watermarkpodautoscaler

import (
	"fmt"
	"math"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"

	"github.com/prometheus/client_golang/prometheus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	metricsclient "k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
)

// ReplicaCalculatorItf interface for ReplicaCalculator
type ReplicaCalculatorItf interface {
	GetExternalMetricReplicas(currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCount int32, utilization int64, timestamp time.Time, err error)
}

// ReplicaCalculator is responsible for calculation of the number of replicas
// It contains all the needed information
type ReplicaCalculator struct {
	metricsClient metricsclient.MetricsClient
	podsGetter    v1coreclient.PodsGetter
}

// NewReplicaCalculator returns a ReplicaCalculator object reference
func NewReplicaCalculator(metricsClient metricsclient.MetricsClient, podsGetter v1coreclient.PodsGetter) *ReplicaCalculator {
	return &ReplicaCalculator{
		metricsClient: metricsClient,
		podsGetter:    podsGetter,
	}
}

// GetExternalMetricReplicas calculates the desired replica count based on a
// target metric value (as a milli-value) for the external metric in the given
// namespace, and the current replica count.
func (c *ReplicaCalculator) GetExternalMetricReplicas(currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCount int32, utilization int64, timestamp time.Time, err error) {
	metricName := metric.External.MetricName
	selector := metric.External.MetricSelector
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return 0, 0, time.Time{}, err
	}

	metrics, timestamp, err := c.metricsClient.GetExternalMetric(metricName, wpa.Namespace, labelSelector)
	if err != nil {
		// When we add official support for several metrics, move this Delete to only occur if no metric is available at all.
		restrictedScaling.Delete(prometheus.Labels{"wpa_name": wpa.Name, "reason": "upscale_capping"})
		restrictedScaling.Delete(prometheus.Labels{"wpa_name": wpa.Name, "reason": "downscale_capping"})
		restrictedScaling.Delete(prometheus.Labels{"wpa_name": wpa.Name, "reason": "within_bounds"})
		value.Delete(prometheus.Labels{"wpa_name": wpa.Name, "metric_name": metricName})
		return 0, 0, time.Time{}, fmt.Errorf("unable to get external metric %s/%s/%+v: %s", wpa.Namespace, metricName, selector, err)
	}
	log.Info(fmt.Sprintf("Metrics from the External Metrics Provider: %v", metrics))

	averaged := 1.0
	if wpa.Spec.Algorithm == "average" {
		averaged = float64(currentReplicas)
	}

	var sum int64
	for _, val := range metrics {
		sum += val
	}
	adjustedUsage := float64(sum) / averaged
	utilizationQuantity := resource.NewMilliQuantity(int64(adjustedUsage), resource.DecimalSI)

	highMark := metric.External.HighWatermark
	lowMark := metric.External.LowWatermark

	log.Info(fmt.Sprintf("About to compare utilization %s vs LWM %s and HWM %s", utilizationQuantity.String(), lowMark.String(), highMark.String()))

	adjustedHM := float64(highMark.MilliValue()) + wpa.Spec.Tolerance*float64(highMark.MilliValue())
	adjustedLM := float64(lowMark.MilliValue()) - wpa.Spec.Tolerance*float64(lowMark.MilliValue())

	// We do not use the abs as we want to know if we are higher than the high mark or lower than the low mark
	switch {
	case adjustedUsage > adjustedHM:
		replicaCount = int32(math.Ceil(float64(currentReplicas) * adjustedUsage / (float64(highMark.MilliValue()))))
		log.Info(fmt.Sprintf("Value is above highMark. Usage: %s. ReplicaCount %d", utilizationQuantity.String(), replicaCount))
	case adjustedUsage < adjustedLM:
		replicaCount = int32(math.Floor(float64(currentReplicas) * adjustedUsage / (float64(lowMark.MilliValue()))))
		log.Info(fmt.Sprintf("Value is below lowMark. Usage: %s ReplicaCount %d", utilizationQuantity.String(), replicaCount))
	default:
		restrictedScaling.With(prometheus.Labels{"wpa_name": wpa.Name, "reason": "within_bounds"}).Set(1)
		value.With(prometheus.Labels{"wpa_name": wpa.Name, "metric_name": metricName}).Set(adjustedUsage)
		log.Info(fmt.Sprintf("Within bounds of the watermarks. Value: %s is [%s; %s] Tol: +/- %v%%", utilizationQuantity.String(), lowMark.String(), highMark.String(), wpa.Spec.Tolerance))
		return currentReplicas, utilizationQuantity.MilliValue(), timestamp, nil
	}

	restrictedScaling.With(prometheus.Labels{"wpa_name": wpa.Name, "reason": "within_bounds"}).Set(0)
	value.With(prometheus.Labels{"wpa_name": wpa.Name, "metric_name": metricName}).Set(adjustedUsage)

	return replicaCount, utilizationQuantity.MilliValue(), timestamp, nil
}
