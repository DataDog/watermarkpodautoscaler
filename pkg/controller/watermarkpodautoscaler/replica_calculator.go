// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package watermarkpodautoscaler

import (
	"fmt"
	"math"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	logr "github.com/go-logr/logr"

	"github.com/prometheus/client_golang/prometheus"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	v1coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	metricsclient "k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
)

type ReplicaCalculation struct {
	replicaCount int32
	utilization  int64
	timestamp    time.Time
}

// ReplicaCalculatorItf interface for ReplicaCalculator
type ReplicaCalculatorItf interface {
	GetExternalMetricReplicas(logger logr.Logger, currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
	GetResourceReplicas(logger logr.Logger, currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
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
func (c *ReplicaCalculator) GetExternalMetricReplicas(logger logr.Logger, currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (ReplicaCalculation, error) {
	metricName := metric.External.MetricName
	selector := metric.External.MetricSelector
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return ReplicaCalculation{0, 0, time.Time{}}, err
	}

	metrics, timestamp, err := c.metricsClient.GetExternalMetric(metricName, wpa.Namespace, labelSelector)
	if err != nil {
		// When we add official support for several metrics, move this Delete to only occur if no metric is available at all.
		labelsWithReason := prometheus.Labels{
			wpaNamePromLabel:           wpa.Name,
			resourceNamespacePromLabel: wpa.Namespace,
			resourceNamePromLabel:      wpa.Spec.ScaleTargetRef.Name,
			resourceKindPromLabel:      wpa.Spec.ScaleTargetRef.Kind,
			reasonPromLabel:            upscaleCappingPromLabel}
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = downscaleCappingPromLabel
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = "within_bounds"
		restrictedScaling.Delete(labelsWithReason)
		value.Delete(prometheus.Labels{wpaNamePromLabel: wpa.Name, metricNamePromLabel: metricName})
		return ReplicaCalculation{0, 0, time.Time{}}, fmt.Errorf("unable to get external metric %s/%s/%+v: %s", wpa.Namespace, metricName, selector, err)
	}
	logger.Info("Metrics from the External Metrics Provider", "metrics", metrics)

	averaged := 1.0
	if wpa.Spec.Algorithm == "average" {
		averaged = float64(currentReplicas)
	}

	var sum int64
	for _, val := range metrics {
		sum += val
	}

	replicaCount, utilizationQuantity := getReplicaCount(logger, currentReplicas, wpa, metricName, averaged, sum, metric.External.LowWatermark, metric.External.HighWatermark)
	return ReplicaCalculation{replicaCount, utilizationQuantity, timestamp}, nil
}

// GetResourceReplicas calculates the desired replica count based on a target resource utilization percentage
// of the given resource for pods matching the given selector in the given namespace, and the current replica count
func (c *ReplicaCalculator) GetResourceReplicas(logger logr.Logger, currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (ReplicaCalculation, error) {

	resourceName := metric.Resource.Name
	selector := metric.Resource.MetricSelector
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return ReplicaCalculation{0, 0, time.Time{}}, err
	}

	namespace := wpa.Namespace
	metrics, timestamp, err := c.metricsClient.GetResourceMetric(resourceName, namespace, labelSelector)
	if err != nil {
		// When we add official support for several metrics, move this Delete to only occur if no metric is available at all.
		labelsWithReason := prometheus.Labels{
			wpaNamePromLabel:           wpa.Name,
			resourceNamespacePromLabel: wpa.Namespace,
			resourceNamePromLabel:      wpa.Spec.ScaleTargetRef.Name,
			resourceKindPromLabel:      wpa.Spec.ScaleTargetRef.Kind,
			reasonPromLabel:            upscaleCappingPromLabel}
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = downscaleCappingPromLabel
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = "within_bounds"
		restrictedScaling.Delete(labelsWithReason)
		value.Delete(prometheus.Labels{wpaNamePromLabel: wpa.Name, metricNamePromLabel: string(resourceName)})
		return ReplicaCalculation{0, 0, time.Time{}}, fmt.Errorf("unable to get resource metric %s/%s/%+v: %s", wpa.Namespace, resourceName, selector, err)
	}
	logger.Info("Metrics from the Resource Client", "metrics", metrics)

	podList, err := c.podsGetter.Pods(namespace).List(metav1.ListOptions{LabelSelector: labelSelector.String()})
	if err != nil {
		return ReplicaCalculation{0, 0, time.Time{}}, fmt.Errorf("unable to get pods while calculating replica count: %v", err)
	}

	if len(podList.Items) == 0 {
		return ReplicaCalculation{0, 0, time.Time{}}, fmt.Errorf("no pods returned by selector while calculating replica count")
	}

	readyPods, ignoredPods := groupPods(logger, podList, metrics, resourceName, time.Duration(wpa.Spec.ReadinessDelaySeconds)*time.Second)
	readyPodCount := len(readyPods)

	removeMetricsForPods(metrics, ignoredPods)
	if len(metrics) == 0 {
		return ReplicaCalculation{0, 0, time.Time{}}, fmt.Errorf("did not receive metrics for any ready pods")
	}

	averaged := 1.0
	if wpa.Spec.Algorithm == "average" {
		averaged = float64(readyPodCount)
	}

	var sum int64
	for _, podMetric := range metrics {
		sum += podMetric.Value
	}

	replicaCount, utilizationQuantity := getReplicaCount(logger, currentReplicas, wpa, string(resourceName), averaged, sum, metric.Resource.LowWatermark, metric.Resource.HighWatermark)
	return ReplicaCalculation{replicaCount, utilizationQuantity, timestamp}, nil
}

func getReplicaCount(logger logr.Logger, currentReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, name string, averaged float64, sum int64, lowMark, highMark *resource.Quantity) (replicaCount int32, utilization int64) {
	adjustedUsage := float64(sum) / averaged
	utilizationQuantity := resource.NewMilliQuantity(int64(adjustedUsage), resource.DecimalSI)

	adjustedHM := float64(highMark.MilliValue()) + wpa.Spec.Tolerance*float64(highMark.MilliValue())
	adjustedLM := float64(lowMark.MilliValue()) - wpa.Spec.Tolerance*float64(lowMark.MilliValue())

	labelsWithReason := prometheus.Labels{wpaNamePromLabel: wpa.Name, resourceNamespacePromLabel: wpa.Namespace, resourceNamePromLabel: wpa.Spec.ScaleTargetRef.Name, resourceKindPromLabel: wpa.Spec.ScaleTargetRef.Kind, reasonPromLabel: "within_bounds"}
	labelsWithMetricName := prometheus.Labels{wpaNamePromLabel: wpa.Name, resourceNamespacePromLabel: wpa.Namespace, resourceNamePromLabel: wpa.Spec.ScaleTargetRef.Name, resourceKindPromLabel: wpa.Spec.ScaleTargetRef.Kind, metricNamePromLabel: name}

	switch {
	case adjustedUsage > adjustedHM:
		replicaCount = int32(math.Ceil(float64(currentReplicas) * adjustedUsage / (float64(highMark.MilliValue()))))
		logger.Info("Value is above highMark", "usage", utilizationQuantity.String(), "replicaCount", replicaCount)
	case adjustedUsage < adjustedLM:
		replicaCount = int32(math.Floor(float64(currentReplicas) * adjustedUsage / (float64(lowMark.MilliValue()))))
		// Keep a minimum of 1 replica
		replicaCount = int32(math.Max(float64(replicaCount), 1))
		logger.Info("Value is below lowMark", "usage", utilizationQuantity.String(), "replicaCount", replicaCount)
	default:
		restrictedScaling.With(labelsWithReason).Set(1)
		value.With(labelsWithMetricName).Set(adjustedUsage)
		logger.Info("Within bounds of the watermarks", "value", utilizationQuantity.String(), "lwm", lowMark.String(), "hwm", highMark.String(), "tolerance", wpa.Spec.Tolerance)

		return currentReplicas, utilizationQuantity.MilliValue()
	}

	restrictedScaling.With(labelsWithReason).Set(0)
	value.With(labelsWithMetricName).Set(adjustedUsage)

	return replicaCount, utilizationQuantity.MilliValue()
}

func groupPods(logger logr.Logger, podList *v1.PodList, metrics metricsclient.PodMetricsInfo, resource v1.ResourceName, delayOfInitialReadinessStatus time.Duration) (readyPods, ignoredPods sets.String) {
	readyPods = sets.NewString()
	ignoredPods = sets.NewString()
	for _, pod := range podList.Items {
		// Failed pods shouldn't produce metrics, but add to ignoredPods to be safe
		if pod.Status.Phase == v1.PodFailed {
			ignoredPods.Insert(pod.Name)
			continue
		}
		// Pending pods are ignored
		if pod.Status.Phase == v1.PodPending {
			ignoredPods.Insert(pod.Name)
			continue
		}
		// Pods missing metrics
		_, found := metrics[pod.Name]
		if !found {
			continue
		}

		// Unready pods are ignored.
		if resource == v1.ResourceCPU {
			var ignorePod bool
			_, condition := getPodCondition(&pod.Status, v1.PodReady)

			if condition == nil || pod.Status.StartTime == nil {
				ignorePod = true
			} else {
				// Ignore metric if pod is unready and it has never been ready.
				ignorePod = condition.Status == v1.ConditionFalse && pod.Status.StartTime.Add(delayOfInitialReadinessStatus).After(condition.LastTransitionTime.Time)
			}
			if ignorePod {
				ignoredPods.Insert(pod.Name)
				continue
			}
		}
		readyPods.Insert(pod.Name)
	}
	return readyPods, ignoredPods
}

func removeMetricsForPods(metrics metricsclient.PodMetricsInfo, pods sets.String) {
	for _, pod := range pods.UnsortedList() {
		delete(metrics, pod)
	}
}

// getPodCondition extracts the provided condition from the given status and returns that, and the
// index of the located condition. Returns nil and -1 if the condition is not present.
func getPodCondition(status *v1.PodStatus, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if status == nil {
		return -1, nil
	}
	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return i, &status.Conditions[i]
		}
	}
	return -1, nil
}
