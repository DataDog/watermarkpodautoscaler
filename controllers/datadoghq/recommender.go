// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"fmt"
	"time"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

func metricNameForRecommender(spec *v1alpha1.WatermarkPodAutoscalerSpec) string {
	if spec.Recommender == nil {
		return ""
	}
	args := fmt.Sprintf("targetType:%s", spec.Recommender.TargetType)
	for k, v := range spec.Recommender.Settings {
		args += fmt.Sprintf(",%s:%s", k, v)
	}
	return fmt.Sprintf("recommender{%s}", args)
}

type RecommenderClient interface {
	GetReplicaRecommendation(request *ReplicaRecommendationRequest) (ReplicaRecommendationResponse, error)
}

type RecommenderClientImpl struct {
}

type ReplicaRecommendationRequest struct {
	Namespace            string
	TargetRef            *v1alpha1.CrossVersionObjectReference
	Recommender          *v1alpha1.RecommenderSpec
	CurrentReplicas      int32
	CurrentReadyReplicas int32
}

type ReplicaRecommendationResponse struct {
	Replicas           int
	ReplicasLowerBound int
	ReplicasUpperBound int
	Timestamp          time.Time
	Details            string
}

func NewRecommenderClient() RecommenderClient {
	return &RecommenderClientImpl{}
}

func (r *RecommenderClientImpl) GetReplicaRecommendation(request *ReplicaRecommendationRequest) (ReplicaRecommendationResponse, error) {
	return ReplicaRecommendationResponse{}, nil
}

var _ RecommenderClient = &RecommenderClientImpl{}

type RecommenderClientMock struct {
	ReturnedResponse ReplicaRecommendationResponse
	Error            error
}

func NewMockRecommenderClient() *RecommenderClientMock {
	return &RecommenderClientMock{
		ReplicaRecommendationResponse{2, 1, 3, time.Now(), "because"},
		nil,
	}
}

func (m *RecommenderClientMock) GetReplicaRecommendation(request *ReplicaRecommendationRequest) (ReplicaRecommendationResponse, error) {
	return m.ReturnedResponse, m.Error
}

var _ RecommenderClient = &RecommenderClientMock{}
