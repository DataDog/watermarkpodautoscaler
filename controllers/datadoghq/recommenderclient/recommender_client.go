package recommenderclient

import (
	"time"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

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
	panic("implement me")
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
