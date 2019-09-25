package utils

import "testing"

func TestJSONEncode(t *testing.T) {
	type args struct {
		m []FakeMetric
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "1 metric",
			args: args{
				m: []FakeMetric{
					{
						Value:      "1337",
						MetricName: "aMetricName",
						MetricLabels: map[string]string{
							"aLabel": "aValue",
						},
					},
				},
			},
			want:    "[{\"value\":\"1337\",\"metricName\":\"aMetricName\",\"metricLabels\":{\"aLabel\":\"aValue\"}}]",
			wantErr: false,
		},
		{
			name: "2 metrics",
			args: args{
				m: []FakeMetric{
					{
						Value:      "1337",
						MetricName: "aMetricName",
						MetricLabels: map[string]string{
							"aLabel": "aValue",
						},
					},
					{
						Value:      "7331",
						MetricName: "anotherMetricName",
						MetricLabels: map[string]string{
							"anotherLabel": "anotherValue",
						},
					},
				},
			},
			want:    "[{\"value\":\"1337\",\"metricName\":\"aMetricName\",\"metricLabels\":{\"aLabel\":\"aValue\"}},{\"value\":\"7331\",\"metricName\":\"anotherMetricName\",\"metricLabels\":{\"anotherLabel\":\"anotherValue\"}}]",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := JSONEncode(tt.args.m)
			if (err != nil) != tt.wantErr {
				t.Errorf("JsonEncode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("JsonEncode() = %v, want %v", got, tt.want)
			}
		})
	}
}
