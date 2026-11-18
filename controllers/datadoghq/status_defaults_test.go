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
		name         string
		provider     config.Provider
		specConverge v1alpha1.ConvergeTowardsWatermarkType
		recommender  bool
		wantStatus   *v1alpha1.WatermarkPodAutoscalerStatusDefaults
	}{
		{
			name: "no provider and no spec value",
		},
		{
			name:     "empty configuration",
			provider: config.StaticProvider{},
		},
		{
			name:       "default applied",
			provider:   highDefault,
			wantStatus: &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
		},
		// status.defaults reports the controller default on every WPA, whatever the spec and the recommender.
		{
			name:         "spec value set",
			provider:     highDefault,
			specConverge: v1alpha1.ConvergeDownwards,
			wantStatus:   &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
		},
		{
			name:         "spec disabled",
			provider:     highDefault,
			specConverge: v1alpha1.ConvergeDisabled,
			wantStatus:   &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
		},
		{
			name:         "unknown spec value",
			provider:     highDefault,
			specConverge: "bogus",
			wantStatus:   &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
		},
		{
			name:         "spec value without provider",
			specConverge: v1alpha1.ConvergeUpwards,
		},
		{
			name:        "recommender",
			provider:    highDefault,
			recommender: true,
			wantStatus:  &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
		},
		{
			name:         "recommender with a spec value",
			wantStatus:   &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
			provider:     highDefault,
			recommender:  true,
			specConverge: v1alpha1.ConvergeUpwards,
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
			if tt.recommender {
				wpa.Spec.Recommender = &v1alpha1.RecommenderSpec{URL: "http://recommender.example.com", TargetType: "cpu"}
			}
			specBefore := wpa.Spec.DeepCopy()

			r := &WatermarkPodAutoscalerReconciler{Options: Options{ConfigProvider: tt.provider}}
			r.setStatusDefaults(wpa)

			assert.Equal(t, tt.wantStatus, wpa.Status.Defaults)
			assert.Equal(t, specBefore, &wpa.Spec, "the spec must never be modified")
		})
	}
}

// TestReconcileStatusDefaults checks that the default is persisted in the status, never in the spec.
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
		name          string
		defaulted     bool
		specConverge  v1alpha1.ConvergeTowardsWatermarkType
		staleDefaults *v1alpha1.WatermarkPodAutoscalerStatusDefaults
		wantDefaults  *v1alpha1.WatermarkPodAutoscalerStatusDefaults
	}{
		{
			name:         "invalid spec path persists the default in the status",
			defaulted:    true,
			wantDefaults: &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
		},
		{
			// the default must not leak into the spec written by Client.Update
			name:      "spec defaulting path does not write the default to the spec",
			defaulted: false,
		},
		{
			name:          "a stale status.defaults is replaced by the controller default",
			defaulted:     true,
			specConverge:  v1alpha1.ConvergeDisabled,
			staleDefaults: &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeDisabled},
			wantDefaults:  &v1alpha1.WatermarkPodAutoscalerStatusDefaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(s))
			c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build()
			wpa := newWPA(tt.defaulted)
			wpa.Spec.ConvergeTowardsWatermark = tt.specConverge
			require.NoError(t, c.Create(t.Context(), wpa))
			if tt.staleDefaults != nil {
				wpa.Status.Defaults = tt.staleDefaults
				require.NoError(t, c.Status().Update(t.Context(), wpa))
			}
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
			assert.Equal(t, tt.specConverge, got.Spec.ConvergeTowardsWatermark, "the default must never be written to the spec")
			assert.Equal(t, tt.wantDefaults, got.Status.Defaults)
			if tt.defaulted {
				// the invalid spec stops the reconcile before the calculator: the convergence is not evaluated
				cond, err := getCondition(&got.Status, v1alpha1.WatermarkPodAutoscalerStatusConvergeToWatermark)
				require.NoError(t, err)
				assert.Equal(t, convergeNotEvaluatedReason, cond.Reason)
			}
		})
	}
}
