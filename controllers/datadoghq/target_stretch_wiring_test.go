// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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
	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &v1alpha1.WatermarkPodAutoscaler{})
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
				assert.Equal(t, convergeToWatermarkUnstable, converge.Message)
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
