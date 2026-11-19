// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package v1alpha1

const (
	// ConditionReasonSuccessfulGetScale Condition when the target's scale can be retrieved
	ConditionReasonSuccessfulGetScale = "SucceededGetScale"
	// ConditionReasonScalingDisabled Condition when scaling is disable for the target
	ConditionReasonScalingDisabled = "ScalingDisabled"
	// ConditionReasonSuccessfulScale Condition reason for Succeeded Rescale
	ConditionReasonSuccessfulScale = "SuccessfulScale"
	// ConditionReasonFailedScale Condition reason for Failed Rescale
	ConditionReasonFailedScale = "FailedScale"
	// ConditionReasonReadyForScale Condition reason when the target is ready to be Scaled
	ConditionReasonReadyForScale = "ReadyForScale"
	// ConditionReasonFailedUpdateReplicasStatus Condition when unable to scale and update the target's status
	ConditionReasonFailedUpdateReplicasStatus = "FailedUpdateReplicas"
	// ConditionReasonBackOffDownscale Condition when downscaling is forbidden
	ConditionReasonBackOffDownscale = "BackoffDownscale"
	// ConditionReasonBackOffUpscale Condition when upscaling is forbidden
	ConditionReasonBackOffUpscale = "BackoffUpscale"
	// ConditionReasonBackOff Condition when scaling is forbidden
	ConditionReasonBackOff = "BackoffBoth"
	// ConditionReasonFailedGetExternalMetrics Condition when the External Metrics Server does not serve a metric
	ConditionReasonFailedGetExternalMetrics = "FailedGetExternalMetric"
	// ConditionReasonFailedGetResourceMetric Condition when the Resource Metrics Server does not serve a metric
	ConditionReasonFailedGetResourceMetric = "FailedGetResourceMetric"
	// ConditionValidMetricFound Condition when a valid metric is retrieved
	ConditionValidMetricFound = "ValidMetricFound"
	// CondistionReasonNotScaling Condition reason when not scaling
	ConditionReasonNotScaling = "NotScaling"
	// ReasonFailedSpecCheck Reason when the spec of the WPA is incorrect
	ReasonFailedSpecCheck = "FailedSpecCheck"
	// ReasonScaling Reason when scaling
	ReasonScaling = "Scaling"
	// ReasonFailedScale Reason when unable to scale
	ReasonFailedScale = "FailedScale"
	// ReasonFailedUpdateReplicasStatus Reason when unable to scale and update the target's status
	ReasonFailedUpdateReplicasStatus = "FailedUpdateReplicas"
	// ReasonFailedUpdateStatus Reason when the status can't be updated
	ReasonFailedUpdateStatus = "FailedUpdateStatus"
	// ReasonFailedProcessWPA Reason when the WPA can't be processed
	ReasonFailedProcessWPA = "FailedProcessWPA"
	// ReasonDatadogMonitorOK Reason when the DatadogMonitor associated with a WPA is in a OK state.
	ReasonDatadogMonitorOK = "DatadogMonitorOK"
	// ReasonDatadogMonitorNotOK Reason when the DatadogMonitor associated with a WPA is not in a OK state.
	ReasonDatadogMonitorNotOK = "DatadogMonitorNotOK"
	// ReasonFailedGetDatadogMonitor Reason when the DatadogMonitor associated with a WPA is not found.
	ReasonFailedGetDatadogMonitor = "FailedGetDatadogMonitor"
)

const (
	// TargetStretchEnabledAnnotationKey opts a WPA in ("true") or out ("false") of the cluster-wide target stretch.
	TargetStretchEnabledAnnotationKey = "wpa.datadoghq.com/target-stretch.enabled"
	// TargetStretchMaxRatioAnnotationKey caps the stretch ratio applied to a WPA (decimal in [1, 2]).
	TargetStretchMaxRatioAnnotationKey = "wpa.datadoghq.com/target-stretch.max-ratio"
	// TargetStretchAnnotationPrefix is the prefix shared by the target stretch annotations.
	TargetStretchAnnotationPrefix = "wpa.datadoghq.com/target-stretch."

	// TargetStretchReasonEnforced the watermarks used for scaling are stretched by the cluster ratio.
	TargetStretchReasonEnforced = "Enforced"
	// TargetStretchReasonCappedByWPA the watermarks are stretched by the WPA max-ratio cap.
	TargetStretchReasonCappedByWPA = "CappedByWPA"
	// TargetStretchReasonDryRun the stretch is only computed as a shadow recommendation.
	TargetStretchReasonDryRun = "DryRun"
	// TargetStretchReasonOptedOut the WPA opted out of the stretch.
	TargetStretchReasonOptedOut = "OptedOut"
	// TargetStretchReasonNotOptedIn the WPA did not opt in while the cluster runs an opt-in mode.
	TargetStretchReasonNotOptedIn = "NotOptedIn"
	// TargetStretchReasonDisabled the cluster-wide stretch is disabled.
	TargetStretchReasonDisabled = "Disabled"
	// TargetStretchReasonInvalidConfig the cluster-wide stretch configuration is invalid.
	TargetStretchReasonInvalidConfig = "InvalidConfig"
	// TargetStretchReasonInvalidAnnotation a target stretch annotation of the WPA cannot be parsed.
	TargetStretchReasonInvalidAnnotation = "InvalidAnnotation"

	// ReasonTargetStretchApplied event reason when the effective watermarks change in an enabled mode.
	ReasonTargetStretchApplied = "TargetStretchApplied"
	// ReasonTargetStretchRemoved event reason when the stretch stops being applied.
	ReasonTargetStretchRemoved = "TargetStretchRemoved"
)
