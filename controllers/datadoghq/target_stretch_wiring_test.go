// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/scheme"
	fakescale "k8s.io/client-go/scale/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	sigmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/pkg/config"
	"github.com/DataDog/watermarkpodautoscaler/third_party/kubernetes/pkg/controller/podautoscaler/metrics"
)

// watermarkRecommender is a recommender test double: it answers 10 replicas when the request carries the
// stretched upper bound (84) and 12 otherwise, and records every request.
type watermarkRecommender struct {
	mu       sync.Mutex
	requests []ReplicaRecommendationRequest
	err      error
}

func (w *watermarkRecommender) GetReplicaRecommendation(_ context.Context, req *ReplicaRecommendationRequest) (*ReplicaRecommendationResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.requests = append(w.requests, *req)
	if w.err != nil {
		return nil, w.err
	}
	replicas := 12
	if req.Recommender.HighWatermark.MilliValue() == 84000 {
		replicas = 10
	}
	return &ReplicaRecommendationResponse{Replicas: replicas, ReplicasLowerBound: replicas, ReplicasUpperBound: replicas, Timestamp: time.Now(), ObservedTargetValue: 78}, nil
}

type stretchWiring struct {
	r              *WatermarkPodAutoscalerReconciler
	metricsQueries *int
	reco           *watermarkRecommender
}

// newStretchWiring builds a reconciler with the real ReplicaCalculator on fake metrics clients and a fake API client.
func newStretchWiring(t *testing.T, tc *replicaCalcTestCase, wpa *v1alpha1.WatermarkPodAutoscaler, cfg config.TargetStretchConfig) *stretchWiring {
	t.Helper()
	tc.namespace = testNamespace
	podClient := tc.prepareTestClientSet()
	informerFactory := informers.NewSharedInformerFactory(podClient, 0)
	informer := informerFactory.Core().V1().Pods()
	podLister := informer.Lister() // registers the informer before Start

	queries := 0
	countQueries := func(action core.Action) {
		if action.GetVerb() == "list" {
			queries++
		}
	}
	emClient := tc.getFakeEMClient(t)
	emClient.PrependReactor("list", "*", func(action core.Action) (bool, runtime.Object, error) {
		if action.GetResource().Resource == "pods" {
			return false, nil, nil
		}
		countQueries(action)
		return false, nil, nil
	})
	rClient := tc.getFakeResourceClient()
	rClient.PrependReactor("list", "pods", func(action core.Action) (bool, runtime.Object, error) {
		countQueries(action)
		return false, nil, nil
	})
	mClient := metrics.NewRESTMetricsClient(rClient.MetricsV1beta1(), nil, emClient)
	reco := &watermarkRecommender{}

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	informerFactory.Start(stop)
	require.True(t, cache.WaitForNamedCacheSync("HPA", stop, informer.Informer().HasSynced))

	s := scheme.Scheme
	s.AddKnownTypes(appsv1.SchemeGroupVersion, &appsv1.Deployment{})
	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &v1alpha1.WatermarkPodAutoscaler{}, &v1alpha1.WatermarkPodAutoscalerList{})
	c := fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build()
	require.NoError(t, c.Create(t.Context(), wpa))

	fsc := &fakescale.FakeScaleClient{}
	fsc.AddReactor("get", "deployments", func(core.Action) (bool, runtime.Object, error) {
		return true, tc.scale.DeepCopy(), nil
	})
	fsc.AddReactor("update", "deployments", func(action core.Action) (bool, runtime.Object, error) {
		return true, action.(core.UpdateAction).GetObject(), nil
	})

	r := &WatermarkPodAutoscalerReconciler{
		Client:        c,
		Log:           logf.Log,
		Scheme:        s,
		scaleClient:   fsc,
		restMapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
		syncPeriod:    defaultSyncPeriod,
		eventRecorder: events.NewFakeRecorder(100),
		replicaCalc:   NewReplicaCalculator(mClient, reco, podLister, "test-cluster"),
		Options:       Options{TargetStretch: &config.StaticTargetStretchConfigProvider{Config: cfg}},
	}
	return &stretchWiring{r: r, metricsQueries: &queries, reco: reco}
}

func (w *stretchWiring) reconcile(t *testing.T, wpa *v1alpha1.WatermarkPodAutoscaler) *v1alpha1.WatermarkPodAutoscaler {
	t.Helper()
	key := types.NamespacedName{Namespace: wpa.Namespace, Name: wpa.Name}
	_, err := w.r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	persisted := &v1alpha1.WatermarkPodAutoscaler{}
	require.NoError(t, w.r.Client.Get(t.Context(), key, persisted))
	return persisted
}

func wiringWPA(metric *v1alpha1.MetricSpec, reco *v1alpha1.RecommenderSpec) *v1alpha1.WatermarkPodAutoscaler {
	wpa := &v1alpha1.WatermarkPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   testNamespace,
			Name:        "stretch-wiring",
			Annotations: map[string]string{v1alpha1.TargetStretchEnabledAnnotationKey: "true"},
		},
		Spec: v1alpha1.WatermarkPodAutoscalerSpec{
			ScaleTargetRef: v1alpha1.CrossVersionObjectReference{Kind: "Deployment", Name: testDeploymentName, APIVersion: "apps/v1"},
			Algorithm:      "absolute",
			Recommender:    reco,
			MinReplicas:    v1alpha1.NewInt32(1),
			MaxReplicas:    100,
		},
	}
	if metric != nil {
		wpa.Spec.Metrics = []v1alpha1.MetricSpec{*metric}
	}
	return v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
}

var wiringSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"name": podNamePrefix}}

func externalWiringMetric() v1alpha1.MetricSpec {
	return v1alpha1.MetricSpec{Type: v1alpha1.ExternalMetricSourceType, External: &v1alpha1.ExternalMetricSource{
		MetricName: "queue", MetricSelector: wiringSelector,
		LowWatermark: resource.NewQuantity(50, resource.DecimalSI), HighWatermark: resource.NewQuantity(70, resource.DecimalSI),
	}}
}

func resourceWiringMetric() v1alpha1.MetricSpec {
	return v1alpha1.MetricSpec{Type: v1alpha1.ResourceMetricSourceType, Resource: &v1alpha1.ResourceMetricSource{
		Name: corev1.ResourceCPU, MetricSelector: wiringSelector,
		LowWatermark: resource.NewQuantity(50, resource.DecimalSI), HighWatermark: resource.NewQuantity(70, resource.DecimalSI),
	}}
}

// Usage 78 for 10 ready replicas with the default 10% tolerance: the spec watermarks 50/70 scale to 12,
// the watermarks stretched by 1.2 (60/84) keep 10.
func TestTargetStretchReconcileWithRealCalculator(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	cpuLevels := []int64{7800, 7800, 7800, 7800, 7800, 7800, 7800, 7800, 7800, 7800}
	tests := []struct {
		name   string
		metric v1alpha1.MetricSpec
		levels []int64
	}{
		{"external", externalWiringMetric(), []int64{78000}},
		{"resource", resourceWiringMetric(), cpuLevels},
	}
	for _, tt := range tests {
		for _, mode := range []config.TargetStretchMode{config.TargetStretchModeEnabledOptIn, config.TargetStretchModeDryRunOptIn} {
			t.Run(tt.name+"/"+string(mode), func(t *testing.T) {
				metric := tt.metric
				tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: tt.levels}, timestamp: time.Now()}
				wpa := wiringWPA(&metric, nil)
				w := newStretchWiring(t, tc, wpa, stretchCfg(mode, 1200))

				got := w.reconcile(t, wpa)

				ts := got.Status.TargetStretch
				require.NotNil(t, ts, "status.targetStretch must be persisted")
				assert.Equal(t, string(mode), ts.Mode)
				assert.Equal(t, "1.2", ts.AppliedRatio)
				require.Len(t, ts.EffectiveWatermarks, 1)
				assert.Equal(t, int64(60000), ts.EffectiveWatermarks[0].LowWatermark.MilliValue())
				assert.Equal(t, int64(84000), ts.EffectiveWatermarks[0].HighWatermark.MilliValue())
				specHW := got.Spec.Metrics[0].Resource
				if specHW == nil {
					assert.Equal(t, int64(70000), got.Spec.Metrics[0].External.HighWatermark.MilliValue(), "spec must not be mutated")
				} else {
					assert.Equal(t, int64(70000), specHW.HighWatermark.MilliValue(), "spec must not be mutated")
				}
				assert.Equal(t, 1, *w.metricsQueries, "the metric is fetched once, the shadow reuses it")
				if mode.IsEnforced() {
					assert.Equal(t, int32(10), got.Status.DesiredReplicas, "enforce scales on the stretched watermarks")
					assert.Nil(t, ts.ReplicasRecommended)
				} else {
					assert.Equal(t, int32(12), got.Status.DesiredReplicas, "dry-run scales on the spec watermarks")
					require.NotNil(t, ts.ReplicasRecommended)
					assert.Equal(t, int32(10), *ts.ReplicasRecommended)
				}
			})
		}
	}
}

func recommenderWiringSpec() *v1alpha1.RecommenderSpec {
	return &v1alpha1.RecommenderSpec{
		URL: "http://reco", TargetType: "cpu", Settings: map[string]string{},
		LowWatermark: resource.NewQuantity(50, resource.DecimalSI), HighWatermark: resource.NewQuantity(70, resource.DecimalSI),
	}
}

func TestTargetStretchReconcileWithRecommender(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	for _, mode := range []config.TargetStretchMode{config.TargetStretchModeEnabledOptIn, config.TargetStretchModeDryRunOptIn} {
		t.Run(string(mode), func(t *testing.T) {
			tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{}}
			wpa := wiringWPA(nil, recommenderWiringSpec())
			w := newStretchWiring(t, tc, wpa, stretchCfg(mode, 1200))

			got := w.reconcile(t, wpa)

			ts := got.Status.TargetStretch
			require.NotNil(t, ts)
			if mode.IsEnforced() {
				require.Len(t, w.reco.requests, 1)
				assert.Equal(t, int64(60000), w.reco.requests[0].Recommender.LowWatermark.MilliValue(), "lowerBound is stretched")
				assert.Equal(t, int64(84000), w.reco.requests[0].Recommender.HighWatermark.MilliValue(), "upperBound is stretched")
				assert.Equal(t, int32(10), got.Status.DesiredReplicas)
			} else {
				require.Len(t, w.reco.requests, 2, "dry-run calls the recommender with the spec and the stretched bounds")
				assert.Equal(t, int64(70000), w.reco.requests[0].Recommender.HighWatermark.MilliValue())
				assert.Equal(t, int64(84000), w.reco.requests[1].Recommender.HighWatermark.MilliValue())
				assert.Equal(t, int32(12), got.Status.DesiredReplicas)
				require.NotNil(t, ts.ReplicasRecommended)
				assert.Equal(t, int32(10), *ts.ReplicasRecommended)
			}
			assert.Equal(t, int64(70000), got.Spec.Recommender.HighWatermark.MilliValue(), "spec must not be mutated")
		})
	}
}

// The dry-run recommender shadow must not touch the live value/restricted_scaling gauges, on success or error.
func TestRecommenderShadowLeavesLiveGaugesUntouched(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{}}
	wpa := wiringWPA(nil, recommenderWiringSpec())
	w := newStretchWiring(t, tc, wpa, stretchCfg(config.TargetStretchModeDryRunOptIn, 1200))
	calc := w.r.replicaCalc

	valueLabels := getPrometheusLabels(wpa)
	valueLabels[metricNamePromLabel] = metricNameForRecommender(&wpa.Spec)
	restrictedLabels := getPrometheusLabels(wpa)
	restrictedLabels[reasonPromLabel] = withinBoundsPromLabelVal
	value.With(valueLabels).Set(42)
	restrictedScaling.With(restrictedLabels).Set(7)
	t.Cleanup(func() {
		value.Delete(valueLabels)
		restrictedScaling.Delete(restrictedLabels)
	})
	stretched := effectiveRecommender(wpa.Spec.Recommender, 1200)

	// Success, with a recommendation that differs from the current replicas (upscale branch).
	scale := tenPods.DeepCopy()
	scale.Spec.Replicas = 8
	_, err := calc.GetRecommenderShadowReplicas(t.Context(), logf.Log, scale, wpa.DeepCopy(), stretched)
	require.NoError(t, err)
	assert.InDelta(t, 42.0, testutil.ToFloat64(value.With(valueLabels)), 0)
	assert.InDelta(t, 7.0, testutil.ToFloat64(restrictedScaling.With(restrictedLabels)), 0)

	// Error: the live path deletes these series, the shadow must not.
	w.reco.err = assert.AnError
	_, err = calc.GetRecommenderShadowReplicas(t.Context(), logf.Log, tenPods, wpa.DeepCopy(), stretched)
	require.Error(t, err)
	assert.InDelta(t, 42.0, testutil.ToFloat64(value.With(valueLabels)), 0)
	// Delete reports whether the series still existed (the live error path would have deleted it).
	assert.True(t, restrictedScaling.Delete(restrictedLabels), "restricted_scaling series must survive a shadow error")
}

// Usage 70 on 10 ready replicas with LW/HW 50/70 and convergeTowardsWatermark=highwatermark: the spec watermarks
// cannot converge (9 replicas would exceed HW 70), the watermarks stretched by 1.2 (HW 84) allow converging to 9.
func TestTargetStretchReconcileConvergence(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	for _, mode := range []config.TargetStretchMode{config.TargetStretchModeEnabledOptIn, config.TargetStretchModeDryRunOptIn} {
		t.Run(string(mode), func(t *testing.T) {
			metric := externalWiringMetric()
			tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{70000}}, timestamp: time.Now()}
			wpa := wiringWPA(&metric, nil)
			wpa.Spec.ConvergeTowardsWatermark = v1alpha1.ConvergeUpwards
			w := newStretchWiring(t, tc, wpa, stretchCfg(mode, 1200))

			got := w.reconcile(t, wpa)

			var converge *autoscalingv2.HorizontalPodAutoscalerCondition
			for i := range got.Status.Conditions {
				if got.Status.Conditions[i].Type == v1alpha1.WatermarkPodAutoscalerStatusConvergeToWatermark {
					converge = &got.Status.Conditions[i]
				}
			}
			require.NotNil(t, converge, "the persisted WPA carries the convergence condition")
			ts := got.Status.TargetStretch
			require.NotNil(t, ts)
			if mode.IsEnforced() {
				assert.Equal(t, int32(9), got.Status.DesiredReplicas, "converges on the stretched high watermark")
				assert.Equal(t, corev1.ConditionTrue, converge.Status)
				assert.Nil(t, ts.ReplicasRecommended)
			} else {
				assert.Equal(t, int32(10), got.Status.DesiredReplicas, "the real path keeps the spec watermarks")
				require.NotNil(t, ts.ReplicasRecommended)
				assert.Equal(t, int32(9), *ts.ReplicasRecommended, "the shadow converges on the stretched watermarks")
				// The shadow converges (it would set True); the persisted condition must come from the real path.
				assert.Equal(t, corev1.ConditionFalse, converge.Status, "shadow conditions must not leak into the persisted status")
				assert.Equal(t, convergedToHighWatermarkReason, converge.Reason)
				l := getPrometheusLabels(got)
				l[conditionPromLabel] = convergeConditionValues[v1alpha1.ConvergeUpwards]
				assert.InDelta(t, 0, testutil.ToFloat64(conditions.With(l)), 0, "the shadow must not overwrite the live convergence gauge")
			}
		})
	}
}

// After a cap change, a failed live metric lookup must not leave the previous shadow recommendation in the
// status or in the per-WPA gauge.
func TestTargetStretchShadowClearedOnFailedRecalculation(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	metric := externalWiringMetric()
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{78000}}, timestamp: time.Now()}
	wpa := wiringWPA(&metric, nil)
	w := newStretchWiring(t, tc, wpa, stretchCfg(config.TargetStretchModeDryRunOptIn, 1200))
	labels := getPrometheusLabels(wpa)
	t.Cleanup(func() { cleanupTargetStretchMetrics(wpa) })

	got := w.reconcile(t, wpa)
	require.NotNil(t, got.Status.TargetStretch.ReplicasRecommended)
	assert.Equal(t, int32(10), *got.Status.TargetStretch.ReplicasRecommended)
	assert.InDelta(t, 10, testutil.ToFloat64(targetStretchReplicasRecommended.With(labels)), 0)

	// Lower the cap and make the live external metric lookup fail.
	got.Annotations[v1alpha1.TargetStretchMaxRatioAnnotationKey] = "1.1"
	require.NoError(t, w.r.Client.Update(t.Context(), got))
	tc.metric.spec = v1alpha1.MetricSpec{}

	got = w.reconcile(t, wpa)
	ts := got.Status.TargetStretch
	require.NotNil(t, ts)
	assert.Equal(t, "1.1", ts.AppliedRatio, "the new cap is applied")
	assert.Nil(t, ts.ReplicasRecommended, "the stale shadow recommendation must be cleared")
	assert.False(t, targetStretchReplicasRecommended.Delete(labels), "the stale shadow gauge must be removed")
}

// hookedCalculator runs a hook before delegating external metric lookups, to observe state mid-reconcile.
type hookedCalculator struct {
	ReplicaCalculatorItf

	beforeExternal func()
}

func (h *hookedCalculator) GetExternalMetricReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (ReplicaCalculation, error) {
	if h.beforeExternal != nil {
		h.beforeExternal()
	}
	return h.ReplicaCalculatorItf.GetExternalMetricReplicas(ctx, logger, target, metric, wpa)
}

// On a healthy dry-run reconcile the shadow gauge must stay exported while the reconcile runs.
func TestTargetStretchShadowGaugeContinuity(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	metric := externalWiringMetric()
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{78000}}, timestamp: time.Now()}
	wpa := wiringWPA(&metric, nil)
	w := newStretchWiring(t, tc, wpa, stretchCfg(config.TargetStretchModeDryRunOptIn, 1200))
	labels := getPrometheusLabels(wpa)
	t.Cleanup(func() { cleanupTargetStretchMetrics(wpa) })

	w.reconcile(t, wpa)
	require.InDelta(t, 10, testutil.ToFloat64(targetStretchReplicasRecommended.With(labels)), 0)

	presentDuring := false
	w.r.replicaCalc = &hookedCalculator{ReplicaCalculatorItf: w.r.replicaCalc, beforeExternal: func() {
		// Delete reports whether the series exists without creating it; restore it right away.
		presentDuring = targetStretchReplicasRecommended.Delete(labels)
		if presentDuring {
			targetStretchReplicasRecommended.With(labels).Set(10)
		}
	}}
	got := w.reconcile(t, wpa)

	assert.True(t, presentDuring, "the shadow gauge must not disappear during a healthy reconcile")
	require.NotNil(t, got.Status.TargetStretch.ReplicasRecommended)
	assert.InDelta(t, 10, testutil.ToFloat64(targetStretchReplicasRecommended.With(labels)), 0)
}

func stretchCondition(wpa *v1alpha1.WatermarkPodAutoscaler) *autoscalingv2.HorizontalPodAutoscalerCondition {
	for i := range wpa.Status.Conditions {
		if wpa.Status.Conditions[i].Type == v1alpha1.WatermarkPodAutoscalerStatusTargetStretched {
			return &wpa.Status.Conditions[i]
		}
	}
	return nil
}

// A WPA blocked by lifecycle control (no DatadogMonitor) that opts out must drop its stretch state, without
// any scaling or shadow computation.
func TestTargetStretchLifecycleBlockedOptOut(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	metric := externalWiringMetric()
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{78000}}, timestamp: time.Now()}
	wpa := wiringWPA(&metric, nil)
	w := newStretchWiring(t, tc, wpa, stretchCfg(config.TargetStretchModeEnabledOptIn, 1200))
	labels := getPrometheusLabels(wpa)
	t.Cleanup(func() { cleanupTargetStretchMetrics(wpa) })

	got := w.reconcile(t, wpa)
	require.NotNil(t, got.Status.TargetStretch)
	require.True(t, targetStretchAppliedRatio.Delete(labels))
	targetStretchAppliedRatio.With(labels).Set(1.2)
	queries := *w.metricsQueries

	got.Annotations[lifecycleControlEnabledAnnotationKey] = "true"
	got.Annotations[v1alpha1.TargetStretchEnabledAnnotationKey] = "false"
	require.NoError(t, w.r.Client.Update(t.Context(), got))
	got = w.reconcile(t, wpa)

	blocked, err := getCondition(&got.Status, v1alpha1.ScalingBlocked)
	require.NoError(t, err)
	assert.Equal(t, corev1.ConditionTrue, blocked.Status, "the WPA is blocked by lifecycle control")
	assert.Nil(t, got.Status.TargetStretch, "the stretch block is cleared on the blocked path")
	cond := stretchCondition(got)
	require.NotNil(t, cond)
	assert.Equal(t, v1alpha1.TargetStretchReasonOptedOut, cond.Reason)
	assert.False(t, targetStretchAppliedRatio.Delete(labels), "per-WPA stretch gauges are removed")
	assert.Equal(t, queries, *w.metricsQueries, "no scaling computation on the blocked path")
}

// An invalid WPA must follow the stretch mode too: a dry-run shadow gauge is cleared (no shadow is computed),
// and switching the cluster to disabled clears the status block.
func TestTargetStretchInvalidSpecTransitions(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	metric := externalWiringMetric()
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{78000}}, timestamp: time.Now()}
	wpa := wiringWPA(&metric, nil)
	w := newStretchWiring(t, tc, wpa, stretchCfg(config.TargetStretchModeDryRunOptIn, 1200))
	labels := getPrometheusLabels(wpa)
	t.Cleanup(func() { cleanupTargetStretchMetrics(wpa) })

	got := w.reconcile(t, wpa)
	require.NotNil(t, got.Status.TargetStretch.ReplicasRecommended)
	require.InDelta(t, 10, testutil.ToFloat64(targetStretchReplicasRecommended.With(labels)), 0)
	queries := *w.metricsQueries

	got.Spec.MaxReplicas = 0 // below minReplicas: invalid spec
	require.NoError(t, w.r.Client.Update(t.Context(), got))
	got = w.reconcile(t, wpa)
	require.NotNil(t, got.Status.TargetStretch, "still selected in dry-run")
	assert.Nil(t, got.Status.TargetStretch.ReplicasRecommended, "no shadow on the invalid-spec path")
	assert.False(t, targetStretchReplicasRecommended.Delete(labels), "the stale shadow gauge is removed")
	assert.Equal(t, queries, *w.metricsQueries, "no scaling computation on the invalid-spec path")

	w.r.Options.TargetStretch = &config.StaticTargetStretchConfigProvider{Config: config.DisabledTargetStretchConfig()}
	got = w.reconcile(t, wpa)
	assert.Nil(t, got.Status.TargetStretch, "disabled clears the stretch block on the invalid-spec path")
	cond := stretchCondition(got)
	require.NotNil(t, cond)
	assert.Equal(t, v1alpha1.TargetStretchReasonDisabled, cond.Reason)
	assert.False(t, targetStretchAppliedRatio.Delete(labels))
	assert.Equal(t, queries, *w.metricsQueries)
}

// gatedGetClient pauses the first gated WPA Get after reading it, to interleave a reconcile with a spec update.
type gatedGetClient struct {
	client.Client

	gate    bool
	got     chan struct{}
	release chan struct{}
}

func (c *gatedGetClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	err := c.Client.Get(ctx, key, obj, opts...)
	if _, isWPA := obj.(*v1alpha1.WatermarkPodAutoscaler); isWPA && c.gate {
		c.gate = false
		close(c.got)
		<-c.release
	}
	return err
}

// specSeriesPresent reports, without modifying the gauges, whether a spec low or high watermark series exists for
// the WPA and metric name.
func specSeriesPresent(wpa *v1alpha1.WatermarkPodAutoscaler, metric string) bool {
	want := getPrometheusLabels(wpa)
	want[metricNamePromLabel] = metric
	for _, vec := range []*prometheus.GaugeVec{specLowWatermark, specHighWatermark} {
		ch := make(chan prometheus.Metric, 1024)
		vec.Collect(ch)
		close(ch)
		for m := range ch {
			var pb dto.Metric
			if err := m.Write(&pb); err != nil {
				continue
			}
			got := prometheus.Labels{}
			for _, lp := range pb.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			if apiequality.Semantic.DeepEqual(got, want) {
				return true
			}
		}
	}
	return false
}

// A reconcile in flight with the old spec republishes the old metric name after updatePredicate cleaned it up.
// Opting out must still remove it, and finalize must leave nothing behind.
func TestTargetStretchSpecSeriesRenameInterleaving(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	metric := externalWiringMetric() // metric name "queue"
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{78000}}, timestamp: time.Now()}
	wpa := wiringWPA(&metric, nil)
	w := newStretchWiring(t, tc, wpa, stretchCfg(config.TargetStretchModeEnabledOptOut, 1200))
	t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })
	w.reconcile(t, wpa)

	gated := &gatedGetClient{Client: w.r.Client, gate: true, got: make(chan struct{}), release: make(chan struct{})}
	w.r.Client = gated
	done := make(chan error, 1)
	go func() {
		_, err := w.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: wpa.Namespace, Name: wpa.Name}})
		done <- err
	}()
	<-gated.got // the in-flight reconcile holds the old spec (metric "queue")

	oldObj := &v1alpha1.WatermarkPodAutoscaler{}
	require.NoError(t, gated.Client.Get(t.Context(), types.NamespacedName{Namespace: wpa.Namespace, Name: wpa.Name}, oldObj))
	newObj := oldObj.DeepCopy()
	newObj.Spec.Metrics[0].External.MetricName = "memory"
	newObj.Annotations[v1alpha1.TargetStretchEnabledAnnotationKey] = "false"
	require.NoError(t, gated.Update(t.Context(), newObj))
	require.True(t, updatePredicate(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}))

	close(gated.release)
	// The in-flight status update conflicts with the spec update; its metrics were published before that.
	<-done
	tc.metric.spec = newObj.Spec.Metrics[0] // the fake metrics client serves the renamed metric from now on
	require.True(t, specSeriesPresent(wpa, "queue"), "precondition: the in-flight reconcile republished the old name")
	w.r.Client = gated.Client
	_ = w.reconcile(t, wpa) // opted out with the new spec: removes every recorded series
	assert.False(t, specSeriesPresent(wpa, "queue"), "the old-name series must not survive the opt-out")
	assert.False(t, specSeriesPresent(wpa, "memory"))

	cleanupAssociatedMetrics(wpa, false)
	assert.False(t, specSeriesPresent(wpa, "queue"), "nothing left after finalize")
}

// An empty metric name (admitted by the CRD, rejected later by CheckWPAValidity) must not leave a metric_name=""
// series behind through opt-out, disabled mode and finalize.
func TestTargetStretchSpecSeriesEmptyMetricName(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	metric := v1alpha1.MetricSpec{Type: v1alpha1.ExternalMetricSourceType, External: &v1alpha1.ExternalMetricSource{
		MetricName:   "",
		LowWatermark: resource.NewQuantity(50, resource.DecimalSI), HighWatermark: resource.NewQuantity(70, resource.DecimalSI),
	}}
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{78000}}, timestamp: time.Now()}
	wpa := wiringWPA(&metric, nil)
	w := newStretchWiring(t, tc, wpa, stretchCfg(config.TargetStretchModeEnabledOptOut, 1200))
	t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })
	key := types.NamespacedName{Namespace: wpa.Namespace, Name: wpa.Name}
	setEnabled := func(v string) {
		cur := &v1alpha1.WatermarkPodAutoscaler{}
		require.NoError(t, w.r.Client.Get(t.Context(), key, cur))
		if cur.Annotations == nil {
			cur.Annotations = map[string]string{}
		}
		if v == "" {
			delete(cur.Annotations, v1alpha1.TargetStretchEnabledAnnotationKey)
		} else {
			cur.Annotations[v1alpha1.TargetStretchEnabledAnnotationKey] = v
		}
		require.NoError(t, w.r.Client.Update(t.Context(), cur))
	}

	setEnabled("")
	w.reconcile(t, wpa)
	require.True(t, specSeriesPresent(wpa, ""), "precondition: the empty metric name is published")

	setEnabled("false")
	w.reconcile(t, wpa)
	assert.False(t, specSeriesPresent(wpa, ""), "opt-out removes the empty-name series")

	setEnabled("")
	w.reconcile(t, wpa)
	require.True(t, specSeriesPresent(wpa, ""))
	w.r.Options.TargetStretch = &config.StaticTargetStretchConfigProvider{Config: config.DisabledTargetStretchConfig()}
	w.reconcile(t, wpa)
	assert.False(t, specSeriesPresent(wpa, ""), "disabled mode removes the empty-name series")

	w.r.Options.TargetStretch = &config.StaticTargetStretchConfigProvider{Config: stretchCfg(config.TargetStretchModeEnabledOptOut, 1200)}
	w.reconcile(t, wpa)
	require.True(t, specSeriesPresent(wpa, ""))
	cleanupAssociatedMetrics(wpa, false)
	assert.False(t, specSeriesPresent(wpa, ""), "finalize removes the empty-name series")
}

// The invalid-spec reconcile does not requeue, so an annotation-only opt-out must be enqueued by updatePredicate.
func TestTargetStretchAnnotationOnlyEditAfterInvalidSpec(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	metric := externalWiringMetric()
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{78000}}, timestamp: time.Now()}
	wpa := wiringWPA(&metric, nil)
	wpa.Spec.MaxReplicas = 0 // below minReplicas: invalid spec
	w := newStretchWiring(t, tc, wpa, stretchCfg(config.TargetStretchModeEnabledOptOut, 1200))
	t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })
	key := types.NamespacedName{Namespace: wpa.Namespace, Name: wpa.Name}

	res, err := w.r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res, "precondition: the invalid-spec reconcile does not requeue")
	oldObj := &v1alpha1.WatermarkPodAutoscaler{}
	require.NoError(t, w.r.Client.Get(t.Context(), key, oldObj))
	require.NotNil(t, oldObj.Status.TargetStretch)

	newObj := oldObj.DeepCopy()
	newObj.Annotations[v1alpha1.TargetStretchEnabledAnnotationKey] = "false"
	assert.True(t, updatePredicate(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}), "an annotation-only target stretch edit is enqueued")

	unrelated := oldObj.DeepCopy()
	unrelated.Annotations["example.com/other"] = "x"
	assert.False(t, updatePredicate(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: unrelated}), "unrelated annotation edits are still ignored")

	capChange := oldObj.DeepCopy()
	capChange.Annotations[v1alpha1.TargetStretchMaxRatioAnnotationKey] = "1.1"
	assert.True(t, updatePredicate(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: capChange}), "a max-ratio edit is enqueued")

	require.NoError(t, w.r.Client.Update(t.Context(), newObj))
	got := w.reconcile(t, wpa)
	assert.Nil(t, got.Status.TargetStretch, "the enqueued reconcile applies the opt-out")
	cond := stretchCondition(got)
	require.NotNil(t, cond)
	assert.Equal(t, v1alpha1.TargetStretchReasonOptedOut, cond.Reason)
}

// The quiet recommender path must not write the live convergence gauges, also for an unmarked WPA copy:
// normal recommendation, zero recommendation (tolerateZero=false) and a cold registry.
func TestRecommenderShadowLeavesConvergenceGaugesUntouched(t *testing.T) {
	scale := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	scale.Namespace = testNamespace
	tc := &replicaCalcTestCase{scale: scale, namespace: testNamespace}
	podClient := tc.prepareTestClientSet()
	informerFactory := informers.NewSharedInformerFactory(podClient, 0)
	informer := informerFactory.Core().V1().Pods()
	podLister := informer.Lister()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	informerFactory.Start(stop)
	require.True(t, cache.WaitForNamedCacheSync("HPA", stop, informer.Informer().HasSynced))
	reco := NewMockRecommenderClient()
	calc := NewReplicaCalculator(nil, reco, podLister, "test-cluster")

	wpa := wiringWPA(nil, recommenderWiringSpec())
	wpa.Spec.ConvergeTowardsWatermark = v1alpha1.ConvergeUpwards
	high := getPrometheusLabels(wpa)
	high[conditionPromLabel] = convergeConditionValues[v1alpha1.ConvergeUpwards]
	low := getPrometheusLabels(wpa)
	low[conditionPromLabel] = convergeConditionValues[v1alpha1.ConvergeDownwards]
	t.Cleanup(func() {
		conditions.Delete(high)
		conditions.Delete(low)
	})

	responses := map[string]ReplicaRecommendationResponse{
		"normal": {Replicas: 12, ReplicasLowerBound: 10, ReplicasUpperBound: 20, HasBounds: true, Timestamp: time.Now()},
		"zero":   {Replicas: 0, ReplicasLowerBound: 0, ReplicasUpperBound: 0, Timestamp: time.Now()},
	}
	for _, name := range []string{"normal", "zero"} {
		t.Run(name, func(t *testing.T) {
			conditions.With(high).Set(1)
			conditions.With(low).Set(0)
			reco.ReturnedResponse = responses[name]
			_, err := calc.GetRecommenderShadowReplicas(t.Context(), logf.Log, scale, wpa.DeepCopy(), effectiveRecommender(wpa.Spec.Recommender, 1200))
			require.NoError(t, err)
			assert.InDelta(t, 1, testutil.ToFloat64(conditions.With(high)), 0, "live converge_to_high_watermark gauge unchanged")
			assert.InDelta(t, 0, testutil.ToFloat64(conditions.With(low)), 0, "live converge_to_low_watermark gauge unchanged")
		})
	}
	t.Run("cold registry", func(t *testing.T) {
		conditions.Delete(high)
		conditions.Delete(low)
		reco.ReturnedResponse = responses["normal"]
		_, err := calc.GetRecommenderShadowReplicas(t.Context(), logf.Log, scale, wpa.DeepCopy(), effectiveRecommender(wpa.Spec.Recommender, 1200))
		require.NoError(t, err)
		assert.False(t, conditions.Delete(high), "the shadow must not create the convergence series")
		assert.False(t, conditions.Delete(low))
	})
}

// configValidGauge reads wpa_controller_config_valid from the controller-runtime registry.
func configValidGauge(t *testing.T) float64 {
	t.Helper()
	families, err := sigmetrics.Registry.Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() == "wpa_controller_config_valid" {
			return mf.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatal("wpa_controller_config_valid not registered")
	return 0
}

// The target stretch section of the controller configuration file is reloaded without a restart: the gauges, the
// enqueue of every WPA, the decision, the status and the counts follow; an invalid reload keeps the last valid one.
func TestTargetStretchControllerConfigReload(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	metric := externalWiringMetric()
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{78000}}, timestamp: time.Now()}
	wpa := wiringWPA(&metric, nil)
	t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })

	path := filepath.Join(t.TempDir(), "config.yaml")
	write := func(data string) { require.NoError(t, os.WriteFile(path, []byte(data), 0o600)) }
	write("version: v1\ntargetStretch:\n  mode: dry-run-opt-in\n  ratio: 1.2\n")
	fileProvider := config.NewFileProvider(path, logf.Log)
	stretch := &config.ControllerTargetStretchProvider{Config: fileProvider, Env: stretchCfg(config.TargetStretchModeEnabledOptOut, 1100)}

	w := newStretchWiring(t, tc, wpa, config.DisabledTargetStretchConfig())
	w.r.Options.TargetStretch = stretch
	w.r.stretchEvents = make(chan event.GenericEvent, 10)
	w.r.stretchNotify = make(chan struct{}, 1)
	dispatchCtx, stopDispatch := context.WithCancel(t.Context())
	t.Cleanup(stopDispatch)
	go func() { _ = w.r.runStretchDispatcher(dispatchCtx) }()
	fileProvider.OnChange(func(previous, current *config.ControllerConfig) {
		if !reflect.DeepEqual(previous.TargetStretch, current.TargetStretch) {
			w.r.OnTargetStretchConfigChange(t.Context())
		}
	})
	RecordTargetStretchConfigMetrics(stretch.TargetStretchConfig())
	modeGauge := func(m config.TargetStretchMode) float64 {
		return testutil.ToFloat64(targetStretchMode.With(prometheus.Labels{modePromLabel: string(m)}))
	}

	got := w.reconcile(t, wpa)
	require.NotNil(t, got.Status.TargetStretch)
	assert.Equal(t, "dry-run-opt-in", got.Status.TargetStretch.Mode, "the file wins over the env config")
	assert.Equal(t, int32(12), got.Status.DesiredReplicas)
	assert.InDelta(t, 1, modeGauge(config.TargetStretchModeDryRunOptIn), 0)

	write("version: v1\ntargetStretch:\n  mode: enabled-opt-in\n  ratio: 1.5\n")
	fileProvider.Reload()
	assert.InDelta(t, 1, modeGauge(config.TargetStretchModeEnabledOptIn), 0, "the mode gauge follows the reload")
	assert.InDelta(t, 0, modeGauge(config.TargetStretchModeDryRunOptIn), 0)
	assert.InDelta(t, 1.5, testutil.ToFloat64(targetStretchRatio), 0, "the ratio gauge follows the reload")
	select {
	case ev := <-w.r.stretchEvents:
		assert.Equal(t, wpa.Name, ev.Object.GetName(), "every WPA is enqueued on a targetStretch change")
	case <-time.After(5 * time.Second):
		t.Fatal("the WPA was not enqueued after the targetStretch change")
	}

	got = w.reconcile(t, wpa)
	require.NotNil(t, got.Status.TargetStretch)
	assert.Equal(t, "enabled-opt-in", got.Status.TargetStretch.Mode, "the decision follows the reload without a restart")
	assert.Equal(t, "1.5", got.Status.TargetStretch.AppliedRatio)
	assert.Equal(t, int32(10), got.Status.DesiredReplicas, "scaling uses the reloaded stretched watermarks")
	assert.InDelta(t, 1, testutil.ToFloat64(targetStretchWPAs.With(prometheus.Labels{statePromLabel: "stretched"})), 0)

	write("version: v1\ntargetStretch:\n  mode: enabled-opt-in\n  ratio: 3\n")
	fileProvider.Reload()
	assert.InDelta(t, 0, configValidGauge(t), 0, "an invalid reload sets config_valid to 0")
	got = w.reconcile(t, wpa)
	assert.Equal(t, "1.5", got.Status.TargetStretch.AppliedRatio, "an invalid reload keeps the last valid config")

	write("version: v1\n")
	fileProvider.Reload()
	assert.InDelta(t, 1, configValidGauge(t), 0)
	assert.InDelta(t, 1, modeGauge(config.TargetStretchModeEnabledOptOut), 0, "without the section, the env config applies")
	got = w.reconcile(t, wpa)
	assert.Equal(t, "enabled-opt-out", got.Status.TargetStretch.Mode)
	assert.Equal(t, "1.1", got.Status.TargetStretch.AppliedRatio)
}

// Without a consumer (a standby replica), configuration changes must not pile up goroutines or block; the
// latest configuration is applied once a consumer appears.
func TestTargetStretchChangeDispatchBoundedWithoutConsumer(t *testing.T) {
	tenPods := makeScale(testDeploymentName, 10, map[string]string{"name": podNamePrefix})
	tenPods.Namespace = testNamespace
	metric := externalWiringMetric()
	tc := &replicaCalcTestCase{scale: tenPods, metric: &metricInfo{spec: metric, levels: []int64{78000}}, timestamp: time.Now()}
	wpa := wiringWPA(&metric, nil)
	t.Cleanup(func() { cleanupAssociatedMetrics(wpa, false) })
	w := newStretchWiring(t, tc, wpa, config.DisabledTargetStretchConfig())
	w.r.stretchEvents = make(chan event.GenericEvent) // unbuffered: nothing is consumed until the test reads
	w.r.stretchNotify = make(chan struct{}, 1)

	ratio := 1.2
	current := &config.ControllerConfig{Version: "v1"}
	w.r.Options.TargetStretch = &config.ControllerTargetStretchProvider{Config: providerFunc(func() *config.ControllerConfig { return current })}

	before := goruntime.NumGoroutine()
	modes := []string{"dry-run-opt-in", "enabled-opt-in", "dry-run-opt-out", "enabled-opt-out"}
	for i := range 200 {
		current = &config.ControllerConfig{Version: "v1", TargetStretch: &config.TargetStretchSettings{Mode: modes[i%len(modes)], Ratio: &ratio}}
		w.r.OnTargetStretchConfigChange(t.Context()) // must return immediately
	}
	assert.LessOrEqual(t, goruntime.NumGoroutine(), before+2, "no goroutine per change without a consumer")
	assert.Len(t, w.r.stretchNotify, 1, "changes are coalesced into one pending signal")
	assert.InDelta(t, 1, testutil.ToFloat64(targetStretchMode.With(prometheus.Labels{modePromLabel: "enabled-opt-out"})), 0, "the gauges show the latest config")

	// The consumer appears (this replica becomes the leader): one dispatch enqueues the WPA, and the reconcile
	// reads the latest configuration.
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = w.r.runStretchDispatcher(ctx) }()
	select {
	case ev := <-w.r.stretchEvents:
		assert.Equal(t, wpa.Name, ev.Object.GetName())
	case <-time.After(5 * time.Second):
		t.Fatal("the pending change was not dispatched once a consumer appeared")
	}
	got := w.reconcile(t, wpa)
	require.NotNil(t, got.Status.TargetStretch)
	assert.Equal(t, "enabled-opt-out", got.Status.TargetStretch.Mode, "the latest config is applied")
	select {
	case <-w.r.stretchEvents:
		t.Fatal("coalesced changes must dispatch once")
	case <-time.After(200 * time.Millisecond):
	}
}

// providerFunc adapts a function to config.Provider.
type providerFunc func() *config.ControllerConfig

func (f providerFunc) Get() *config.ControllerConfig { return f() }
