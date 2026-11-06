// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakescale "k8s.io/client-go/scale/fake"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1/test"
	"github.com/DataDog/watermarkpodautoscaler/pkg/config"
)

func TestSetStatusDefaults(t *testing.T) {
	highDefault := config.StaticProvider{Config: &config.ControllerConfig{
		Version:  config.ControllerConfigVersionV1,
		Defaults: config.Defaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
	}}

	tests := []struct {
		name           string
		provider       config.Provider
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
			provider:       config.StaticProvider{},
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

// TestReconcileStatusDefaults runs Reconcile against a fake API server and checks that the default is persisted
// in the status, through the status subresource, and never written to the spec.
func TestReconcileStatusDefaults(t *testing.T) {
	provider := config.StaticProvider{Config: &config.ControllerConfig{
		Version:  config.ControllerConfigVersionV1,
		Defaults: config.Defaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
	}}
	newWPA := func(defaulted bool) *v1alpha1.WatermarkPodAutoscaler {
		wpa := test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
			Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef: testCrossVersionObjectRef,
				MaxReplicas:    5,
				MinReplicas:    getReplicas(3),
				Metrics: []v1alpha1.MetricSpec{{
					Type: v1alpha1.ExternalMetricSourceType,
					External: &v1alpha1.ExternalMetricSource{
						MetricName:     "deadbeef",
						MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
						// invalid watermarks: the reconcile stops after the spec check, with a status update
						HighWatermark: resource.NewQuantity(3, resource.DecimalSI),
						LowWatermark:  resource.NewQuantity(4, resource.DecimalSI),
					},
				}},
			},
		})
		if defaulted {
			return v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
		}
		return wpa
	}

	tests := []struct {
		name         string
		defaulted    bool
		wantDefaults *v1alpha1.WatermarkPodAutoscalerStatusDefaults
	}{
		{
			name:         "invalid spec path persists the default in the status",
			defaulted:    true,
			wantDefaults: &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
		},
		{
			// the spec defaulting path writes the spec back with Client.Update: the default must not leak into it
			name:      "spec defaulting path does not write the default to the spec",
			defaulted: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(s))
			c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build()
			wpa := newWPA(tt.defaulted)
			require.NoError(t, c.Create(t.Context(), wpa))
			t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })

			r := &WatermarkPodAutoscalerReconciler{
				Client:        c,
				Scheme:        s,
				Log:           logf.Log,
				scaleClient:   &fakescale.FakeScaleClient{},
				restMapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				eventRecorder: events.NewFakeRecorder(10),
				Options:       Options{ConfigProvider: provider},
			}
			_, err := r.Reconcile(t.Context(), newRequest(testingNamespace, testingWPAName))
			require.NoError(t, err)

			got := &v1alpha1.WatermarkPodAutoscaler{}
			require.NoError(t, c.Get(t.Context(), types.NamespacedName{Namespace: testingNamespace, Name: testingWPAName}, got))
			assert.Empty(t, got.Spec.ConvergeTowardsWatermark, "the default must never be written to the spec")
			assert.Equal(t, tt.wantDefaults, got.Status.Defaults)
		})
	}
}
