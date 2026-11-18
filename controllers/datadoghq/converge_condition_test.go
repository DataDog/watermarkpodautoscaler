// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

func newConvergeWPA(name string, spec v1alpha1.ConvergeTowardsWatermarkType, controllerDefault v1alpha1.ConvergeTowardsWatermarkType, recommender bool) *v1alpha1.WatermarkPodAutoscaler {
	wpa := &v1alpha1.WatermarkPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "converge"},
		Spec: v1alpha1.WatermarkPodAutoscalerSpec{
			ScaleTargetRef:           v1alpha1.CrossVersionObjectReference{Kind: "Deployment", Name: name},
			ConvergeTowardsWatermark: spec,
		},
	}
	if controllerDefault != "" {
		wpa.Status.Defaults = &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: controllerDefault}
	}
	if recommender {
		wpa.Spec.Recommender = &v1alpha1.RecommenderSpec{URL: "http://recommender.example.com", TargetType: "cpu"}
	}
	return wpa
}

func TestEffectiveConvergeTowardsWatermark(t *testing.T) {
	tests := []struct {
		name             string
		spec, controller v1alpha1.ConvergeTowardsWatermarkType
		recommender      bool
		wantMode         v1alpha1.ConvergeTowardsWatermarkType
		wantSource       string
	}{
		{name: "nothing set", wantSource: convergeSourceNotSet},
		{name: "controller default", controller: v1alpha1.ConvergeUpwards, wantMode: v1alpha1.ConvergeUpwards, wantSource: convergeSourceDefault},
		{name: "spec wins", spec: v1alpha1.ConvergeDisabled, controller: v1alpha1.ConvergeUpwards, wantMode: v1alpha1.ConvergeDisabled, wantSource: convergeSourceSpec},
		{name: "recommender ignores the default", controller: v1alpha1.ConvergeUpwards, recommender: true, wantSource: convergeSourceRecommender},
		{name: "recommender with a spec value", spec: v1alpha1.ConvergeDownwards, controller: v1alpha1.ConvergeUpwards, recommender: true, wantMode: v1alpha1.ConvergeDownwards, wantSource: convergeSourceSpec},
		{name: "recommender without default", recommender: true, wantSource: convergeSourceNotSet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, source := effectiveConvergeTowardsWatermark(newConvergeWPA("effective", tt.spec, tt.controller, tt.recommender))
			assert.Equal(t, tt.wantMode, mode)
			assert.Equal(t, tt.wantSource, source)
		})
	}
}

func TestSetConvergeCondition(t *testing.T) {
	tests := []struct {
		name             string
		spec, controller v1alpha1.ConvergeTowardsWatermarkType
		recommender      bool
		state            convergeState
		wantStatus       corev1.ConditionStatus
		wantReason       string
		wantMessage      string
		wantHigh         float64
		wantLow          float64
	}{
		{
			name: "stepping towards the high watermark from the controller default", controller: v1alpha1.ConvergeUpwards, state: convergeStepping,
			wantStatus: corev1.ConditionTrue, wantReason: convergingToHighWatermarkReason, wantHigh: 1,
			wantMessage: "Converging towards the high watermark (controller default): the next step keeps the usage under it",
		},
		{
			name: "stepping towards the low watermark from the spec", spec: v1alpha1.ConvergeDownwards, state: convergeStepping,
			wantStatus: corev1.ConditionTrue, wantReason: convergingToLowWatermarkReason, wantLow: 1,
			wantMessage: "Converging towards the low watermark (spec): the next step keeps the usage above it",
		},
		{
			name: "converged", spec: v1alpha1.ConvergeUpwards, state: convergeConverged,
			wantStatus: corev1.ConditionFalse, wantReason: convergedToHighWatermarkReason,
			wantMessage: "Converged towards the high watermark (spec): the next step would cross it",
		},
		{
			name: "outside the watermarks", controller: v1alpha1.ConvergeUpwards, state: convergeOutsideWatermarks,
			wantStatus: corev1.ConditionFalse, wantReason: convergeOutsideWatermarksReason,
			wantMessage: "Converging towards the high watermark (controller default) only between the watermarks",
		},
		{
			name: "outside the recommender bounds", spec: v1alpha1.ConvergeDownwards, recommender: true, state: convergeOutsideRecommenderBounds,
			wantStatus: corev1.ConditionFalse, wantReason: convergeOutsideRecommenderBoundsReason,
			wantMessage: "Converging towards the low watermark (spec) only within the recommender bounds",
		},
		{
			name: "following the recommendation", spec: v1alpha1.ConvergeUpwards, recommender: true, state: convergeFollowingRecommendation,
			wantStatus: corev1.ConditionFalse, wantReason: convergeFollowingRecommendationReason,
			wantMessage: "Converging towards the high watermark (spec): following the recommendation",
		},
		{
			name: "disabled in the spec", spec: v1alpha1.ConvergeDisabled, controller: v1alpha1.ConvergeUpwards, state: convergeStepping,
			wantStatus: corev1.ConditionFalse, wantReason: convergenceDisabledReason, wantMessage: "Convergence disabled (spec)",
		},
		{
			name: "disabled by the controller default", controller: v1alpha1.ConvergeDisabled, state: convergeOutsideWatermarks,
			wantStatus: corev1.ConditionFalse, wantReason: convergenceDisabledReason, wantMessage: "Convergence disabled (controller default)",
		},
		{
			name: "no mode", state: convergeOutsideWatermarks,
			wantStatus: corev1.ConditionFalse, wantReason: convergenceDisabledReason, wantMessage: "Convergence not enabled (not set)",
		},
		{
			name: "recommender: the default is not applied", controller: v1alpha1.ConvergeUpwards, recommender: true, state: convergeStepping,
			wantStatus: corev1.ConditionFalse, wantReason: convergenceDisabledReason,
			wantMessage: "Convergence not enabled (controller default not applied: the WPA uses a recommender)",
		},
		{
			name: "unknown mode", spec: "100%", state: convergeStepping,
			wantStatus: corev1.ConditionFalse, wantReason: convergenceDisabledReason,
			wantMessage: `Convergence not enabled: unknown mode "100%" (spec)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wpa := newConvergeWPA("set-converge-condition", tt.spec, tt.controller, tt.recommender)
			t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })

			mode, _ := effectiveConvergeTowardsWatermark(wpa)
			setConvergeCondition(wpa, mode, tt.state)

			cond, err := getCondition(&wpa.Status, v1alpha1.WatermarkPodAutoscalerStatusConvergeToWatermark)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, cond.Status)
			assert.Equal(t, tt.wantReason, cond.Reason)
			assert.Equal(t, tt.wantMessage, cond.Message)

			labels := getPromBaseLabels(wpa)
			labels[conditionPromLabel] = convergeConditionValues[v1alpha1.ConvergeUpwards]
			assert.InDelta(t, tt.wantHigh, getGaugeVal(t, conditions.With(labels)), 0, "converge_to_high_watermark")
			labels[conditionPromLabel] = convergeConditionValues[v1alpha1.ConvergeDownwards]
			assert.InDelta(t, tt.wantLow, getGaugeVal(t, conditions.With(labels)), 0, "converge_to_low_watermark")
		})
	}
}

func TestConvergeConditionMetricFollowsTheMode(t *testing.T) {
	wpa := newConvergeWPA("converge-metric-mode", v1alpha1.ConvergeUpwards, "", false)
	labels := getPromBaseLabels(wpa)
	high, low := convergeConditionValues[v1alpha1.ConvergeUpwards], convergeConditionValues[v1alpha1.ConvergeDownwards]

	setConvergeCondition(wpa, v1alpha1.ConvergeUpwards, convergeStepping)
	wpa.Spec.ConvergeTowardsWatermark = v1alpha1.ConvergeDownwards
	setConvergeCondition(wpa, v1alpha1.ConvergeDownwards, convergeStepping)
	labels[conditionPromLabel] = high
	assert.InDelta(t, 0, getGaugeVal(t, conditions.With(labels)), 0, "the previous direction is reset")
	labels[conditionPromLabel] = low
	assert.InDelta(t, 1, getGaugeVal(t, conditions.With(labels)), 0)

	// a deleted WPA removes both series
	cleanupAssociatedMetrics(wpa, false)
	for _, labelVal := range []string{high, low} {
		labels[conditionPromLabel] = labelVal
		assert.False(t, conditions.Delete(labels), "%s should have been deleted", labelVal)
	}
}

func TestSetConvergeNotEvaluated(t *testing.T) {
	wpa := newConvergeWPA("converge-not-evaluated", v1alpha1.ConvergeUpwards, "", false)
	t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })
	labels := getPromBaseLabels(wpa)
	labels[conditionPromLabel] = convergeConditionValues[v1alpha1.ConvergeUpwards]

	setConvergeCondition(wpa, v1alpha1.ConvergeUpwards, convergeStepping)
	require.InDelta(t, 1, getGaugeVal(t, conditions.With(labels)), 0)

	setConvergeNotEvaluated(wpa, "the replica count could not be computed")
	cond, err := getCondition(&wpa.Status, v1alpha1.WatermarkPodAutoscalerStatusConvergeToWatermark)
	require.NoError(t, err)
	assert.Equal(t, corev1.ConditionFalse, cond.Status)
	assert.Equal(t, convergeNotEvaluatedReason, cond.Reason)
	assert.Equal(t, "Convergence not evaluated: the replica count could not be computed", cond.Message)
	assert.InDelta(t, 0, getGaugeVal(t, conditions.With(labels)), 0, "a stale converging series is reset")
}

func TestUpdatePredicateTargetChangeDropsConditionSeries(t *testing.T) {
	oldWPA := newConvergeWPA("target-change", v1alpha1.ConvergeUpwards, "", false)
	setConvergeCondition(oldWPA, v1alpha1.ConvergeUpwards, convergeStepping)
	newWPA := oldWPA.DeepCopy()
	newWPA.Spec.ScaleTargetRef.Name = "other-target"
	t.Cleanup(func() { cleanupAssociatedMetrics(newWPA, false) })

	require.True(t, updatePredicate(event.UpdateEvent{ObjectOld: oldWPA, ObjectNew: newWPA}))
	labels := getPromBaseLabels(oldWPA)
	for _, labelVal := range convergeConditionValues {
		labels[conditionPromLabel] = labelVal
		assert.False(t, conditions.Delete(labels), "the series of the old target should have been deleted: %s", labelVal)
	}
}

// TestGetReplicaCountConvergeCondition checks the condition set by the metric path in each region of the usage.
func TestGetReplicaCountConvergeCondition(t *testing.T) {
	low, high := resource.NewMilliQuantity(90000, resource.DecimalSI), resource.NewMilliQuantity(150000, resource.DecimalSI)
	tests := []struct {
		name         string
		usage        float64
		wantReason   string
		wantReplicas int32
	}{
		{name: "above the high watermark", usage: 200000, wantReason: convergeOutsideWatermarksReason, wantReplicas: 14},
		{name: "below the low watermark", usage: 50000, wantReason: convergeOutsideWatermarksReason, wantReplicas: 5},
		{name: "between the watermarks", usage: 100000, wantReason: convergingToHighWatermarkReason, wantReplicas: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wpa := newConvergeWPA("get-replica-count", "", v1alpha1.ConvergeUpwards, false)
			wpa.Spec.MaxReplicas = 20
			wpa.Spec.ReplicaScalingAbsoluteModulo = v1alpha1.NewInt32(1)
			t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })

			replicas, _, _ := getReplicaCount(logf.Log, 10, 10, wpa, "metric", tt.usage, low, high)
			assert.Equal(t, tt.wantReplicas, replicas)
			cond, err := getCondition(&wpa.Status, v1alpha1.WatermarkPodAutoscalerStatusConvergeToWatermark)
			require.NoError(t, err)
			assert.Equal(t, tt.wantReason, cond.Reason)
		})
	}
}
