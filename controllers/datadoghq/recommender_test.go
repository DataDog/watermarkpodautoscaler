// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func NewMockRecommenderClient() *RecommenderClientMock {
	return &RecommenderClientMock{
		ReplicaRecommendationResponse{2, 1, 3, time.Now(), "because"},
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
	_, _ = client.Get("fake")
}
