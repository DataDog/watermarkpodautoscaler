// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package controllers

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/DataDog/watermarkpodautoscaler/api/v1alpha1"

	metricsclient "github.com/DataDog/watermarkpodautoscaler/third_party/kubernetes/pkg/controller/podautoscaler/metrics"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	corelisters "k8s.io/client-go/listers/core/v1"
)

// ReplicaCalculation is used to compute the scaling recommendation.
type ReplicaCalculation struct {
	replicaCount  int32
	utilization   int64
	timestamp     time.Time
	readyReplicas int32
}

// ReplicaCalculatorItf interface for ReplicaCalculator
type ReplicaCalculatorItf interface {
	GetExternalMetricReplicas(logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
	GetResourceReplicas(logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
}

// ReplicaCalculator is responsible for calculation of the number of replicas
// It contains all the needed information
type ReplicaCalculator struct {
	metricsClient metricsclient.MetricsClient
	podLister     corelisters.PodLister
}

// NewReplicaCalculator returns a ReplicaCalculator object reference
func NewReplicaCalculator(metricsClient metricsclient.MetricsClient, podLister corelisters.PodLister) *ReplicaCalculator {
	return &ReplicaCalculator{
		metricsClient: metricsClient,
		podLister:     podLister,
	}
}

// GetExternalMetricReplicas calculates the desired replica count based on a
// target metric value (as a milli-value) for the external metric in the given
// namespace, and the current replica count.
func (c *ReplicaCalculator) GetExternalMetricReplicas(logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (ReplicaCalculation, error) {
	lbl, err := labels.Parse(target.Status.Selector)
	if err != nil {
		logger.Error(err, "Could not parse the labels of the target")
	}
	podList, err := c.podLister.Pods(target.Namespace).List(lbl)
	if err != nil {
		return ReplicaCalculation{}, fmt.Errorf("unable to get pods while calculating replica count: %v", err)
	}
	if len(podList) == 0 {
		return ReplicaCalculation{}, fmt.Errorf("no pods returned by selector while calculating replica count")
	}

	currentReadyReplicas, incorrectTargetPodsCount, err := c.getReadyPodsCount(logger, target.Name, podList, time.Duration(wpa.Spec.ReadinessDelaySeconds)*time.Second)
	if err != nil {
		return ReplicaCalculation{}, fmt.Errorf("unable to get the number of ready pods across all namespaces for %v: %s", lbl, err.Error())
	}

	if currentReadyReplicas > target.Status.Replicas {
		// if we enter in that condition it means that several Deployment use the same label selector in the same namespace.
		logger.Info("To many ready Pods reported. It can be a conflict between Deployment.Spec.Selector", "currentReadyReplicas", currentReadyReplicas, "targetStatusReplicas", target.Status.Replicas, "scale.selector", target.Status.Selector)
		currentReadyReplicas = target.Status.Replicas
	}

	ratioReadyPods := (100 * currentReadyReplicas / (int32(len(podList)) - incorrectTargetPodsCount))
	if ratioReadyPods < wpa.Spec.MinAvailableReplicaPercentage {
		return ReplicaCalculation{}, fmt.Errorf("%d %% of the pods are unready, will not autoscale %s/%s", ratioReadyPods, target.Namespace, target.Name)
	}

	averaged := 1.0
	if wpa.Spec.Algorithm == "average" {
		averaged = float64(currentReadyReplicas)
	}

	metricName := metric.External.MetricName
	selector := metric.External.MetricSelector
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return ReplicaCalculation{}, err
	}

	metrics, timestamp, err := c.metricsClient.GetExternalMetric(metricName, wpa.Namespace, labelSelector)
	if err != nil {
		// When we add official support for several metrics, move this Delete to only occur if no metric is available at all.
		labelsWithReason := prometheus.Labels{
			wpaNamePromLabel:           wpa.Name,
			wpaNamespacePromLabel:      wpa.Namespace,
			resourceNamespacePromLabel: wpa.Namespace,
			resourceNamePromLabel:      wpa.Spec.ScaleTargetRef.Name,
			resourceKindPromLabel:      wpa.Spec.ScaleTargetRef.Kind,
			reasonPromLabel:            upscaleCappingPromLabelVal,
		}
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = downscaleCappingPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = withinBoundsPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		value.Delete(prometheus.Labels{wpaNamePromLabel: wpa.Name, metricNamePromLabel: metricName})
		return ReplicaCalculation{0, 0, time.Time{}, 0}, fmt.Errorf("unable to get external metric %s/%s/%+v: %s", wpa.Namespace, metricName, selector, err) //nolint:errorlint
	}
	logger.Info("Metrics from the External Metrics Provider", "metrics", metrics)

	var sum int64
	for _, val := range metrics {
		sum += val
	}

	// if the average algorithm is used, the metrics retrieved has to be divided by the number of available replicas.
	adjustedUsage := float64(sum) / averaged
	replicaCount, utilizationQuantity := getReplicaCount(logger, target.Status.Replicas, currentReadyReplicas, wpa, metricName, adjustedUsage, metric.External.LowWatermark, metric.External.HighWatermark)
	return ReplicaCalculation{replicaCount, utilizationQuantity, timestamp, currentReadyReplicas}, nil
}

// GetResourceReplicas calculates the desired replica count based on a target resource utilization percentage
// of the given resource for pods matching the given selector in the given namespace, and the current replica count
func (c *ReplicaCalculator) GetResourceReplicas(logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (ReplicaCalculation, error) {
	resourceName := metric.Resource.Name
	selector := metric.Resource.MetricSelector
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return ReplicaCalculation{0, 0, time.Time{}, 0}, err
	}

	namespace := wpa.Namespace
	metrics, timestamp, err := c.metricsClient.GetResourceMetric(resourceName, namespace, labelSelector, "")
	if err != nil {
		// When we add official support for several metrics, move this Delete to only occur if no metric is available at all.
		labelsWithReason := prometheus.Labels{
			wpaNamePromLabel:           wpa.Name,
			wpaNamespacePromLabel:      wpa.Namespace,
			resourceNamespacePromLabel: wpa.Namespace,
			resourceNamePromLabel:      wpa.Spec.ScaleTargetRef.Name,
			resourceKindPromLabel:      wpa.Spec.ScaleTargetRef.Kind,
			reasonPromLabel:            upscaleCappingPromLabelVal,
		}
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = downscaleCappingPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = withinBoundsPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		value.Delete(prometheus.Labels{wpaNamePromLabel: wpa.Name, metricNamePromLabel: string(resourceName)})
		return ReplicaCalculation{0, 0, time.Time{}, 0}, fmt.Errorf("unable to get resource metric %s/%s/%+v: %s", wpa.Namespace, resourceName, selector, err) //nolint:errorlint
	}
	logger.Info("Metrics from the Resource Client", "metrics", metrics)

	lbl, err := labels.Parse(target.Status.Selector)
	if err != nil {
		return ReplicaCalculation{0, 0, time.Time{}, 0}, fmt.Errorf("could not parse the labels of the target: %w", err)
	}

	podList, err := c.podLister.Pods(namespace).List(lbl)
	if err != nil {
		return ReplicaCalculation{0, 0, time.Time{}, 0}, fmt.Errorf("unable to get pods while calculating replica count: %w", err)
	}

	if len(podList) == 0 {
		return ReplicaCalculation{0, 0, time.Time{}, 0}, fmt.Errorf("no pods returned by selector while calculating replica count")
	}
	readiness := time.Duration(wpa.Spec.ReadinessDelaySeconds) * time.Second
	readyPods, ignoredPods := groupPods(logger, podList, target.Name, metrics, resourceName, readiness)
	readyPodCount := len(readyPods)

	ratioReadyPods := int32(100 * readyPodCount / (len(podList) - len(ignoredPods)))

	if ratioReadyPods < wpa.Spec.MinAvailableReplicaPercentage {
		return ReplicaCalculation{}, fmt.Errorf("%d %% of the pods are unready, will not autoscale %s/%s", ratioReadyPods, target.Namespace, target.Name)
	}

	removeMetricsForPods(metrics, ignoredPods)
	if len(metrics) == 0 {
		return ReplicaCalculation{0, 0, time.Time{}, 0}, fmt.Errorf("did not receive metrics for any ready pods")
	}

	averaged := 1.0
	if wpa.Spec.Algorithm == "average" {
		averaged = float64(readyPodCount)
	}

	var sum int64
	for _, podMetric := range metrics {
		sum += podMetric.Value
	}
	adjustedUsage := float64(sum) / averaged

	replicaCount, utilizationQuantity := getReplicaCount(logger, target.Status.Replicas, int32(readyPodCount), wpa, string(resourceName), adjustedUsage, metric.Resource.LowWatermark, metric.Resource.HighWatermark)
	return ReplicaCalculation{replicaCount, utilizationQuantity, timestamp, int32(readyPodCount)}, nil
}

func getReplicaCount(logger logr.Logger, currentReplicas, currentReadyReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, name string, adjustedUsage float64, lowMark, highMark *resource.Quantity) (replicaCount int32, utilization int64) {
	utilizationQuantity := resource.NewMilliQuantity(int64(adjustedUsage), resource.DecimalSI)
	adjustedHM := float64(highMark.MilliValue() + highMark.MilliValue()*wpa.Spec.Tolerance.MilliValue()/1000)
	adjustedLM := float64(lowMark.MilliValue() - lowMark.MilliValue()*wpa.Spec.Tolerance.MilliValue()/1000)

	labelsWithReason := prometheus.Labels{wpaNamePromLabel: wpa.Name, wpaNamespacePromLabel: wpa.Namespace, resourceNamespacePromLabel: wpa.Namespace, resourceNamePromLabel: wpa.Spec.ScaleTargetRef.Name, resourceKindPromLabel: wpa.Spec.ScaleTargetRef.Kind, reasonPromLabel: withinBoundsPromLabelVal}
	labelsWithMetricName := prometheus.Labels{wpaNamePromLabel: wpa.Name, wpaNamespacePromLabel: wpa.Namespace, resourceNamespacePromLabel: wpa.Namespace, resourceNamePromLabel: wpa.Spec.ScaleTargetRef.Name, resourceKindPromLabel: wpa.Spec.ScaleTargetRef.Kind, metricNamePromLabel: name}

	switch {
	case adjustedUsage > adjustedHM:
		replicaCount = int32(math.Ceil(float64(currentReadyReplicas) * adjustedUsage / (float64(highMark.MilliValue()))))
		// Scale up the computed replica count so that it is evenly divisible by the ReplicaScalingAbsoluteModulo.
		if replicaScalingAbsoluteModuloRemainder := int32(math.Mod(float64(replicaCount), float64(*wpa.Spec.ReplicaScalingAbsoluteModulo))); replicaScalingAbsoluteModuloRemainder > 0 {
			replicaCount += *wpa.Spec.ReplicaScalingAbsoluteModulo - replicaScalingAbsoluteModuloRemainder
		}
		// tolerance: milliValue/10 to represent the %.
		logger.Info("Value is above highMark", "usage", utilizationQuantity.String(), "replicaCount", replicaCount, "currentReadyReplicas", currentReadyReplicas, "tolerance (%)", float64(wpa.Spec.Tolerance.MilliValue())/10, "adjustedHM", adjustedHM, "adjustedUsage", adjustedUsage)
		if replicaCount < currentReplicas {
			logger.Info("Recommendation is lower than current number of replicas while attempting to upscale, aborting", "replicaCount", replicaCount, "currentReadyReplicas", currentReadyReplicas)
			replicaCount = currentReplicas
		}
	case adjustedUsage < adjustedLM:
		replicaCount = int32(math.Floor(float64(currentReadyReplicas) * adjustedUsage / (float64(lowMark.MilliValue()))))
		// Keep a minimum of 1 replica
		replicaCount = int32(math.Max(float64(replicaCount), 1))
		if replicaScalingAbsoluteModuloRemainder := int32(math.Mod(float64(replicaCount), float64(*wpa.Spec.ReplicaScalingAbsoluteModulo))); replicaScalingAbsoluteModuloRemainder > 0 {
			// Scale up the computed replica count so that it is evenly divisible by the ReplicaScalingAbsoluteModulo.
			replicaCount += *wpa.Spec.ReplicaScalingAbsoluteModulo - replicaScalingAbsoluteModuloRemainder
		}
		logger.Info("Value is below lowMark", "usage", utilizationQuantity.String(), "replicaCount", replicaCount, "currentReadyReplicas", currentReadyReplicas, "tolerance (%)", float64(wpa.Spec.Tolerance.MilliValue())/10, "adjustedLM", adjustedLM, "adjustedUsage", adjustedUsage)
		if replicaCount > currentReplicas {
			logger.Info("Recommendation is higher than current number of replicas while attempting to downscale, aborting", "replicaCount", replicaCount, "currentReadyReplicas", currentReadyReplicas)
			replicaCount = currentReplicas
		}
	default:
		restrictedScaling.With(labelsWithReason).Set(1)
		value.With(labelsWithMetricName).Set(adjustedUsage)
		logger.Info("Within bounds of the watermarks", "value", utilizationQuantity.String(), "currentReadyReplicas", currentReadyReplicas, "tolerance (%)", float64(wpa.Spec.Tolerance.MilliValue())/10, "adjustedLM", adjustedLM, "adjustedHM", adjustedHM, "adjustedUsage", adjustedUsage)
		// returning the currentReplicas instead of the count of healthy ones to be consistent with the upstream behavior.
		return currentReplicas, utilizationQuantity.MilliValue()
	}

	restrictedScaling.With(labelsWithReason).Set(0)
	value.With(labelsWithMetricName).Set(adjustedUsage)

	return replicaCount, utilizationQuantity.MilliValue()
}

func (c *ReplicaCalculator) getReadyPodsCount(log logr.Logger, targetName string, podList []*corev1.Pod, readinessDelay time.Duration) (int32, int32, error) {
	toleratedAsReadyPodCount := 0
	var incorrectTargetPodsCount int
	for _, pod := range podList {
		// matchLabel might be too broad, use the OwnerRef to scope over the actual target
		if ok := checkOwnerRef(pod.OwnerReferences, targetName); !ok {
			incorrectTargetPodsCount++
			continue
		}
		_, condition := getPodCondition(&pod.Status, corev1.PodReady)
		// We can't distinguish pods that are past the Readiness in the lifecycle but have not reached it
		// and pods that are still Unschedulable but we don't need this level of granularity.
		if condition == nil || pod.Status.StartTime == nil {
			log.Info("Pod unready", "namespace", pod.Namespace, "name", pod.Name)
			continue
		}
		if pod.Status.Phase == corev1.PodRunning && condition.Status == corev1.ConditionTrue ||
			// Pending includes the time spent pulling images onto the host.
			// If the pod is stuck in a ContainerCreating state for more than readinessDelay we want to discard it.
			pod.Status.Phase == corev1.PodPending && metav1.Now().Sub((condition.LastTransitionTime).Time) < readinessDelay {
			toleratedAsReadyPodCount++
		}
	}
	log.Info("getReadyPodsCount", "full podList length", len(podList), "toleratedAsReadyPodCount", toleratedAsReadyPodCount, "incorrectly targeted pods", incorrectTargetPodsCount)
	if toleratedAsReadyPodCount == 0 {
		return 0, int32(incorrectTargetPodsCount), fmt.Errorf("among the %d pods, none is ready. Skipping recommendation", len(podList))
	}

	return int32(toleratedAsReadyPodCount), int32(incorrectTargetPodsCount), nil
}

const (
	replicaSetKind  = "ReplicaSet"
	statefulSetKind = "StatefulSet"
)

func checkOwnerRef(ownerRef []metav1.OwnerReference, targetName string) bool {
	for _, o := range ownerRef {
		if o.Kind != replicaSetKind && o.Kind != statefulSetKind {
			continue
		}

		mainOwnerRef := o.Name
		if o.Kind == replicaSetKind {
			// removes the last section from the replicaSet name to get the deployment name.
			lastIndex := strings.LastIndex(o.Name, "-")
			if lastIndex > -1 {
				mainOwnerRef = o.Name[:lastIndex]
			}
		}
		if mainOwnerRef == targetName {
			return true
		}
	}
	return false
}

func groupPods(logger logr.Logger, podList []*corev1.Pod, targetName string, metrics metricsclient.PodMetricsInfo, resource corev1.ResourceName, delayOfInitialReadinessStatus time.Duration) (readyPods, ignoredPods sets.String) {
	readyPods = sets.NewString()
	ignoredPods = sets.NewString()
	missing := sets.NewString()
	var incorrectTargetPodsCount int
	for _, pod := range podList {
		// matchLabel might be too broad, use the OwnerRef to scope over the actual target
		if ok := checkOwnerRef(pod.OwnerReferences, targetName); !ok {
			incorrectTargetPodsCount++
			continue
		}
		// Failed pods shouldn't produce metrics, but add to ignoredPods to be safe
		if pod.Status.Phase == corev1.PodFailed {
			ignoredPods.Insert(pod.Name)
			continue
		}
		// Pending pods are ignored with Resource metrics.
		if pod.Status.Phase == corev1.PodPending {
			ignoredPods.Insert(pod.Name)
			continue
		}
		// Pods missing metrics
		_, found := metrics[pod.Name]
		if !found {
			missing.Insert(pod.Name)
			continue
		}

		// Unready pods are ignored.
		if resource == corev1.ResourceCPU {
			var ignorePod bool
			_, condition := getPodCondition(&pod.Status, corev1.PodReady)

			if condition == nil || pod.Status.StartTime == nil {
				ignorePod = true
			} else {
				// Ignore metric if pod is unready and it has never been ready.
				ignorePod = condition.Status == corev1.ConditionFalse && pod.Status.StartTime.Add(delayOfInitialReadinessStatus).After(condition.LastTransitionTime.Time)
			}
			if ignorePod {
				ignoredPods.Insert(pod.Name)
				continue
			}
		}
		readyPods.Insert(pod.Name)
	}
	logger.Info("groupPods", "ready", len(readyPods), "missing", len(missing), "ignored", len(ignoredPods), "incorrect target", incorrectTargetPodsCount)
	return readyPods, ignoredPods
}

func removeMetricsForPods(metrics metricsclient.PodMetricsInfo, pods sets.String) {
	for _, pod := range pods.UnsortedList() {
		delete(metrics, pod)
	}
}

// getPodCondition extracts the provided condition from the given status and returns that, and the
// index of the located condition. Returns nil and -1 if the condition is not present.
func getPodCondition(status *corev1.PodStatus, conditionType corev1.PodConditionType) (int, *corev1.PodCondition) {
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
