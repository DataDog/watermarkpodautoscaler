// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/pkg/config"
	"github.com/DataDog/watermarkpodautoscaler/pkg/math32"
)

// targetStretchDecision is the per-reconcile outcome of the cluster-wide target stretch evaluation.
type targetStretchDecision struct {
	mode config.TargetStretchMode
	// selected is true when the mode is active and the WPA is in scope.
	selected bool
	// enforce is true when the stretched watermarks are used for scaling.
	enforce bool
	// dryRun is true when only a shadow recommendation is computed.
	dryRun bool
	// appliedRatioMilli is min(cluster ratio, WPA cap), in milli.
	appliedRatioMilli int64
	capped            bool
	optedOut          bool
	invalidAnnotation bool
	// annotated is true when the WPA carries any target-stretch.* annotation.
	annotated bool

	condStatus corev1.ConditionStatus
	reason     string
	message    string
}

// stretching returns true when the watermarks used for scaling differ from the spec.
func (d targetStretchDecision) stretching() bool {
	return d.enforce && d.appliedRatioMilli > config.TargetStretchRatioMinMilli
}

func (r *WatermarkPodAutoscalerReconciler) targetStretchConfig() config.TargetStretchConfig {
	if r.Options.TargetStretch == nil {
		return config.DisabledTargetStretchConfig()
	}
	return r.Options.TargetStretch.TargetStretchConfig()
}

// evaluateTargetStretch decides, from the cluster config and the WPA annotations, whether and how the WPA is stretched.
func evaluateTargetStretch(cfg config.TargetStretchConfig, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) targetStretchDecision {
	d := targetStretchDecision{
		mode:              cfg.Mode,
		appliedRatioMilli: config.TargetStretchRatioMinMilli,
		condStatus:        corev1.ConditionFalse,
	}
	for k := range wpa.Annotations {
		if strings.HasPrefix(k, datadoghqv1alpha1.TargetStretchAnnotationPrefix) {
			d.annotated = true
			break
		}
	}

	var enabled *bool
	var invalidMsgs []string
	if raw, ok := wpa.Annotations[datadoghqv1alpha1.TargetStretchEnabledAnnotationKey]; ok {
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			invalidMsgs = append(invalidMsgs, fmt.Sprintf("%s=%q is not a boolean", datadoghqv1alpha1.TargetStretchEnabledAnnotationKey, raw))
		} else {
			enabled = &b
		}
	}
	capMilli := cfg.RatioMilli
	if raw, ok := wpa.Annotations[datadoghqv1alpha1.TargetStretchMaxRatioAnnotationKey]; ok {
		m, err := config.ParseTargetStretchRatioMilli(raw)
		if err != nil {
			invalidMsgs = append(invalidMsgs, fmt.Sprintf("%s: %v", datadoghqv1alpha1.TargetStretchMaxRatioAnnotationKey, err))
		} else {
			capMilli = m
		}
	}

	if !cfg.Mode.IsActive() {
		if !cfg.Valid {
			d.reason = datadoghqv1alpha1.TargetStretchReasonInvalidConfig
			d.message = fmt.Sprintf("the cluster-wide target stretch configuration is invalid, the feature is disabled: %v", cfg.Err)
		} else {
			d.reason = datadoghqv1alpha1.TargetStretchReasonDisabled
			d.message = "the cluster-wide target stretch is disabled"
		}
		return d
	}

	switch {
	case len(invalidMsgs) > 0:
		d.invalidAnnotation = true
		d.optedOut = true
		d.reason = datadoghqv1alpha1.TargetStretchReasonInvalidAnnotation
		d.message = "the WPA is not stretched because of an invalid annotation: " + strings.Join(invalidMsgs, "; ")
		return d
	case enabled != nil && !*enabled:
		d.optedOut = true
		d.reason = datadoghqv1alpha1.TargetStretchReasonOptedOut
		d.message = "the WPA opted out of the cluster-wide target stretch"
		return d
	case cfg.Mode.IsOptOut() || (enabled != nil && *enabled):
		d.selected = true
	default:
		d.reason = datadoghqv1alpha1.TargetStretchReasonNotOptedIn
		d.message = fmt.Sprintf("the WPA did not opt in (%s: \"true\") while the mode is %s", datadoghqv1alpha1.TargetStretchEnabledAnnotationKey, cfg.Mode)
		return d
	}

	d.appliedRatioMilli = cfg.RatioMilli
	if capMilli < cfg.RatioMilli {
		d.appliedRatioMilli = capMilli
		d.capped = true
	}
	ratio := config.FormatRatioMilli(d.appliedRatioMilli)
	d.reason = datadoghqv1alpha1.TargetStretchReasonEnforced
	if d.capped {
		d.reason = datadoghqv1alpha1.TargetStretchReasonCappedByWPA
	}
	switch {
	case cfg.Mode.IsDryRun():
		d.dryRun = true
		d.reason = datadoghqv1alpha1.TargetStretchReasonDryRun
		d.message = fmt.Sprintf("dry-run: scaling uses the spec watermarks, a shadow recommendation is computed with a ratio of %s", ratio)
	case d.appliedRatioMilli > config.TargetStretchRatioMinMilli:
		d.enforce = true
		d.condStatus = corev1.ConditionTrue
		d.message = fmt.Sprintf("the watermarks used for scaling are stretched by a ratio of %s", ratio)
	default:
		d.enforce = true
		d.message = "the applied ratio is 1, the watermarks are not changed"
	}
	return d
}

// stretchQuantity multiplies q by ratioMilli/1000, rounding half up at the milli unit.
// It keeps the controller's milli semantics (q.MilliValue(), which rounds sub-milli values up), multiplies with
// big.Int so the product cannot overflow, and saturates at math.MaxInt64 milli so consumers never see a negative value.
func stretchQuantity(q *resource.Quantity, ratioMilli int64) *resource.Quantity {
	if q == nil {
		return nil
	}
	if q.Cmp(*resource.NewMilliQuantity(math.MaxInt64, q.Format)) > 0 {
		// MilliValue() would not fit in int64 for this input.
		return resource.NewMilliQuantity(math.MaxInt64, q.Format)
	}
	res := new(big.Int).Mul(big.NewInt(q.MilliValue()), big.NewInt(ratioMilli))
	res.Add(res, big.NewInt(500))
	res.Quo(res, big.NewInt(1000))
	if !res.IsInt64() {
		return resource.NewMilliQuantity(math.MaxInt64, q.Format)
	}
	return resource.NewMilliQuantity(res.Int64(), q.Format)
}

// effectiveMetrics returns a copy of the metrics with stretched watermarks.
func effectiveMetrics(metrics []datadoghqv1alpha1.MetricSpec, ratioMilli int64) []datadoghqv1alpha1.MetricSpec {
	out := make([]datadoghqv1alpha1.MetricSpec, len(metrics))
	for i := range metrics {
		m := metrics[i].DeepCopy()
		if m.External != nil {
			m.External.LowWatermark = stretchQuantity(m.External.LowWatermark, ratioMilli)
			m.External.HighWatermark = stretchQuantity(m.External.HighWatermark, ratioMilli)
		}
		if m.Resource != nil {
			m.Resource.LowWatermark = stretchQuantity(m.Resource.LowWatermark, ratioMilli)
			m.Resource.HighWatermark = stretchQuantity(m.Resource.HighWatermark, ratioMilli)
		}
		out[i] = *m
	}
	return out
}

// effectiveRecommender returns a copy of the recommender spec with stretched watermarks.
func effectiveRecommender(reco *datadoghqv1alpha1.RecommenderSpec, ratioMilli int64) *datadoghqv1alpha1.RecommenderSpec {
	if reco == nil {
		return nil
	}
	out := reco.DeepCopy()
	out.LowWatermark = stretchQuantity(out.LowWatermark, ratioMilli)
	out.HighWatermark = stretchQuantity(out.HighWatermark, ratioMilli)
	return out
}

// metricSpecName returns the metric name used in the prometheus metric_name label.
func metricSpecName(m datadoghqv1alpha1.MetricSpec) string {
	if m.Resource != nil {
		return string(m.Resource.Name)
	}
	if m.External != nil {
		return m.External.MetricName
	}
	return ""
}

// metricsForScaling returns the metrics used for scaling: stretched when enforced.
func metricsForScaling(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, d targetStretchDecision) []datadoghqv1alpha1.MetricSpec {
	if d.enforce {
		return effectiveMetrics(wpa.Spec.Metrics, d.appliedRatioMilli)
	}
	return wpa.Spec.Metrics
}

// recommenderForScaling returns the recommender spec used for scaling: stretched when enforced.
func recommenderForScaling(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, d targetStretchDecision) *datadoghqv1alpha1.RecommenderSpec {
	if d.enforce {
		return effectiveRecommender(wpa.Spec.Recommender, d.appliedRatioMilli)
	}
	return wpa.Spec.Recommender
}

func effectiveWatermarksStatus(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, ratioMilli int64) []datadoghqv1alpha1.TargetStretchWatermarks {
	// The stretch is evaluated before fillMissingWatermark runs on the reconciled object.
	wpa = withFilledWatermarks(wpa)
	var out []datadoghqv1alpha1.TargetStretchWatermarks
	if wpa.Spec.Recommender != nil {
		reco := effectiveRecommender(wpa.Spec.Recommender, ratioMilli)
		if reco.LowWatermark != nil && reco.HighWatermark != nil {
			out = append(out, datadoghqv1alpha1.TargetStretchWatermarks{Metric: metricNameForRecommender(&wpa.Spec), LowWatermark: *reco.LowWatermark, HighWatermark: *reco.HighWatermark})
		}
		return out
	}
	for _, m := range effectiveMetrics(wpa.Spec.Metrics, ratioMilli) {
		var lw, hw *resource.Quantity
		switch {
		case m.External != nil:
			lw, hw = m.External.LowWatermark, m.External.HighWatermark
		case m.Resource != nil:
			lw, hw = m.Resource.LowWatermark, m.Resource.HighWatermark
		}
		if lw == nil || hw == nil {
			continue
		}
		out = append(out, datadoghqv1alpha1.TargetStretchWatermarks{Metric: metricSpecName(m), LowWatermark: *lw, HighWatermark: *hw})
	}
	return out
}

// withFilledWatermarks returns a copy of the WPA where a missing low or high watermark takes the other value.
func withFilledWatermarks(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) *datadoghqv1alpha1.WatermarkPodAutoscaler {
	cp := wpa.DeepCopy()
	fillMissingWatermark(logr.Discard(), cp)
	return cp
}

// removeCondition removes the condition type from the WPA status.
func removeCondition(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, conditionType autoscalingv2.HorizontalPodAutoscalerConditionType) {
	if len(wpa.Status.Conditions) == 0 {
		return
	}
	out := wpa.Status.Conditions[:0:0]
	for _, c := range wpa.Status.Conditions {
		if c.Type != conditionType {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = nil
	}
	wpa.Status.Conditions = out
}

// applyTargetStretchStatus sets the TargetStretched condition and the status.targetStretch block.
// The ReplicasRecommended field is set later by the dry-run shadow computation.
func applyTargetStretchStatus(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, d targetStretchDecision) {
	if d.selected || d.annotated {
		setCondition(wpa, datadoghqv1alpha1.WatermarkPodAutoscalerStatusTargetStretched, d.condStatus, d.reason, "%s", d.message)
	} else {
		removeCondition(wpa, datadoghqv1alpha1.WatermarkPodAutoscalerStatusTargetStretched)
	}
	if !d.selected {
		wpa.Status.TargetStretch = nil
		return
	}
	wpa.Status.TargetStretch = &datadoghqv1alpha1.TargetStretchStatus{
		Mode:                string(d.mode),
		AppliedRatio:        config.FormatRatioMilli(d.appliedRatioMilli),
		EffectiveWatermarks: effectiveWatermarksStatus(wpa, d.appliedRatioMilli),
		// Cleared on every reconcile: only a successful shadow computation sets it again.
		ReplicasRecommended: nil,
	}
}

func statusIsStretching(s *datadoghqv1alpha1.TargetStretchStatus) bool {
	return s != nil && config.TargetStretchMode(s.Mode).IsEnforced() && s.AppliedRatio != config.FormatRatioMilli(config.TargetStretchRatioMinMilli)
}

// emitTargetStretchEvents emits events when the stretched watermarks used for scaling change (enabled modes only).
func (r *WatermarkPodAutoscalerReconciler) emitTargetStretchEvents(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, prev *datadoghqv1alpha1.WatermarkPodAutoscalerStatus) {
	if r.eventRecorder == nil {
		return
	}
	var prevTS *datadoghqv1alpha1.TargetStretchStatus
	if prev != nil {
		prevTS = prev.TargetStretch
	}
	cur := wpa.Status.TargetStretch
	prevOn, curOn := statusIsStretching(prevTS), statusIsStretching(cur)
	switch {
	case curOn && (!prevOn || prevTS.AppliedRatio != cur.AppliedRatio || !apiequality.Semantic.DeepEqual(prevTS.EffectiveWatermarks, cur.EffectiveWatermarks)):
		parts := make([]string, 0, len(cur.EffectiveWatermarks))
		for _, w := range cur.EffectiveWatermarks {
			parts = append(parts, fmt.Sprintf("%s: low=%s high=%s", w.Metric, w.LowWatermark.String(), w.HighWatermark.String()))
		}
		r.eventRecorder.Eventf(wpa, nil, corev1.EventTypeNormal, datadoghqv1alpha1.ReasonTargetStretchApplied, datadoghqv1alpha1.ReasonTargetStretchApplied,
			"Target stretch ratio %s applied (mode %s): %s", cur.AppliedRatio, cur.Mode, strings.Join(parts, ", "))
	case prevOn && !curOn:
		r.eventRecorder.Eventf(wpa, nil, corev1.EventTypeNormal, datadoghqv1alpha1.ReasonTargetStretchRemoved, datadoghqv1alpha1.ReasonTargetStretchRemoved,
			"Target stretch removed, scaling uses the spec watermarks")
	}
}

// computeShadowReplicas computes, in dry-run modes, the replica count the WPA would recommend with the stretched
// watermarks. It reuses the metric values already fetched, and skips forbidden windows, delays and limit factors.
func (r *WatermarkPodAutoscalerReconciler) computeShadowReplicas(ctx context.Context, logger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, scale *autoscalingv1.Scale, d targetStretchDecision, calcs []ReplicaCalculation) *int32 {
	shadow := wpa.DeepCopy()
	var replicas int32
	if wpa.Spec.Recommender != nil {
		reco := effectiveRecommender(wpa.Spec.Recommender, d.appliedRatioMilli)
		calc, err := r.replicaCalc.GetRecommenderShadowReplicas(ctx, logger, scale, shadow, reco)
		if err != nil {
			logger.Info("Failed to compute the target stretch shadow recommendation", "error", err)
			return nil
		}
		replicas = calc.replicaCount
	} else {
		metrics := effectiveMetrics(wpa.Spec.Metrics, d.appliedRatioMilli)
		found := false
		for i := range metrics {
			if i >= len(calcs) {
				break
			}
			calc := calcs[i]
			proposal := calc.replicaCount
			if calc.usageValid {
				var lw, hw *resource.Quantity
				switch {
				case metrics[i].External != nil:
					lw, hw = metrics[i].External.LowWatermark, metrics[i].External.HighWatermark
				case metrics[i].Resource != nil:
					lw, hw = metrics[i].Resource.LowWatermark, metrics[i].Resource.HighWatermark
				}
				if lw == nil || hw == nil {
					continue
				}
				proposal, _, _, _, _ = computeReplicaCountForUsage(logr.Discard(), scale.Spec.Replicas, calc.readyReplicas, shadow, calc.adjustedUsage, lw, hw)
			}
			if !found || proposal > replicas {
				replicas = proposal
				found = true
			}
		}
		if !found {
			return nil
		}
	}
	minReplicas := int32(1)
	if wpa.Spec.MinReplicas != nil && *wpa.Spec.MinReplicas > minReplicas {
		minReplicas = *wpa.Spec.MinReplicas
	}
	replicas = math32.Cap(replicas, minReplicas, wpa.Spec.MaxReplicas)
	logger.Info("Target stretch shadow recommendation", "replicasRecommended", replicas, "ratio", config.FormatRatioMilli(d.appliedRatioMilli))
	return &replicas
}

// targetStretchFlags is the per-WPA state used for the controller-level counts.
type targetStretchFlags struct {
	selected, stretched, capped, optedOut bool
}

// targetStretchTracker keeps the controller-level WPA counts. Totals are maintained incrementally so a
// reconcile never scans every tracked WPA; an unchanged WPA does not touch the gauges at all.
type targetStretchTracker struct {
	mu     sync.Mutex
	wpas   map[types.NamespacedName]targetStretchFlags
	totals targetStretchTotals
}

type targetStretchTotals struct {
	selected, stretched, capped, optedOut int
}

func (t *targetStretchTotals) apply(f targetStretchFlags, delta int) {
	if f.selected {
		t.selected += delta
	}
	if f.stretched {
		t.stretched += delta
	}
	if f.capped {
		t.capped += delta
	}
	if f.optedOut {
		t.optedOut += delta
	}
}

var stretchTracker = &targetStretchTracker{wpas: map[types.NamespacedName]targetStretchFlags{}}

func (t *targetStretchTracker) update(key types.NamespacedName, d targetStretchDecision) {
	flags := targetStretchFlags{selected: d.selected, stretched: d.stretching(), capped: d.selected && d.capped, optedOut: d.optedOut}
	t.mu.Lock()
	defer t.mu.Unlock()
	old, found := t.wpas[key]
	if found && old == flags {
		return
	}
	if found {
		t.totals.apply(old, -1)
	}
	t.totals.apply(flags, 1)
	t.wpas[key] = flags
	t.publishLocked()
}

func (t *targetStretchTracker) remove(key types.NamespacedName) {
	t.mu.Lock()
	defer t.mu.Unlock()
	old, found := t.wpas[key]
	if !found {
		return
	}
	t.totals.apply(old, -1)
	delete(t.wpas, key)
	t.publishLocked()
}

func (t *targetStretchTracker) publishLocked() {
	targetStretchWPAs.With(prometheus.Labels{statePromLabel: "selected"}).Set(float64(t.totals.selected))
	targetStretchWPAs.With(prometheus.Labels{statePromLabel: "stretched"}).Set(float64(t.totals.stretched))
	targetStretchWPAs.With(prometheus.Labels{statePromLabel: "capped"}).Set(float64(t.totals.capped))
	targetStretchWPAs.With(prometheus.Labels{statePromLabel: "opted_out"}).Set(float64(t.totals.optedOut))
}

// RecordTargetStretchConfigMetrics publishes the controller-level target stretch configuration gauges.
func RecordTargetStretchConfigMetrics(cfg config.TargetStretchConfig) {
	targetStretchConfigValid.Set(boolToFloat64(cfg.Valid))
	for _, m := range config.AllTargetStretchModes {
		targetStretchMode.With(prometheus.Labels{modePromLabel: string(m)}).Set(boolToFloat64(m == cfg.Mode))
	}
	targetStretchRatio.Set(float64(cfg.RatioMilli) / 1000)
}

// updateTargetStretchWPAMetrics publishes the per-WPA target stretch gauges, only for selected WPAs.
func updateTargetStretchWPAMetrics(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, d targetStretchDecision) {
	if !d.selected {
		cleanupTargetStretchMetrics(wpa)
		return
	}
	wpa = withFilledWatermarks(wpa)
	labels := getPrometheusLabels(wpa)
	targetStretchAppliedRatio.With(labels).Set(float64(d.appliedRatioMilli) / 1000)
	// Outside dry-run there is no shadow. In dry-run the series is kept during the reconcile (no gap for a
	// scrape) and is overwritten on success or deleted by clearFailedShadowGauge.
	if !d.dryRun {
		targetStretchReplicasRecommended.Delete(labels)
	}
	specs := map[string][2]float64{}
	addSpec := func(name string, lw, hw *resource.Quantity) {
		if lw == nil || hw == nil {
			return
		}
		specs[name] = [2]float64{float64(lw.MilliValue()), float64(hw.MilliValue())}
	}
	if wpa.Spec.Recommender != nil {
		addSpec(metricNameForRecommender(&wpa.Spec), wpa.Spec.Recommender.LowWatermark, wpa.Spec.Recommender.HighWatermark)
	}
	for _, m := range wpa.Spec.Metrics {
		switch {
		case m.External != nil:
			addSpec(metricSpecName(m), m.External.LowWatermark, m.External.HighWatermark)
		case m.Resource != nil:
			addSpec(metricSpecName(m), m.Resource.LowWatermark, m.Resource.HighWatermark)
		}
	}
	specSeries.publish(wpa, specs)
}

// clearFailedShadowGauge deletes the shadow gauge when this reconcile did not produce a shadow recommendation
// (metric lookup failure, shadow failure or early exit), so a stale value is never exported.
func clearFailedShadowGauge(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, d targetStretchDecision) {
	if d.dryRun && (wpa.Status.TargetStretch == nil || wpa.Status.TargetStretch.ReplicasRecommended == nil) {
		targetStretchReplicasRecommended.Delete(getPrometheusLabels(wpa))
	}
}

// specSeriesRegistry records, per WPA, the exact spec_low/high_watermark label sets that were published, so cleanup
// deletes exactly those series (including an empty metric_name, or names from an older spec published by an
// in-flight reconcile) without scanning the gauge vectors. Publishing and cleanup share one mutex, so a reconcile
// cannot publish a series that a concurrent cleanup misses.
type specSeriesRegistry struct {
	mu     sync.Mutex
	series map[types.NamespacedName]map[string]prometheus.Labels
}

var specSeries = &specSeriesRegistry{series: map[types.NamespacedName]map[string]prometheus.Labels{}}

func wpaKey(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) types.NamespacedName {
	return types.NamespacedName{Namespace: wpa.Namespace, Name: wpa.Name}
}

// publish sets the spec watermark gauges for the given metric names (name -> {low, high}) and deletes the
// previously published series that are not part of this set.
func (s *specSeriesRegistry) publish(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, specs map[string][2]float64) {
	key := wpaKey(wpa)
	s.mu.Lock()
	defer s.mu.Unlock()
	current := make(map[string]prometheus.Labels, len(specs))
	for name, v := range specs {
		l := getPrometheusLabels(wpa)
		l[metricNamePromLabel] = name
		specLowWatermark.With(l).Set(v[0])
		specHighWatermark.With(l).Set(v[1])
		current[labelsID(l)] = l
	}
	for id, l := range s.series[key] {
		if _, kept := current[id]; !kept {
			specLowWatermark.Delete(l)
			specHighWatermark.Delete(l)
		}
	}
	if len(current) == 0 {
		delete(s.series, key)
		return
	}
	s.series[key] = current
}

// labelsID returns a deterministic identifier for a label set.
func labelsID(l prometheus.Labels) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(l[k]))
		b.WriteByte(',')
	}
	return b.String()
}

// forget deletes every spec watermark series recorded for the WPA and drops its entry.
func (s *specSeriesRegistry) forget(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) {
	key := wpaKey(wpa)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.series[key] {
		specLowWatermark.Delete(l)
		specHighWatermark.Delete(l)
	}
	delete(s.series, key)
}

func cleanupTargetStretchMetrics(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) {
	labels := getPrometheusLabels(wpa)
	targetStretchAppliedRatio.Delete(labels)
	targetStretchReplicasRecommended.Delete(labels)
	// Delete exactly the spec watermark series published for this WPA.
	specSeries.forget(wpa)
}
