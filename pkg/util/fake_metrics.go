// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package util

import "encoding/json"

// JSONEncode encodes a FakeMetric into json string
func JSONEncode(metrics []FakeMetric) (string, error) {
	encoded, err := json.Marshal(metrics)
	return string(encoded), err
}

// FakeMetric is used to define metrics to be exposed by the fake custom metrics server
type FakeMetric struct {
	Value        string            `json:"value"`
	MetricName   string            `json:"metricName"`
	MetricLabels map[string]string `json:"metricLabels"`
}
