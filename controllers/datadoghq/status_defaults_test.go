// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/pkg/config"
)

func TestSetStatusDefaults(t *testing.T) {
	highDefault := config.StaticConfigProvider{Config: &config.ControllerConfig{
		Version:  config.ControllerConfigVersionV1,
		Defaults: config.Defaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
	}}

	tests := []struct {
		name           string
		provider       config.ConfigProvider
		specConverge   v1alpha1.ConvergeTowardsWatermarkType
		wantStatus     *v1alpha1.WatermarkPodAutoscalerStatusDefaults
		wantActiveMode string
	}{
		{
			name:           "no provider and no spec value",
			wantActiveMode: convergeModeNone,
		},
		{
			name:           "empty configuration",
			provider:       config.StaticConfigProvider{},
			wantActiveMode: convergeModeNone,
		},
		{
			name:           "default applied",
			provider:       highDefault,
			wantStatus:     &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
			wantActiveMode: "highwatermark",
		},
		{
			name:           "spec value wins over the default",
			provider:       highDefault,
			specConverge:   v1alpha1.ConvergeDownwards,
			wantStatus:     &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeDownwards},
			wantActiveMode: "lowwatermark",
		},
		{
			name:           "spec disabled opts out of the default",
			provider:       highDefault,
			specConverge:   v1alpha1.ConvergeDisabled,
			wantStatus:     &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeDisabled},
			wantActiveMode: "disabled",
		},
		{
			name:           "unknown spec value is reported as is, metric uses none",
			provider:       highDefault,
			specConverge:   "bogus",
			wantStatus:     &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: "bogus"},
			wantActiveMode: convergeModeNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wpa := &v1alpha1.WatermarkPodAutoscaler{
				ObjectMeta: metav1.ObjectMeta{Name: "status-defaults-" + tt.name, Namespace: "ns"},
				Spec: v1alpha1.WatermarkPodAutoscalerSpec{
					ScaleTargetRef:           v1alpha1.CrossVersionObjectReference{Kind: "Deployment", Name: "app"},
					ConvergeTowardsWatermark: tt.specConverge,
				},
				// a stale value from a previous reconcile must be overwritten
				Status: v1alpha1.WatermarkPodAutoscalerStatus{Defaults: &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: "stale"}},
			}
			specBefore := wpa.Spec.DeepCopy()
			t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })

			r := &WatermarkPodAutoscalerReconciler{Options: Options{ConfigProvider: tt.provider}}
			r.setStatusDefaults(wpa)

			assert.Equal(t, tt.wantStatus, wpa.Status.Defaults)
			assert.Equal(t, specBefore, &wpa.Spec, "the spec must never be modified")

			labels := getPromBaseLabels(wpa)
			for _, mode := range convergeModeValues {
				labels[modePromLabel] = mode
				want := 0.0
				if mode == tt.wantActiveMode {
					want = 1
				}
				assert.InDelta(t, want, getGaugeVal(t, convergeTowardsWatermarkMode.With(labels)), 0, "mode %s", mode)
			}
		})
	}
}

func TestConvergeTowardsWatermarkMetricFollowsModeChange(t *testing.T) {
	wpa := &v1alpha1.WatermarkPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "mode-change", Namespace: "ns"},
		Spec:       v1alpha1.WatermarkPodAutoscalerSpec{ScaleTargetRef: v1alpha1.CrossVersionObjectReference{Kind: "Deployment", Name: "app"}},
	}
	t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })
	r := &WatermarkPodAutoscalerReconciler{}

	wpa.Spec.ConvergeTowardsWatermark = v1alpha1.ConvergeUpwards
	r.setStatusDefaults(wpa)
	wpa.Spec.ConvergeTowardsWatermark = v1alpha1.ConvergeDisabled
	r.setStatusDefaults(wpa)

	labels := getPromBaseLabels(wpa)
	labels[modePromLabel] = "highwatermark"
	assert.InDelta(t, 0.0, getGaugeVal(t, convergeTowardsWatermarkMode.With(labels)), 0, "previous mode must be reset to 0")
	labels[modePromLabel] = "disabled"
	assert.InDelta(t, 1.0, getGaugeVal(t, convergeTowardsWatermarkMode.With(labels)), 0)
}

func TestConvergeTowardsWatermarkMetricCleanup(t *testing.T) {
	wpa := &v1alpha1.WatermarkPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "cleanup", Namespace: "ns"},
		Spec:       v1alpha1.WatermarkPodAutoscalerSpec{ScaleTargetRef: v1alpha1.CrossVersionObjectReference{Kind: "Deployment", Name: "app"}},
	}
	(&WatermarkPodAutoscalerReconciler{}).setStatusDefaults(wpa)

	// metrics-only cleanup (spec metrics changed) keeps the series
	cleanupAssociatedMetrics(wpa, true)
	labels := getPromBaseLabels(wpa)
	labels[modePromLabel] = convergeModeNone
	assert.True(t, convergeTowardsWatermarkMode.Delete(labels), "series must survive a metrics-only cleanup")
	(&WatermarkPodAutoscalerReconciler{}).setStatusDefaults(wpa)

	// full cleanup (WPA deleted) removes every mode series
	cleanupAssociatedMetrics(wpa, false)
	for _, mode := range convergeModeValues {
		labels[modePromLabel] = mode
		assert.False(t, convergeTowardsWatermarkMode.Delete(labels), "mode %s should have been deleted", mode)
	}
}

func TestAllWPARequests(t *testing.T) {
	scheme := fake.NewClientBuilder().Build().Scheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1alpha1.WatermarkPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"}},
		&v1alpha1.WatermarkPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns2"}},
	).Build()
	r := &WatermarkPodAutoscalerReconciler{Client: c, Log: logf.Log}

	requests := r.allWPARequests(t.Context(), nil)
	var got []types.NamespacedName
	for _, req := range requests {
		got = append(got, req.NamespacedName)
	}
	assert.ElementsMatch(t, []types.NamespacedName{{Namespace: "ns1", Name: "a"}, {Namespace: "ns2", Name: "b"}}, got)
}
