// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import "time"

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
