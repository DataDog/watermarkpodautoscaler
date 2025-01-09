// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	autoscaling "github.com/DataDog/agent-payload/v5/autoscaling/kubernetes"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

func NewMockRecommenderClient() *RecommenderClientMock {
	return &RecommenderClientMock{
		ReplicaRecommendationResponse{2, 1, 3, time.Now(), "because", 0.5},
		nil,
	}
}

func (m *RecommenderClientMock) GetReplicaRecommendation(request *ReplicaRecommendationRequest) (*ReplicaRecommendationResponse, error) {
	return &m.ReturnedResponse, m.Error
}

var _ RecommenderClient = &RecommenderClientMock{}

type RecommenderClientMock struct {
	ReturnedResponse ReplicaRecommendationResponse
	Error            error
}

// TODO: Add more tests for the HTTP client
func TestRecommenderClient(t *testing.T) {
	client := NewRecommenderClient(http.DefaultClient)
	// This should not work with empty requests.
	resp, err := client.GetReplicaRecommendation(&ReplicaRecommendationRequest{})
	require.Error(t, err)
	require.Nil(t, resp)
}

func TestInstrumentation(t *testing.T) {
	client := http.DefaultClient
	client.Transport = instrumentRoundTripper("http://test", http.DefaultTransport)
	// This simply makes sure the instrumentation does crash.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "fake://fake", nil)
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
}

func TestRecommenderTargetBuild(t *testing.T) {
	request := &ReplicaRecommendationRequest{
		Namespace: "test",
		TargetRef: &v1alpha1.CrossVersionObjectReference{
			Kind:       "Deployment",
			Name:       "test",
			APIVersion: "v1",
		},
		TargetCluster:        "test",
		Recommender:          nil,
		DesiredReplicas:      6,
		CurrentReplicas:      6,
		CurrentReadyReplicas: 6,
		MinReplicas:          3,
		MaxReplicas:          10,
	}

	tests := []struct {
		name        string
		recommender *v1alpha1.RecommenderSpec
		expect      *autoscaling.WorkloadRecommendationTarget
	}{
		{
			name: "< 1 high/low watermark",
			recommender: &v1alpha1.RecommenderSpec{
				URL:           "https://test",
				Settings:      map[string]string{},
				TargetType:    "memory",
				HighWatermark: resource.NewMilliQuantity(800, resource.DecimalSI),
				LowWatermark:  resource.NewMilliQuantity(200, resource.DecimalSI),
			},
			expect: &autoscaling.WorkloadRecommendationTarget{
				Type:        "memory",
				TargetValue: 0,
				LowerBound:  0.2,
				UpperBound:  0.8,
			},
		},
		{
			name: "Large high/low watermark",
			recommender: &v1alpha1.RecommenderSpec{
				URL:           "https://test",
				Settings:      map[string]string{},
				TargetType:    "memory",
				HighWatermark: resource.NewQuantity(2500, resource.DecimalSI),
				LowWatermark:  resource.NewQuantity(100, resource.DecimalSI),
			},
			expect: &autoscaling.WorkloadRecommendationTarget{
				Type:        "memory",
				TargetValue: 0,
				LowerBound:  100,
				UpperBound:  2500,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request.Recommender = tt.recommender
			got, err := buildWorkloadRecommendationRequest(request)
			require.NoError(t, err)
			require.Len(t, got.GetTargets(), 1)
			targets := got.GetTargets()
			assert.Equal(t, tt.expect, targets[0])
		})
	}
}
