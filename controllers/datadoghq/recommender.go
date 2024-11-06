// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	autoscaling "github.com/DataDog/agent-payload/v5/autoscaling/kubernetes"
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
	GetReplicaRecommendation(request *ReplicaRecommendationRequest) (*ReplicaRecommendationResponse, error)
}

type RecommenderClientImpl struct {
	client *http.Client
}

type ReplicaRecommendationRequest struct {
	Namespace            string
	TargetRef            *v1alpha1.CrossVersionObjectReference
	Recommender          *v1alpha1.RecommenderSpec
	DesiredReplicas      int32
	CurrentReplicas      int32
	CurrentReadyReplicas int32
	MinReplicas          int32
	MaxReplicas          int32
}

type ReplicaRecommendationResponse struct {
	Replicas           int
	ReplicasLowerBound int
	ReplicasUpperBound int
	Timestamp          time.Time
	Details            string
}

// NewRecommenderClient returns a new RecommenderClient with the given http.Client.
func NewRecommenderClient(client *http.Client) RecommenderClient {
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	return &RecommenderClientImpl{
		client: client,
	}
}

// instrumentedClient returns a copy of the client with an instrumented Transport for this recommender.
//
// The returned client is a shallow copy of the original client, with the Transport field replaced
// with an instrumented RoundTripper (which just wraps the original Transport).
func (r *RecommenderClientImpl) instrumentedClient(recommender string) *http.Client {
	client := *r.client
	client.Transport = instrumentRoundTripper(recommender, client.Transport)
	return &client
}

func instrumentRoundTripper(recommender string, transport http.RoundTripper) http.RoundTripper {
	labels := prometheus.Labels{"recommender": recommender}

	return promhttp.InstrumentRoundTripperCounter(
		requestsTotal.MustCurryWith(labels),
		promhttp.InstrumentRoundTripperInFlight(
			responseInflight.With(labels),
			promhttp.InstrumentRoundTripperDuration(
				requestDuration.MustCurryWith(labels),
				transport,
			),
		),
	)
}

// GetReplicaRecommendation returns a recommendation for the number of replicas to scale to
// based on the given ReplicaRecommendationRequest.
//
// Currently, it supports http based recommendation service, but we need to implement grpc services too.
func (r *RecommenderClientImpl) GetReplicaRecommendation(request *ReplicaRecommendationRequest) (*ReplicaRecommendationResponse, error) {
	reco := request.Recommender
	if reco == nil {
		return &ReplicaRecommendationResponse{}, fmt.Errorf("recommender spec is required")
	}

	u, err := url.Parse(reco.URL)
	if err != nil {
		return &ReplicaRecommendationResponse{}, fmt.Errorf("error parsing url: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return &ReplicaRecommendationResponse{}, fmt.Errorf("only http and https schemes are supported")
	}

	req, err := buildWorkloadRecommendationRequest(request)
	if err != nil {
		return &ReplicaRecommendationResponse{}, err
	}

	payload, err := protojson.Marshal(req)
	if err != nil {
		return &ReplicaRecommendationResponse{}, fmt.Errorf("error marshaling request: %w", err)
	}

	// TODO: We might want to make the timeout configurable later.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := r.instrumentedClient(request.Recommender.URL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "wpa-controller")

	if err != nil {
		return &ReplicaRecommendationResponse{}, fmt.Errorf("error creating request: %w", err)
	}
	resp, err := client.Do(httpReq)

	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	if err != nil {
		return &ReplicaRecommendationResponse{}, fmt.Errorf("error sending request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &ReplicaRecommendationResponse{}, fmt.Errorf("unexpected response code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &ReplicaRecommendationResponse{}, fmt.Errorf("error reading response: %w", err)
	}

	reply := &autoscaling.WorkloadRecommendationReply{}
	err = protojson.Unmarshal(body, reply)
	if err != nil {
		return &ReplicaRecommendationResponse{}, fmt.Errorf("error unmarshaling response: %w", err)
	}

	return buildReplicaRecommendationResponse(reply)
}

// buildWorkloadRecommendationRequest builds a WorkloadRecommendationRequest from a ReplicaRecommendationRequest
func buildWorkloadRecommendationRequest(request *ReplicaRecommendationRequest) (*autoscaling.WorkloadRecommendationRequest, error) {
	reco := request.Recommender
	if reco == nil {
		return &autoscaling.WorkloadRecommendationRequest{}, fmt.Errorf("recommender spec is required")
	}
	upperBound, lowerBound := float64(0), float64(0)
	if reco.HighWatermark != nil {
		upperBound = float64(reco.HighWatermark.MilliValue() / 1000.)
	}
	if reco.LowWatermark != nil {
		lowerBound = float64(reco.LowWatermark.MilliValue() / 1000.)
	}
	target := &autoscaling.WorkloadRecommendationTarget{
		Type:        request.Recommender.TargetType,
		TargetValue: 0.,
		UpperBound:  upperBound,
		LowerBound:  lowerBound,
	}

	req := &autoscaling.WorkloadRecommendationRequest{
		State: &autoscaling.WorkloadState{
			DesiredReplicas: request.DesiredReplicas,
			CurrentReplicas: &request.CurrentReplicas,
			ReadyReplicas:   &request.CurrentReadyReplicas,
		},
		TargetRef: &autoscaling.WorkloadTargetRef{
			Kind:       request.TargetRef.Kind,
			Name:       request.TargetRef.Name,
			ApiVersion: request.TargetRef.APIVersion,
			Namespace:  request.Namespace,
		},
		Constraints: &autoscaling.WorkloadRecommendationConstraints{
			MinReplicas: request.MinReplicas,
			MaxReplicas: request.MaxReplicas,
		},
		Targets: []*autoscaling.WorkloadRecommendationTarget{target},
	}
	if len(reco.Settings) > 0 {
		req.Settings = make(map[string]*structpb.Value)
		for k, v := range reco.Settings {
			req.Settings[k] = structpb.NewStringValue(v)
		}
	}

	return req, nil
}

// buildReplicaRecommendationResponse builds a ReplicaRecommendationResponse from a WorkloadRecommendationReply
func buildReplicaRecommendationResponse(reply *autoscaling.WorkloadRecommendationReply) (*ReplicaRecommendationResponse, error) {
	if reply.GetError() != nil {
		return nil, fmt.Errorf("error from recommender: %d %s", reply.GetError().GetCode(), reply.GetError().GetMessage())
	}

	ret := &ReplicaRecommendationResponse{
		Replicas:           int(reply.GetTargetReplicas()),
		ReplicasLowerBound: int(reply.GetLowerBoundReplicas()),
		ReplicasUpperBound: int(reply.GetUpperBoundReplicas()),
		Timestamp:          reply.GetTimestamp().AsTime(),
		Details:            reply.GetReason(),
	}
	return ret, nil
}

var _ RecommenderClient = &RecommenderClientImpl{}
