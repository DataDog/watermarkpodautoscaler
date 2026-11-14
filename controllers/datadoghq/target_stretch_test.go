// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1/test"
	"github.com/DataDog/watermarkpodautoscaler/pkg/config"
)

func stretchCfg(mode config.TargetStretchMode, ratio int64) config.TargetStretchConfig {
	return config.TargetStretchConfig{Mode: mode, RatioMilli: ratio, Valid: true}
}

func newStretchWPA(name string, annotations map[string]string) *v1alpha1.WatermarkPodAutoscaler {
	wpa := test.NewWatermarkPodAutoscaler(testingNamespace, name, &test.NewWatermarkPodAutoscalerOptions{
		Annotations: annotations,
		Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
			Metrics: []v1alpha1.MetricSpec{{
				Type: v1alpha1.ExternalMetricSourceType,
				External: &v1alpha1.ExternalMetricSource{
					MetricName:     "cpu",
					MetricSelector: &metav1.LabelSelector{},
					LowWatermark:   resource.NewQuantity(50, resource.DecimalSI),
					HighWatermark:  resource.NewQuantity(70, resource.DecimalSI),
				},
			}},
			MinReplicas: getReplicas(1),
			MaxReplicas: 100,
		},
	})
	return v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
}

func TestEvaluateTargetStretch(t *testing.T) {
	const en, mr = v1alpha1.TargetStretchEnabledAnnotationKey, v1alpha1.TargetStretchMaxRatioAnnotationKey
	type want struct {
		selected, enforce, dryRun, capped, optedOut bool
		ratio                                       int64
		status                                      corev1.ConditionStatus
		reason                                      string
	}
	tests := []struct {
		name string
		mode config.TargetStretchMode
		ann  map[string]string
		want want
	}{
		{"disabled unannotated", config.TargetStretchModeDisabled, nil, want{ratio: 1000, status: "False", reason: "Disabled"}},
		{"disabled opted in", config.TargetStretchModeDisabled, map[string]string{en: "true"}, want{ratio: 1000, status: "False", reason: "Disabled"}},
		{"opt-in unannotated", config.TargetStretchModeEnabledOptIn, nil, want{ratio: 1000, status: "False", reason: "NotOptedIn"}},
		{"opt-in only cap", config.TargetStretchModeEnabledOptIn, map[string]string{mr: "1.15"}, want{ratio: 1000, status: "False", reason: "NotOptedIn"}},
		{"opt-in true", config.TargetStretchModeEnabledOptIn, map[string]string{en: "true"}, want{selected: true, enforce: true, ratio: 1200, status: "True", reason: "Enforced"}},
		{"opt-in true capped", config.TargetStretchModeEnabledOptIn, map[string]string{en: "true", mr: "1.15"}, want{selected: true, enforce: true, capped: true, ratio: 1150, status: "True", reason: "CappedByWPA"}},
		{"opt-in cap above ratio", config.TargetStretchModeEnabledOptIn, map[string]string{en: "true", mr: "1.5"}, want{selected: true, enforce: true, ratio: 1200, status: "True", reason: "Enforced"}},
		{"opt-in capped to 1", config.TargetStretchModeEnabledOptIn, map[string]string{en: "true", mr: "1"}, want{selected: true, enforce: true, capped: true, ratio: 1000, status: "False", reason: "CappedByWPA"}},
		{"opt-in false", config.TargetStretchModeEnabledOptIn, map[string]string{en: "false"}, want{optedOut: true, ratio: 1000, status: "False", reason: "OptedOut"}},
		{"opt-out unannotated", config.TargetStretchModeEnabledOptOut, nil, want{selected: true, enforce: true, ratio: 1200, status: "True", reason: "Enforced"}},
		{"opt-out false", config.TargetStretchModeEnabledOptOut, map[string]string{en: "false"}, want{optedOut: true, ratio: 1000, status: "False", reason: "OptedOut"}},
		{"opt-out typo is never stretched", config.TargetStretchModeEnabledOptOut, map[string]string{en: "flase"}, want{optedOut: true, ratio: 1000, status: "False", reason: "InvalidAnnotation"}},
		{"opt-out invalid cap", config.TargetStretchModeEnabledOptOut, map[string]string{mr: "3"}, want{optedOut: true, ratio: 1000, status: "False", reason: "InvalidAnnotation"}},
		{"cap below 1 before rounding", config.TargetStretchModeEnabledOptOut, map[string]string{mr: "0.9999"}, want{optedOut: true, ratio: 1000, status: "False", reason: "InvalidAnnotation"}},
		{"cap above 2 before rounding", config.TargetStretchModeEnabledOptOut, map[string]string{mr: "2.0004"}, want{optedOut: true, ratio: 1000, status: "False", reason: "InvalidAnnotation"}},
		{"dry-run opt-in true", config.TargetStretchModeDryRunOptIn, map[string]string{en: "true", mr: "1.15"}, want{selected: true, dryRun: true, capped: true, ratio: 1150, status: "False", reason: "DryRun"}},
		{"dry-run opt-in unannotated", config.TargetStretchModeDryRunOptIn, nil, want{ratio: 1000, status: "False", reason: "NotOptedIn"}},
		{"dry-run opt-out unannotated", config.TargetStretchModeDryRunOptOut, nil, want{selected: true, dryRun: true, ratio: 1200, status: "False", reason: "DryRun"}},
		{"dry-run opt-out false", config.TargetStretchModeDryRunOptOut, map[string]string{en: "false"}, want{optedOut: true, ratio: 1000, status: "False", reason: "OptedOut"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := evaluateTargetStretch(stretchCfg(tt.mode, 1200), newStretchWPA("w", tt.ann))
			got := want{d.selected, d.enforce, d.dryRun, d.capped, d.optedOut, d.appliedRatioMilli, d.condStatus, d.reason}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			if d.annotated != (len(tt.ann) > 0) {
				t.Fatalf("annotated=%v", d.annotated)
			}
		})
	}

	invalid := config.LoadTargetStretchConfigFromEnv(func(k string) (string, bool) { return map[string]string{"WPA_STRETCH_MODE": "bogus"}[k], true })
	d := evaluateTargetStretch(invalid, newStretchWPA("w", map[string]string{en: "true"}))
	if d.selected || d.reason != v1alpha1.TargetStretchReasonInvalidConfig {
		t.Fatalf("invalid config: got %+v", d)
	}
}

func TestStretchQuantityLargeValues(t *testing.T) {
	tests := []struct {
		in    string
		ratio int64
		want  string
	}{
		{"50", 1150, "57500m"},
		{"70", 1150, "80500m"},
		{"5T", 2000, "10T"},                            // product 5e15*2000 overflows int64, result fits
		{"1", 1001, "1001m"},                           // half-up rounding at milli
		{"333m", 1500, "500m"},                         // 499.5 rounds to 500
		{"9223372036854775", 1000, "9223372036854775"}, // close to the int64 milli limit
	}
	for _, tt := range tests {
		in := resource.MustParse(tt.in)
		orig := in.DeepCopy()
		got := stretchQuantity(&in, tt.ratio)
		want := resource.MustParse(tt.want)
		if got.Cmp(want) != 0 {
			t.Errorf("stretchQuantity(%s, %d) = %s, want %s", tt.in, tt.ratio, got.String(), tt.want)
		}
		if in.Cmp(orig) != 0 || in.String() != orig.String() {
			t.Errorf("stretchQuantity mutated its input: %s -> %s", orig.String(), in.String())
		}
	}
}

func TestStretchQuantitySaturates(t *testing.T) {
	prev := int64(0)
	for _, in := range []string{"1T", "5T", "4000T", "9000T", "1E", "100E"} {
		q := resource.MustParse(in)
		got := stretchQuantity(&q, 2000).MilliValue()
		if got <= 0 {
			t.Fatalf("stretchQuantity(%s, 2) milli = %d, want positive", in, got)
		}
		if got < prev {
			t.Fatalf("stretchQuantity is not monotonic: %s gives %d < %d", in, got, prev)
		}
		prev = got
	}
	if prev != math.MaxInt64 {
		t.Fatalf("huge values must saturate at MaxInt64 milli, got %d", prev)
	}
}

func TestStretchWatermarks(t *testing.T) {
	wpa := newStretchWPA("w", nil)
	m := effectiveMetrics(wpa.Spec.Metrics, 1150)
	if lw, hw := m[0].External.LowWatermark.MilliValue(), m[0].External.HighWatermark.MilliValue(); lw != 57500 || hw != 80500 {
		t.Fatalf("got %d/%d, want 57500/80500", lw, hw)
	}
	if wpa.Spec.Metrics[0].External.HighWatermark.MilliValue() != 70000 {
		t.Fatalf("spec was mutated")
	}
	reco := effectiveRecommender(&v1alpha1.RecommenderSpec{LowWatermark: resource.NewQuantity(50, resource.DecimalSI), HighWatermark: resource.NewQuantity(70, resource.DecimalSI)}, 1200)
	req, err := buildWorkloadRecommendationRequest(&ReplicaRecommendationRequest{Recommender: reco, TargetRef: &v1alpha1.CrossVersionObjectReference{}})
	if err != nil {
		t.Fatal(err)
	}
	if s := req.String(); !strings.Contains(s, "60") || !strings.Contains(s, "84") {
		t.Fatalf("recommender bounds not stretched: %s", s)
	}
}

// usageCalculator runs the real watermark math (getReplicaCount) on the watermarks it receives, with a fixed usage.
func usageCalculator(usage float64, ready int32) *fakeReplicaCalculator {
	return &fakeReplicaCalculator{replicasFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (ReplicaCalculation, error) {
		replicas, util, pos := getReplicaCount(logf.Log, ready, ready, wpa, metric.External.MetricName, usage, metric.External.LowWatermark, metric.External.HighWatermark)
		return ReplicaCalculation{replicas, util, metav1.Now().Time, ready, pos, "", usage, true}, nil
	}}
}

func TestTargetStretchComputeReplicas(t *testing.T) {
	scale := &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 10}, Status: autoscalingv1.ScaleStatus{Replicas: 10}}
	// Usage 78 with the default 10% tolerance: above the spec high watermark 70 (77), within the stretched 80.5 (88.55).
	r := &WatermarkPodAutoscalerReconciler{eventRecorder: events.NewFakeRecorder(10), replicaCalc: usageCalculator(78000, 10)}
	tests := []struct {
		name         string
		mode         config.TargetStretchMode
		ann          map[string]string
		wantReplicas int32
		wantShadow   *int32
	}{
		{"disabled scales on spec", config.TargetStretchModeDisabled, map[string]string{v1alpha1.TargetStretchEnabledAnnotationKey: "true"}, 12, nil},
		{"enabled-opt-in stays stable on 57.5/80.5", config.TargetStretchModeEnabledOptIn, map[string]string{v1alpha1.TargetStretchEnabledAnnotationKey: "true", v1alpha1.TargetStretchMaxRatioAnnotationKey: "1.15"}, 10, nil},
		{"enabled-opt-out unannotated is stretched", config.TargetStretchModeEnabledOptOut, nil, 10, nil},
		{"enabled-opt-out false is not stretched", config.TargetStretchModeEnabledOptOut, map[string]string{v1alpha1.TargetStretchEnabledAnnotationKey: "false"}, 12, nil},
		{"dry-run-opt-in scales on spec with shadow", config.TargetStretchModeDryRunOptIn, map[string]string{v1alpha1.TargetStretchEnabledAnnotationKey: "true"}, 12, getReplicas(10)},
		{"dry-run-opt-out scales on spec with shadow", config.TargetStretchModeDryRunOptOut, nil, 12, getReplicas(10)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wpa := newStretchWPA("w", tt.ann)
			d := evaluateTargetStretch(stretchCfg(tt.mode, 1200), wpa)
			applyTargetStretchStatus(wpa, d)
			replicas, _, _, _, _, _, calcs, err := r.computeReplicas(t.Context(), logf.Log, wpa, scale, d)
			if err != nil {
				t.Fatal(err)
			}
			if replicas != tt.wantReplicas {
				t.Fatalf("replicas=%d want %d", replicas, tt.wantReplicas)
			}
			var shadow *int32
			if d.dryRun {
				shadow = r.computeShadowReplicas(t.Context(), logf.Log, wpa, scale, d, calcs)
			}
			if (shadow == nil) != (tt.wantShadow == nil) || (shadow != nil && *shadow != *tt.wantShadow) {
				t.Fatalf("shadow=%v want %v", shadow, tt.wantShadow)
			}
		})
	}
}

func TestApplyTargetStretchStatusUnannotatedUnchanged(t *testing.T) {
	for _, mode := range []config.TargetStretchMode{config.TargetStretchModeDisabled, config.TargetStretchModeEnabledOptIn, config.TargetStretchModeDryRunOptIn} {
		wpa := newStretchWPA("w", nil)
		before := wpa.Status.DeepCopy()
		applyTargetStretchStatus(wpa, evaluateTargetStretch(stretchCfg(mode, 1200), wpa))
		if wpa.Status.TargetStretch != nil || len(wpa.Status.Conditions) != len(before.Conditions) {
			t.Fatalf("mode %s changed the status of an unannotated WPA: %+v", mode, wpa.Status)
		}
	}
	wpa := newStretchWPA("w", map[string]string{v1alpha1.TargetStretchEnabledAnnotationKey: "true", v1alpha1.TargetStretchMaxRatioAnnotationKey: "1.15"})
	applyTargetStretchStatus(wpa, evaluateTargetStretch(stretchCfg(config.TargetStretchModeEnabledOptIn, 1200), wpa))
	ts := wpa.Status.TargetStretch
	if ts == nil || ts.AppliedRatio != "1.15" || len(ts.EffectiveWatermarks) != 1 || ts.EffectiveWatermarks[0].HighWatermark.MilliValue() != 80500 {
		t.Fatalf("unexpected status block: %+v", ts)
	}
}

func TestTargetStretchEvents(t *testing.T) {
	rec := events.NewFakeRecorder(10)
	r := &WatermarkPodAutoscalerReconciler{eventRecorder: rec}
	run := func(mode config.TargetStretchMode, prev *v1alpha1.WatermarkPodAutoscalerStatus) *v1alpha1.WatermarkPodAutoscalerStatus {
		wpa := newStretchWPA("w", map[string]string{v1alpha1.TargetStretchEnabledAnnotationKey: "true"})
		applyTargetStretchStatus(wpa, evaluateTargetStretch(stretchCfg(mode, 1200), wpa))
		r.emitTargetStretchEvents(wpa, prev)
		return wpa.Status.DeepCopy()
	}
	drain := func() []string {
		var out []string
		for len(rec.Events) > 0 {
			out = append(out, <-rec.Events)
		}
		return out
	}
	s := run(config.TargetStretchModeDryRunOptIn, nil)
	if ev := drain(); len(ev) != 0 {
		t.Fatalf("dry-run emitted events: %v", ev)
	}
	s = run(config.TargetStretchModeEnabledOptIn, s)
	if ev := drain(); len(ev) != 1 || !strings.Contains(ev[0], v1alpha1.ReasonTargetStretchApplied) {
		t.Fatalf("expected one applied event, got %v", ev)
	}
	s = run(config.TargetStretchModeEnabledOptIn, s)
	if ev := drain(); len(ev) != 0 {
		t.Fatalf("steady state emitted events: %v", ev)
	}
	run(config.TargetStretchModeDisabled, s)
	if ev := drain(); len(ev) != 1 || !strings.Contains(ev[0], v1alpha1.ReasonTargetStretchRemoved) {
		t.Fatalf("expected one removed event, got %v", ev)
	}
}

func TestTargetStretchMetrics(t *testing.T) {
	tracker := &targetStretchTracker{wpas: map[types.NamespacedName]targetStretchFlags{}}
	cfg := stretchCfg(config.TargetStretchModeEnabledOptOut, 1200)
	for name, ann := range map[string]map[string]string{
		"a": nil,
		"b": {v1alpha1.TargetStretchMaxRatioAnnotationKey: "1.1"},
		"c": {v1alpha1.TargetStretchEnabledAnnotationKey: "false"},
	} {
		tracker.update(types.NamespacedName{Namespace: testingNamespace, Name: name}, evaluateTargetStretch(cfg, newStretchWPA(name, ann)))
	}
	get := func(state string) float64 {
		return testutil.ToFloat64(targetStretchWPAs.With(prometheus.Labels{statePromLabel: state}))
	}
	if get("selected") != 2 || get("stretched") != 2 || get("capped") != 1 || get("opted_out") != 1 {
		t.Fatalf("unexpected counts: %v %v %v %v", get("selected"), get("stretched"), get("capped"), get("opted_out"))
	}
	tracker.remove(types.NamespacedName{Namespace: testingNamespace, Name: "a"})
	if get("selected") != 1 {
		t.Fatalf("remove did not update the counts")
	}

	RecordTargetStretchConfigMetrics(config.LoadTargetStretchConfigFromEnv(func(k string) (string, bool) {
		return map[string]string{"WPA_STRETCH_MODE": "enabled-opt-in", "WPA_STRETCH_RATIO": "9"}[k], true
	}))
	if testutil.ToFloat64(targetStretchConfigValid) != 0 || testutil.ToFloat64(targetStretchMode.With(prometheus.Labels{modePromLabel: "disabled"})) != 1 || testutil.ToFloat64(targetStretchRatio) != 1 {
		t.Fatalf("invalid config gauges not set as expected")
	}

	wpa := newStretchWPA("m", nil)
	d := evaluateTargetStretch(cfg, wpa)
	updateTargetStretchWPAMetrics(wpa, d)
	l := getPrometheusLabels(wpa)
	if testutil.ToFloat64(targetStretchAppliedRatio.With(l)) != 1.2 {
		t.Fatalf("applied ratio gauge not set")
	}
	updateTargetStretchWPAMetrics(wpa, evaluateTargetStretch(stretchCfg(config.TargetStretchModeDisabled, 1000), wpa))
	if testutil.CollectAndCount(specHighWatermark) != 0 || testutil.CollectAndCount(targetStretchAppliedRatio) != 0 {
		t.Fatalf("per-WPA gauges not cleaned up when unselected")
	}
}

// recount is the reference: totals computed from scratch over the tracked flags.
func recount(t *targetStretchTracker) targetStretchTotals {
	var want targetStretchTotals
	for _, f := range t.wpas {
		want.apply(f, 1)
	}
	return want
}

func TestTargetStretchTrackerIncrementalCounts(t *testing.T) {
	tracker := &targetStretchTracker{wpas: map[types.NamespacedName]targetStretchFlags{}}
	key := types.NamespacedName{Namespace: testingNamespace, Name: "w"}
	enforced := evaluateTargetStretch(stretchCfg(config.TargetStretchModeEnabledOptOut, 1200), newStretchWPA("w", map[string]string{v1alpha1.TargetStretchMaxRatioAnnotationKey: "1.1"}))
	optedOut := evaluateTargetStretch(stretchCfg(config.TargetStretchModeEnabledOptOut, 1200), newStretchWPA("w", map[string]string{v1alpha1.TargetStretchEnabledAnnotationKey: "false"}))

	tracker.update(key, enforced)
	tracker.update(key, enforced) // unchanged: no double count
	if got := tracker.totals; got != (targetStretchTotals{selected: 1, stretched: 1, capped: 1}) {
		t.Fatalf("after repeated update: %+v", got)
	}
	tracker.update(key, optedOut) // transition replaces the previous flags
	if got := tracker.totals; got != (targetStretchTotals{optedOut: 1}) {
		t.Fatalf("after transition: %+v", got)
	}
	tracker.remove(key)
	tracker.remove(key) // unknown key: no underflow
	if got := tracker.totals; got != (targetStretchTotals{}) {
		t.Fatalf("after remove: %+v", got)
	}
}

func TestTargetStretchTrackerConcurrentUpdates(t *testing.T) {
	tracker := &targetStretchTracker{wpas: map[types.NamespacedName]targetStretchFlags{}}
	modes := []config.TargetStretchMode{config.TargetStretchModeEnabledOptOut, config.TargetStretchModeDryRunOptIn, config.TargetStretchModeDisabled}
	anns := []map[string]string{nil, {v1alpha1.TargetStretchEnabledAnnotationKey: "true"}, {v1alpha1.TargetStretchEnabledAnnotationKey: "false"}, {v1alpha1.TargetStretchMaxRatioAnnotationKey: "1.1"}}
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 500 {
				key := types.NamespacedName{Namespace: testingNamespace, Name: fmt.Sprintf("w%d", (g*7+i)%40)}
				if i%11 == 0 {
					tracker.remove(key)
					continue
				}
				d := evaluateTargetStretch(stretchCfg(modes[(g+i)%len(modes)], 1200), newStretchWPA(key.Name, anns[(g*3+i)%len(anns)]))
				tracker.update(key, d)
			}
		}(g)
	}
	wg.Wait()
	if want := recount(tracker); tracker.totals != want {
		t.Fatalf("incremental totals %+v differ from a recount %+v", tracker.totals, want)
	}
	get := func(state string) float64 {
		return testutil.ToFloat64(targetStretchWPAs.With(prometheus.Labels{statePromLabel: state}))
	}
	if get("selected") != float64(tracker.totals.selected) || get("opted_out") != float64(tracker.totals.optedOut) {
		t.Fatalf("gauges do not match the totals")
	}
}

// Unselecting a WPA deletes exactly its own spec watermark series, not every series that shares its labels.
func TestCleanupTargetStretchMetricsExactLabels(t *testing.T) {
	wpa := newStretchWPA("exact", nil)
	other := newStretchWPA("exact-other", nil)
	labelsFor := func(w *v1alpha1.WatermarkPodAutoscaler, metric string) prometheus.Labels {
		l := getPrometheusLabels(w)
		l[metricNamePromLabel] = metric
		return l
	}
	foreign := labelsFor(wpa, "not-in-spec") // same WPA labels, a metric name the WPA spec does not use
	t.Cleanup(func() {
		specLowWatermark.Delete(foreign)
		cleanupTargetStretchMetrics(other)
	})

	cfg := stretchCfg(config.TargetStretchModeEnabledOptOut, 1200)
	updateTargetStretchWPAMetrics(wpa, evaluateTargetStretch(cfg, wpa))
	updateTargetStretchWPAMetrics(other, evaluateTargetStretch(cfg, other))
	specLowWatermark.With(foreign).Set(1)

	updateTargetStretchWPAMetrics(wpa, evaluateTargetStretch(stretchCfg(config.TargetStretchModeDisabled, 1000), wpa))

	assert.False(t, specLowWatermark.Delete(labelsFor(wpa, "cpu")), "the WPA spec low watermark series is deleted")
	assert.False(t, specHighWatermark.Delete(labelsFor(wpa, "cpu")), "the WPA spec high watermark series is deleted")
	assert.True(t, specLowWatermark.Delete(foreign), "a series outside the WPA metric names is not touched")
	assert.True(t, specHighWatermark.Delete(labelsFor(other, "cpu")), "another WPA is not touched")
	specHighWatermark.With(labelsFor(other, "cpu")).Set(70000)
}

// When a selected WPA renames a metric, the spec-change cleanup (cleanupAssociatedMetrics on the old object, as
// updatePredicate does) removes the old series, and the next reconcile only exports the new name.
func TestCleanupTargetStretchMetricsOnMetricRename(t *testing.T) {
	cfg := stretchCfg(config.TargetStretchModeEnabledOptOut, 1200)
	oldWPA := newStretchWPA("rename", nil)
	newWPA := oldWPA.DeepCopy()
	newWPA.Spec.Metrics[0].External.MetricName = "memory"
	labelsFor := func(metric string) prometheus.Labels {
		l := getPrometheusLabels(oldWPA)
		l[metricNamePromLabel] = metric
		return l
	}
	t.Cleanup(func() { cleanupTargetStretchMetrics(newWPA) })

	updateTargetStretchWPAMetrics(oldWPA, evaluateTargetStretch(cfg, oldWPA))
	cleanupAssociatedMetrics(oldWPA, true)
	updateTargetStretchWPAMetrics(newWPA, evaluateTargetStretch(cfg, newWPA))

	assert.False(t, specLowWatermark.Delete(labelsFor("cpu")), "the series of the old metric name is removed")
	assert.False(t, specHighWatermark.Delete(labelsFor("cpu")))
	assert.True(t, specLowWatermark.Delete(labelsFor("memory")), "the new metric name is exported")
	assert.True(t, specHighWatermark.Delete(labelsFor("memory")))
}
