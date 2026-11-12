// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/pkg/math32"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	corelisters "k8s.io/client-go/listers/core/v1"

	metricsclient "github.com/DataDog/watermarkpodautoscaler/third_party/kubernetes/pkg/controller/podautoscaler/metrics"
)

const (
	aboveHighWatermarkAllowedMessage = "Allow upscaling if the value stays over the Watermark"
	belowLowWatermarkAllowedMessage  = "Allow downscaling if the value stays under the Watermark"
	aboveHighWatermarkReason         = "Value above High Watermark"
	belowLowWatermarkReason          = "Value above Low Watermark"
)

// ReplicaCalculation is used to compute the scaling recommendation.
type ReplicaCalculation struct {
	replicaCount  int32
	utilization   int64
	timestamp     time.Time
	readyReplicas int32
	pos           metricPosition
	details       string
}

func EmptyReplicaCalculation() ReplicaCalculation {
	return ReplicaCalculation{0, 0, time.Time{}, 0, metricPosition{}, ""}
}

// metricPosition is used to store whether the end value associated with a metric is above/below the Watermarks
type metricPosition struct {
	isAbove bool
	isBelow bool
}

// ReplicaCalculatorItf interface for ReplicaCalculator
type ReplicaCalculatorItf interface {
	GetExternalMetricReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
	GetResourceReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
	GetRecommenderReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
}

// ReplicaCalculator is responsible for calculation of the number of replicas
// It contains all the needed information
type ReplicaCalculator struct {
	metricsClient     metricsclient.MetricsClient
	recommenderClient RecommenderClient
	podLister         corelisters.PodLister
	k8sClusterName    string
}

// NewReplicaCalculator returns a ReplicaCalculator object reference
func NewReplicaCalculator(metricsClient metricsclient.MetricsClient, recommenderClient RecommenderClient, podLister corelisters.PodLister, k8sClusterName string) *ReplicaCalculator {
	return &ReplicaCalculator{
		metricsClient:     metricsClient,
		recommenderClient: recommenderClient,
		podLister:         podLister,
		k8sClusterName:    k8sClusterName,
	}
}

// GetExternalMetricReplicas calculates the desired replica count based on a
// target metric value (as a milli-value) for the external metric in the given
// namespace, and the current replica count.
func (c *ReplicaCalculator) GetExternalMetricReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (ReplicaCalculation, error) {
	lbl, err := labels.Parse(target.Status.Selector)
	if err != nil {
		logger.Error(err, "Could not parse the labels of the target")
	}
	podList, err := c.podLister.Pods(target.Namespace).List(lbl)
	if err != nil {
		return ReplicaCalculation{}, fmt.Errorf("unable to get pods while calculating replica count: %w", err)
	}
	if len(podList) == 0 {
		return ReplicaCalculation{}, fmt.Errorf("no pods returned by selector while calculating replica count")
	}

	skipOwnerCheck := shouldSkipOwnerCheck(wpa)
	currentReadyReplicas, incorrectTargetPodsCount, err := c.getReadyPodsCount(logger, target.Name, podList, time.Duration(wpa.Spec.ReadinessDelaySeconds)*time.Second, skipOwnerCheck)
	if err != nil {
		return ReplicaCalculation{}, fmt.Errorf("unable to get the number of ready pods across all namespaces for %v: %w", lbl, err)
	}

	if currentReadyReplicas > target.Status.Replicas {
		// if we enter in that condition it means that several Deployment use the same label selector in the same namespace.
		logger.Info("Too many ready Pods reported. It can be a conflict between Deployment.Spec.Selector", "currentReadyReplicas", currentReadyReplicas, "targetStatusReplicas", target.Status.Replicas, "scale.selector", target.Status.Selector)
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
		labelsWithReason := getPrometheusLabels(wpa)
		labelsWithReason[reasonPromLabel] = upscaleCappingPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = downscaleCappingPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = withinBoundsPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		value.Delete(prometheus.Labels{wpaNamePromLabel: wpa.Name, metricNamePromLabel: metricName})
		return EmptyReplicaCalculation(), fmt.Errorf("unable to get external metric %s/%s/%+v: %s", wpa.Namespace, metricName, selector, err) //nolint:errorlint
	}
	logger.V(2).Info("Metrics from the External Metrics Provider", "metrics", metrics)

	var sum int64
	for _, val := range metrics {
		sum += val
	}
	if sum == 0 && !wpa.Spec.TolerateZero {
		logger.Info("Warning, retrieved 0 from the External Metrics Provider which is not tolerated by default, use Spec.TolerateZero to enable",
			"wpa_name", wpa.Name,
			"wpa_namespace", wpa.Namespace,
			"metricName", metricName,
		)
		// We artificially set the metricPos between the watermarks to force the controller not to scale the target.
		setConvergeNotEvaluated(wpa, "the metric value is 0 (see spec.tolerateZero)")
		return ReplicaCalculation{currentReadyReplicas, 0, timestamp, currentReadyReplicas, metricPosition{false, false}, ""}, nil
	}

	// if the average algorithm is used, the metrics retrieved has to be divided by the number of available replicas.
	adjustedUsage := float64(sum) / averaged
	replicaCount, utilizationQuantity, metricPos := getReplicaCount(logger, target.Spec.Replicas, currentReadyReplicas, wpa, metricName, adjustedUsage, metric.External.LowWatermark, metric.External.HighWatermark)
	logger.Info("External Metric replica calculation", "metricName", metricName, "replicaCount", replicaCount, "utilizationQuantity", utilizationQuantity, "timestamp", timestamp, "currentReadyReplicas", currentReadyReplicas)
	return ReplicaCalculation{replicaCount, utilizationQuantity, timestamp, currentReadyReplicas, metricPos, ""}, nil
}

// GetResourceReplicas calculates the desired replica count based on a target resource utilization percentage
// of the given resource for pods matching the given selector in the given namespace, and the current replica count
func (c *ReplicaCalculator) GetResourceReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (ReplicaCalculation, error) {
	resourceName := metric.Resource.Name
	selector := metric.Resource.MetricSelector
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return EmptyReplicaCalculation(), err
	}

	namespace := wpa.Namespace
	metrics, timestamp, err := c.metricsClient.GetResourceMetric(resourceName, namespace, labelSelector, "")
	if err != nil {
		// When we add official support for several metrics, move this Delete to only occur if no metric is available at all.
		labelsWithReason := getPrometheusLabels(wpa)
		labelsWithReason[reasonPromLabel] = upscaleCappingPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = downscaleCappingPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = withinBoundsPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		value.Delete(prometheus.Labels{wpaNamePromLabel: wpa.Name, metricNamePromLabel: string(resourceName)})
		return EmptyReplicaCalculation(), fmt.Errorf("unable to get resource metric %s/%s/%+v: %s", wpa.Namespace, resourceName, selector, err) //nolint:errorlint
	}
	logger.Info("Metrics from the Resource Client", "metrics", metrics)

	lbl, err := labels.Parse(target.Status.Selector)
	if err != nil {
		return EmptyReplicaCalculation(), fmt.Errorf("could not parse the labels of the target: %w", err)
	}

	podList, err := c.podLister.Pods(namespace).List(lbl)
	if err != nil {
		return EmptyReplicaCalculation(), fmt.Errorf("unable to get pods while calculating replica count: %w", err)
	}

	if len(podList) == 0 {
		return EmptyReplicaCalculation(), fmt.Errorf("no pods returned by selector while calculating replica count")
	}
	readiness := time.Duration(wpa.Spec.ReadinessDelaySeconds) * time.Second
	readyPods, ignoredPods := groupPods(logger, podList, target.Name, metrics, resourceName, readiness, shouldSkipOwnerCheck(wpa))
	readyPodCount := len(readyPods)

	ratioReadyPods := int32(100 * readyPodCount / (len(podList) - len(ignoredPods)))

	if ratioReadyPods < wpa.Spec.MinAvailableReplicaPercentage {
		return ReplicaCalculation{}, fmt.Errorf("%d %% of the pods are unready, will not autoscale %s/%s", ratioReadyPods, target.Namespace, target.Name)
	}

	removeMetricsForPods(metrics, ignoredPods)
	if len(metrics) == 0 {
		return EmptyReplicaCalculation(), fmt.Errorf("did not receive metrics for any ready pods")
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

	replicaCount, utilizationQuantity, metricPos := getReplicaCount(logger, target.Spec.Replicas, int32(readyPodCount), wpa, string(resourceName), adjustedUsage, metric.Resource.LowWatermark, metric.Resource.HighWatermark)
	logger.Info("Resource Metric replica calculation", "metricName", metric.Resource.Name, "replicaCount", replicaCount, "utilizationQuantity", utilizationQuantity, "timestamp", timestamp, "currentReadyReplicas", int32(readyPodCount))
	return ReplicaCalculation{replicaCount, utilizationQuantity, timestamp, int32(readyPodCount), metricPos, ""}, nil
}

// GetRecommenderReplicas contacts an external replica recommender to get the desired replica count
func (c *ReplicaCalculator) GetRecommenderReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, wpa *v1alpha1.WatermarkPodAutoscaler) (ReplicaCalculation, error) {
	lbl, err := labels.Parse(target.Status.Selector)
	if err != nil {
		logger.Error(err, "Could not parse the labels of the target")
	}
	podList, err := c.podLister.Pods(target.Namespace).List(lbl)
	if err != nil {
		return ReplicaCalculation{}, fmt.Errorf("unable to get pods while calculating replica count: %w", err)
	}
	if len(podList) == 0 {
		return ReplicaCalculation{}, fmt.Errorf("no pods returned by selector while calculating replica count")
	}

	currentReadyReplicas, incorrectTargetPodsCount, err := c.getReadyPodsCount(logger, target.Name, podList, time.Duration(wpa.Spec.ReadinessDelaySeconds)*time.Second, shouldSkipOwnerCheck(wpa))
	if err != nil {
		return ReplicaCalculation{}, fmt.Errorf("unable to get the number of ready pods across all namespaces for %v: %w", lbl, err)
	}

	if currentReadyReplicas > target.Status.Replicas {
		// if we enter in that condition it means that several Deployment use the same label selector in the same namespace.
		logger.Info("Too many ready Pods reported. It can be a conflict between Deployment.Spec.Selector", "currentReadyReplicas", currentReadyReplicas, "targetStatusReplicas", target.Status.Replicas, "scale.selector", target.Status.Selector)
		currentReadyReplicas = target.Status.Replicas
	}

	ratioReadyPods := (100 * currentReadyReplicas / (int32(len(podList)) - incorrectTargetPodsCount))
	if ratioReadyPods < wpa.Spec.MinAvailableReplicaPercentage {
		return ReplicaCalculation{}, fmt.Errorf("%d %% of the pods are unready, will not autoscale %s/%s", ratioReadyPods, target.Namespace, target.Name)
	}

	// This is because most of the code expects a metric name, so we assume that the recommender is the metric name.
	var metricName = metricNameForRecommender(&wpa.Spec)

	minReplicas := int32(0)
	if wpa.Spec.MinReplicas != nil {
		minReplicas = *wpa.Spec.MinReplicas
	}
	var request = ReplicaRecommendationRequest{
		Namespace:            wpa.Namespace,
		TargetRef:            &wpa.Spec.ScaleTargetRef,
		TargetCluster:        c.k8sClusterName,
		Recommender:          wpa.Spec.Recommender,
		CurrentReadyReplicas: currentReadyReplicas,
		CurrentReplicas:      target.Spec.Replicas,
		DesiredReplicas:      target.Spec.Replicas,
		MinReplicas:          minReplicas,
		MaxReplicas:          wpa.Spec.MaxReplicas,
	}
	reco, err := c.recommenderClient.GetReplicaRecommendation(ctx, &request)
	if err != nil {
		// When we add official support for several metrics, move this Delete to only occur if no metric is available at all.
		labelsWithReason := getPrometheusLabels(wpa)
		labelsWithReason[reasonPromLabel] = upscaleCappingPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = downscaleCappingPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		labelsWithReason[reasonPromLabel] = withinBoundsPromLabelVal
		restrictedScaling.Delete(labelsWithReason)
		value.Delete(prometheus.Labels{wpaNamePromLabel: wpa.Name, metricNamePromLabel: metricName})
		return EmptyReplicaCalculation(), fmt.Errorf("unable to get external recommendation %+v: %s", metricName, err) //nolint:errorlint
	}
	logger.V(2).Info("Replica recommendation from the external replicas recommender", "replicas", reco.Replicas, "timestamp", reco.Timestamp, "details", reco.Details, "replicasLowerBound", reco.ReplicasLowerBound, "replicasUpperBound", reco.ReplicasUpperBound)

	if reco.Replicas == 0 && !wpa.Spec.TolerateZero {
		logger.Info("Warning, retrieved 0 from the Replicas Recommender which is not tolerated by default, use Spec.TolerateZero to enable",
			"wpa_name", wpa.Name,
			"wpa_namespace", wpa.Namespace,
			"metricName", metricName,
		)
		// We artificially set the metricPos between the watermarks to force the controller not to scale the target.
		setConvergeNotEvaluated(wpa, "the recommender returned 0 replicas (see spec.tolerateZero)")
		return ReplicaCalculation{currentReadyReplicas, 0, reco.Timestamp, currentReadyReplicas, metricPosition{false, false}, ""}, nil
	}

	utilizationQuantity := int64(reco.ObservedTargetValue * 1000) // We need to multiply by 1000 to convert it to milliValue

	labelsWithMetricName := getPrometheusLabels(wpa)
	labelsWithMetricName[metricNamePromLabel] = metricName
	value.With(labelsWithMetricName).Set(float64(utilizationQuantity))

	replicaCount, metricPos := adjustReplicaCount(logger, target.Spec.Replicas, currentReadyReplicas, wpa, int32(reco.Replicas), int32(reco.ReplicasLowerBound), int32(reco.ReplicasUpperBound), reco.HasBounds)
	logger.Info("Replicas Recommender replica calculation", "metricName", metricName, "replicaCount", replicaCount, "utilizationQuantity", utilizationQuantity, "timestamp", reco.Timestamp, "currentReadyReplicas", currentReadyReplicas)
	return ReplicaCalculation{replicaCount, utilizationQuantity, reco.Timestamp, currentReadyReplicas, metricPos, reco.Details}, nil
}

func getReplicaCountUpscale(logger logr.Logger, currentReplicas, currentReadyReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, adjustedUsage float64, highMark *resource.Quantity) (replicaCount int32) {
	replicaCount = math32.Ceil(float64(currentReadyReplicas) * adjustedUsage / (float64(highMark.MilliValue())))
	return adjustReplicaCountForUpscale(logger, currentReplicas, currentReadyReplicas, wpa, replicaCount)
}

func roundReplicaCountToAbsoluteModulo(replicaCount int32, wpa *v1alpha1.WatermarkPodAutoscaler) int32 {
	// Scale up the computed replica count so that it is evenly divisible by the ReplicaScalingAbsoluteModulo.
	if replicaScalingAbsoluteModuloRemainder := math32.Mod(replicaCount, *wpa.Spec.ReplicaScalingAbsoluteModulo); replicaScalingAbsoluteModuloRemainder > 0 {
		replicaCount += *wpa.Spec.ReplicaScalingAbsoluteModulo - replicaScalingAbsoluteModuloRemainder
	}
	return replicaCount
}

func adjustReplicaCountForUpscale(logger logr.Logger, currentReplicas, currentReadyReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, replicaCount int32) (replicas int32) {
	replicaCount = roundReplicaCountToAbsoluteModulo(replicaCount, wpa)

	if replicaCount < currentReplicas {
		logger.Info("Recommendation is lower than current number of replicas while attempting to upscale, aborting", "replicaCount", replicaCount, "currentReadyReplicas", currentReadyReplicas)
		replicaCount = currentReplicas
	}

	return replicaCount
}

func getReplicaCountDownscale(logger logr.Logger, currentReplicas, currentReadyReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, adjustedUsage float64, lowMark *resource.Quantity) (replicaCount int32) {
	replicaCount = math32.Floor(float64(currentReadyReplicas) * adjustedUsage / (float64(lowMark.MilliValue())))
	// Keep a minimum of 1 replica
	replicaCount = math32.Max(replicaCount, 1)

	return adjustReplicaCountForDownscale(logger, currentReplicas, currentReadyReplicas, wpa, replicaCount)
}

func adjustReplicaCountForDownscale(logger logr.Logger, currentReplicas, currentReadyReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, replicaCount int32) (replicas int32) {
	replicaCount = roundReplicaCountToAbsoluteModulo(replicaCount, wpa)

	if replicaCount > currentReplicas {
		logger.Info("Recommendation is higher than current number of replicas while attempting to downscale, aborting", "replicaCount", replicaCount, "currentReadyReplicas", currentReadyReplicas)
		replicaCount = currentReplicas
	}

	return replicaCount
}

// ConvergeToWatermark condition reasons.
const (
	convergingToHighWatermarkReason        = "ConvergingToHighWatermark"
	convergingToLowWatermarkReason         = "ConvergingToLowWatermark"
	convergedToHighWatermarkReason         = "ConvergedToHighWatermark"
	convergedToLowWatermarkReason          = "ConvergedToLowWatermark"
	convergeOutsideWatermarksReason        = "OutsideWatermarks"
	convergeOutsideRecommenderBoundsReason = "OutsideRecommenderBounds"
	convergeFollowingRecommendationReason  = "FollowingRecommendation"
	convergenceDisabledReason              = "ConvergenceDisabled"
	convergeNotEvaluatedReason             = "NotEvaluated"
)

// Sources of the convergence mode, reported in the ConvergeToWatermark condition message.
const (
	convergeSourceSpec        = "spec"
	convergeSourceDefault     = "controller default"
	convergeSourceNotSet      = "not set"
	convergeSourceRecommender = "controller default not applied: the WPA uses a recommender"
)

// effectiveConvergeTowardsWatermark returns the convergence mode and where it comes from: the spec, else the
// controller default (status.defaults) unless the WPA uses a recommender.
func effectiveConvergeTowardsWatermark(wpa *v1alpha1.WatermarkPodAutoscaler) (v1alpha1.ConvergeTowardsWatermarkType, string) {
	switch {
	case wpa.Spec.ConvergeTowardsWatermark != "":
		return wpa.Spec.ConvergeTowardsWatermark, convergeSourceSpec
	case wpa.Status.Defaults == nil || wpa.Status.Defaults.ConvergeTowardsWatermark == "":
		return "", convergeSourceNotSet
	case wpa.Spec.Recommender != nil:
		return "", convergeSourceRecommender
	default:
		return wpa.Status.Defaults.ConvergeTowardsWatermark, convergeSourceDefault
	}
}

// convergeState is the state of the convergence reported by the ConvergeToWatermark condition.
type convergeState int

const (
	convergeStepping                 convergeState = iota // the next step keeps the usage within the watermark
	convergeConverged                                     // the next step would cross the watermark
	convergeOutsideWatermarks                             // the usage is not between the watermarks
	convergeOutsideRecommenderBounds                      // outside the recommender bounds, or no bounds
	convergeFollowingRecommendation                       // within the bounds, the recommendation wins
)

// convergeWatermarks describes, for each convergence mode, the watermark it converges towards.
var convergeWatermarks = map[v1alpha1.ConvergeTowardsWatermarkType]struct {
	name, side, convergingReason, convergedReason string
}{
	v1alpha1.ConvergeUpwards:   {"high watermark", "under", convergingToHighWatermarkReason, convergedToHighWatermarkReason},
	v1alpha1.ConvergeDownwards: {"low watermark", "above", convergingToLowWatermarkReason, convergedToLowWatermarkReason},
}

// setConvergeCondition sets the ConvergeToWatermark condition and its metric for mode, the effective mode of the
// WPA. The message names the direction and the source of the mode. Without a mode, it reports ConvergenceDisabled.
func setConvergeCondition(wpa *v1alpha1.WatermarkPodAutoscaler, mode v1alpha1.ConvergeTowardsWatermarkType, state convergeState) {
	_, source := effectiveConvergeTowardsWatermark(wpa)
	status, reason, message := corev1.ConditionFalse, convergenceDisabledReason, ""
	watermark, enabled := convergeWatermarks[mode]
	switch {
	case mode == v1alpha1.ConvergeDisabled:
		message = fmt.Sprintf("Convergence disabled (%s)", source)
	case mode == "":
		message = fmt.Sprintf("Convergence not enabled (%s)", source)
	case !enabled:
		message = fmt.Sprintf("Convergence not enabled: unknown mode %q (%s)", mode, source)
	case state == convergeStepping:
		status, reason = corev1.ConditionTrue, watermark.convergingReason
		message = fmt.Sprintf("Converging towards the %s (%s): the next step keeps the usage %s it", watermark.name, source, watermark.side)
	case state == convergeConverged:
		reason = watermark.convergedReason
		message = fmt.Sprintf("Converged towards the %s (%s): the next step would cross it", watermark.name, source)
	case state == convergeOutsideWatermarks:
		reason = convergeOutsideWatermarksReason
		message = fmt.Sprintf("Converging towards the %s (%s) only between the watermarks", watermark.name, source)
	case state == convergeOutsideRecommenderBounds:
		reason = convergeOutsideRecommenderBoundsReason
		message = fmt.Sprintf("Converging towards the %s (%s) only within the recommender bounds", watermark.name, source)
	default:
		reason = convergeFollowingRecommendationReason
		message = fmt.Sprintf("Converging towards the %s (%s): following the recommendation", watermark.name, source)
	}
	setCondition(wpa, v1alpha1.WatermarkPodAutoscalerStatusConvergeToWatermark, status, reason, "%s", message)
	setConvergeConditionMetric(wpa, mode, status == corev1.ConditionTrue)
}

// setConvergeNotEvaluated reports that the convergence was not evaluated at this reconcile, so that neither the
// condition nor its metric keeps a previous "converging" state.
func setConvergeNotEvaluated(wpa *v1alpha1.WatermarkPodAutoscaler, cause string) {
	setCondition(wpa, v1alpha1.WatermarkPodAutoscalerStatusConvergeToWatermark, corev1.ConditionFalse, convergeNotEvaluatedReason, "Convergence not evaluated: %s", cause)
	setConvergeConditionMetric(wpa, "", false)
}

// tryToConvergeToWatermark will try to make the replicaCount slowly converge to a watermark.
// It will suggest converging until the estimated usage goes beyond the respective watermark.
// This feature is officially only supported with 1 metric.
func tryToConvergeToWatermarkForUsage(logger logr.Logger, convergingType v1alpha1.ConvergeTowardsWatermarkType, currentReplicas, currentReadyReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, adjustedUsage float64, lowMark, highMark *resource.Quantity) (optimisedReplicaCount int32) {
	// Compute lowerBound, higherBound and optimisedReplicaCount
	// We compute the maximum and minimum amount of replicas we think we can have without going over the watermarks.
	minReplicas := int32(1)
	if (wpa.Spec.MinReplicas != nil) && (*wpa.Spec.MinReplicas > 0) {
		minReplicas = *wpa.Spec.MinReplicas
	}
	maxReplicas := wpa.Spec.MaxReplicas

	var lowerBoundReplicaCount, upperBoundReplicaCount int32
	if highMark.MilliValue() == 0 {
		lowerBoundReplicaCount = currentReplicas
	} else {
		lowerBoundReplicaCount = math32.Cap(math32.Ceil(float64(currentReadyReplicas)/float64(highMark.MilliValue())*adjustedUsage), minReplicas, maxReplicas)
	}
	if lowMark.MilliValue() == 0 {
		upperBoundReplicaCount = currentReplicas
	} else {
		upperBoundReplicaCount = math32.Cap(math32.Floor(float64(currentReadyReplicas)/float64(lowMark.MilliValue())*adjustedUsage), minReplicas, maxReplicas)
	}
	return tryToConvergeToWatermark(logger, convergingType, currentReplicas, currentReadyReplicas, wpa, lowerBoundReplicaCount, upperBoundReplicaCount)
}

func tryToConvergeToWatermark(logger logr.Logger, convergingType v1alpha1.ConvergeTowardsWatermarkType, currentReplicas, currentReadyReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, lowerBoundReplicaCount, upperBoundReplicaCount int32) (optimisedReplicaCount int32) {
	scaleBy := *wpa.Spec.ReplicaScalingAbsoluteModulo

	switch convergingType {
	case v1alpha1.ConvergeUpwards:
		optimisedReplicaCount = math32.Max(currentReplicas-scaleBy, 1)
		if optimisedReplicaCount < lowerBoundReplicaCount {
			// This would result in a downscale, give up
			setConvergeCondition(wpa, convergingType, convergeConverged)
			logger.Info("Scaling down would likely make the usage go above the High Watermark", "optimisedReplicaCount", optimisedReplicaCount, "currentReadyReplicas", currentReadyReplicas, "lowerBoundReplicaCount", lowerBoundReplicaCount, "upperBoundReplicaCount", upperBoundReplicaCount)
			return currentReplicas
		}
		setConvergeCondition(wpa, convergingType, convergeStepping)
		// This should stay under HW
		logger.Info("Trying to scale down to converge to High Watermark", "optimisedReplicaCount", optimisedReplicaCount, "currentReadyReplicas", currentReadyReplicas, "lowerBoundReplicaCount", lowerBoundReplicaCount, "upperBoundReplicaCount", upperBoundReplicaCount)

	case v1alpha1.ConvergeDownwards:
		optimisedReplicaCount = currentReplicas + scaleBy

		if optimisedReplicaCount > upperBoundReplicaCount {
			// This would result in an upscale, give up
			setConvergeCondition(wpa, convergingType, convergeConverged)
			logger.Info("Scaling up would likely make the usage go below the Low Watermark", "optimisedReplicaCount", optimisedReplicaCount, "currentReadyReplicas", currentReadyReplicas, "lowerBoundReplicaCount", lowerBoundReplicaCount, "upperBoundReplicaCount", upperBoundReplicaCount)
			return currentReplicas
		}
		setConvergeCondition(wpa, convergingType, convergeStepping)
		// This should stay under HW
		logger.Info("Trying to scale up to converge to Low Watermark", "optimisedReplicaCount", optimisedReplicaCount, "currentReadyReplicas", currentReadyReplicas, "lowerBoundReplicaCount", lowerBoundReplicaCount, "upperBoundReplicaCount", upperBoundReplicaCount)

	default:
		setConvergeCondition(wpa, convergingType, convergeStepping) // no mode: ConvergenceDisabled
		optimisedReplicaCount = currentReplicas
	}
	return optimisedReplicaCount
}

func getReplicaCount(logger logr.Logger, currentReplicas, currentReadyReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, name string, adjustedUsage float64, lowMark, highMark *resource.Quantity) (replicaCount int32, utilization int64, position metricPosition) {
	utilizationQuantity := resource.NewMilliQuantity(int64(adjustedUsage), resource.DecimalSI)
	adjustedHM := float64(highMark.MilliValue() + highMark.MilliValue()*wpa.Spec.Tolerance.MilliValue()/1000.)
	adjustedLM := float64(lowMark.MilliValue() - lowMark.MilliValue()*wpa.Spec.Tolerance.MilliValue()/1000.)

	labelsWithReason := getPrometheusLabels(wpa)
	labelsWithReason[reasonPromLabel] = withinBoundsPromLabelVal
	labelsWithMetricName := getPrometheusLabels(wpa)
	labelsWithMetricName[metricNamePromLabel] = name

	value.With(labelsWithMetricName).Set(adjustedUsage)
	msg := ""
	converge, _ := effectiveConvergeTowardsWatermark(wpa)

	switch {
	// Upscale if usage > hw
	case adjustedUsage > adjustedHM:
		msg = "Value is above highMark"
		restrictedScaling.With(labelsWithReason).Set(0)
		replicaCount = getReplicaCountUpscale(logger, currentReplicas, currentReadyReplicas, wpa, adjustedUsage, highMark)
		setConvergeCondition(wpa, converge, convergeOutsideWatermarks)
		position.isAbove = true
		position.isBelow = false
	// Downscale if usage < lw
	case adjustedUsage < adjustedLM:
		msg = "Value is below lowMark"
		restrictedScaling.With(labelsWithReason).Set(0)
		replicaCount = getReplicaCountDownscale(logger, currentReplicas, currentReadyReplicas, wpa, adjustedUsage, lowMark)
		setConvergeCondition(wpa, converge, convergeOutsideWatermarks)
		position.isAbove = false
		position.isBelow = true
	// If within bounds, do nothing unless converging is enabled
	default:
		msg = "Within bounds of the watermarks"
		position.isAbove = false
		position.isBelow = false
		replicaCount = tryToConvergeToWatermarkForUsage(logger, converge, currentReplicas, currentReadyReplicas, wpa, adjustedUsage, lowMark, highMark)
	}

	logger.Info(msg, "usage", utilizationQuantity.String(), "replicaCount", replicaCount, "currentReadyReplicas", currentReadyReplicas, "tolerance (%)", float64(wpa.Spec.Tolerance.MilliValue())/10, "adjustedLM", adjustedLM, "adjustedHM", adjustedHM, "adjustedUsage", adjustedUsage)

	return replicaCount, utilizationQuantity.MilliValue(), position
}

func adjustReplicaCount(logger logr.Logger, currentReplicas, currentReadyReplicas int32, wpa *v1alpha1.WatermarkPodAutoscaler, recommendedReplicaCount, lowerBoundReplicas, upperBoundReplicas int32, hasBounds bool) (replicaCount int32, position metricPosition) {
	labelsWithReason := getPrometheusLabels(wpa)
	labelsWithReason[reasonPromLabel] = withinBoundsPromLabelVal
	msg := ""

	position.isBelow = currentReplicas > upperBoundReplicas
	position.isAbove = currentReplicas < lowerBoundReplicas
	// The controller default does not apply with a recommender: only the spec enables convergence.
	converge, _ := effectiveConvergeTowardsWatermark(wpa)
	// A missing bound reads as 0: only converge with both bounds.
	withinBounds := hasBounds && !position.isAbove && !position.isBelow
	scaleState := convergeFollowingRecommendation
	if !withinBounds {
		scaleState = convergeOutsideRecommenderBounds
	}

	switch {
	// Within the bounds, don't follow the recommendation against the convergence, or it flips between N and N-1.
	case withinBounds && converge == v1alpha1.ConvergeUpwards && recommendedReplicaCount >= currentReplicas,
		withinBounds && converge == v1alpha1.ConvergeDownwards && recommendedReplicaCount <= currentReplicas:
		msg = "Within bounds of the watermarks"
		// Cap the bounds like the metric path, so a step clamped by min/max is reported as unstable.
		minReplicas := int32(1)
		if (wpa.Spec.MinReplicas != nil) && (*wpa.Spec.MinReplicas > 0) {
			minReplicas = *wpa.Spec.MinReplicas
		}
		lowerBound := math32.Cap(lowerBoundReplicas, minReplicas, wpa.Spec.MaxReplicas)
		upperBound := math32.Cap(upperBoundReplicas, minReplicas, wpa.Spec.MaxReplicas)
		replicaCount = tryToConvergeToWatermark(logger, converge, currentReplicas, currentReadyReplicas, wpa, lowerBound, upperBound)
	// Upscale
	case recommendedReplicaCount > currentReplicas:
		msg = "Value is above highMark"
		restrictedScaling.With(labelsWithReason).Set(0)
		target := recommendedReplicaCount
		// Only scale back to the bound, or it swings between the bound and the recommendation.
		if hasBounds && converge == v1alpha1.ConvergeUpwards && position.isAbove {
			target = min(target, lowerBoundReplicas)
		}
		replicaCount = adjustReplicaCountForUpscale(logger, currentReplicas, currentReadyReplicas, wpa, target)
		setConvergeCondition(wpa, converge, scaleState)
	// Downscale
	case recommendedReplicaCount < currentReplicas:
		msg = "Value is below lowMark"
		restrictedScaling.With(labelsWithReason).Set(0)
		target := recommendedReplicaCount
		if hasBounds && converge == v1alpha1.ConvergeDownwards && position.isBelow {
			target = max(target, upperBoundReplicas)
		}
		replicaCount = adjustReplicaCountForDownscale(logger, currentReplicas, currentReadyReplicas, wpa, target)
		setConvergeCondition(wpa, converge, scaleState)
	// If within bounds, do nothing unless converging is enabled
	default:
		msg = "Within bounds of the watermarks"
		position.isAbove = false
		position.isBelow = false
		switch converge {
		// Outside the bounds, or without bounds: don't converge
		case v1alpha1.ConvergeUpwards, v1alpha1.ConvergeDownwards:
			msg = "Outside the recommender bounds, not converging"
			setConvergeCondition(wpa, converge, convergeOutsideRecommenderBounds)
			replicaCount = currentReplicas
		default:
			replicaCount = tryToConvergeToWatermark(logger, converge, currentReplicas, currentReadyReplicas, wpa, lowerBoundReplicas, upperBoundReplicas)
		}
	}

	logger.Info(msg, "replicaCount", replicaCount, "currentReadyReplicas", currentReadyReplicas, "recommendedReplicaCount", recommendedReplicaCount, "upperBoundReplicas", upperBoundReplicas, "lowerBoundReplicas", lowerBoundReplicas)

	return replicaCount, position
}

func (c *ReplicaCalculator) getReadyPodsCount(log logr.Logger, targetName string, podList []*corev1.Pod, readinessDelay time.Duration, skipOwnerCheck bool) (int32, int32, error) {
	toleratedAsReadyPodCount := 0
	var incorrectTargetPodsCount int
	for _, pod := range podList {
		// matchLabel might be too broad, use the OwnerRef to scope over the actual target.
		// When skipOwnerCheck is true, we trust the label selector from the Scale subresource.
		// This is needed for custom resources where pods are owned by intermediate resources
		// (e.g., CassandraDatacenter -> StatefulSet -> Pod).
		if !skipOwnerCheck {
			if ok := checkOwnerRef(pod.OwnerReferences, targetName); !ok {
				incorrectTargetPodsCount++
				continue
			}
		}

		// PodFailed
		//During a node-pressure eviction, the kubelet sets the phase for the selected pods to Failed, and terminates
		// the Pod. These pods should be ignored because they may not be garbage collected for a long time.
		// https://kubernetes.io/docs/concepts/scheduling-eviction/node-pressure-eviction/

		// PodSucceeded
		// A Deployment’s Pod should never be in PodSucceeded. If it is, it usually means:
		// - The Pod is running a one-shot script instead of a service.
		// - The restartPolicy is misconfigured.
		// - A Job-like process was accidentally set up in a Deployment.
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			incorrectTargetPodsCount++
			continue
		}

		_, condition := getPodCondition(&pod.Status, corev1.PodReady)
		// We can't distinguish pods that are past the Readiness in the lifecycle but have not reached it
		// and pods that are still Unschedulable but we don't need this level of granularity.
		// We do not want to account for pods that are being deleted either.
		if condition == nil || pod.Status.StartTime == nil || pod.DeletionTimestamp != nil {
			log.V(2).Info("Pod unready", "namespace", pod.Namespace, "name", pod.Name)
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

// shouldSkipOwnerCheck returns true if the WPA is annotated to skip pod OwnerReference validation.
func shouldSkipOwnerCheck(wpa *v1alpha1.WatermarkPodAutoscaler) bool {
	if wpa.Annotations == nil {
		return false
	}
	return wpa.Annotations[skipOwnerCheckAnnotationKey] == "true"
}

func checkOwnerRef(ownerRef []metav1.OwnerReference, targetName string) bool {
	for _, o := range ownerRef {
		mainOwnerRef := o.Name

		// For ReplicaSets managed by Deployments, we need to strip the generated suffix
		// to get the Deployment name (e.g., "myapp-7d8f9c5b6d" -> "myapp")
		if o.Kind == replicaSetKind {
			lastIndex := strings.LastIndex(o.Name, "-")
			if lastIndex > -1 {
				mainOwnerRef = o.Name[:lastIndex]
			}
		}

		// Check if the owner reference matches the target name
		// This now supports any Kind that implements the scale subresource
		// (Deployment, StatefulSet, custom resources, etc.)
		if mainOwnerRef == targetName {
			return true
		}
	}
	return false
}

func groupPods(logger logr.Logger, podList []*corev1.Pod, targetName string, metrics metricsclient.PodMetricsInfo, resource corev1.ResourceName, delayOfInitialReadinessStatus time.Duration, skipOwnerCheck bool) (readyPods, ignoredPods sets.Set[string]) {
	readyPods = sets.New[string]()
	ignoredPods = sets.New[string]()
	missing := sets.New[string]()
	var incorrectTargetPodsCount int
	for _, pod := range podList {
		// matchLabel might be too broad, use the OwnerRef to scope over the actual target.
		// When skipOwnerCheck is true, we trust the label selector from the Scale subresource.
		if !skipOwnerCheck {
			if ok := checkOwnerRef(pod.OwnerReferences, targetName); !ok {
				incorrectTargetPodsCount++
				continue
			}
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
				// Ignore metric if pod is unready and it has never been ready or is ready and the readiness_delay is not past.
				ignorePod = condition.Status == corev1.ConditionFalse || pod.Status.StartTime.Add(delayOfInitialReadinessStatus).After(condition.LastTransitionTime.Time)
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

func removeMetricsForPods(metrics metricsclient.PodMetricsInfo, pods sets.Set[string]) {
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

// Check that ReplicaCalculator implements ReplicaCalculatorItf
var _ ReplicaCalculatorItf = &ReplicaCalculator{}
