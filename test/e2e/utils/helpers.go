package utils

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
