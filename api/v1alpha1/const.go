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
