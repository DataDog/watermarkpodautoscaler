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
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

	autoscaling "github.com/DataDog/agent-payload/v5/autoscaling/kubernetes"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

func metricNameForRecommender(spec *v1alpha1.WatermarkPodAutoscalerSpec) string {
	if spec.Recommender == nil {
		return ""
	}
	args := fmt.Sprintf("targetType:%s", spec.Recommender.TargetType)
	settings := make([]string, 0, len(spec.Recommender.Settings))
	for k := range spec.Recommender.Settings {
		settings = append(settings, k)
	}
	sort.Strings(settings)

	for _, k := range settings {
		args += fmt.Sprintf(",%s:%s", k, spec.Recommender.Settings[k])
	}
	return fmt.Sprintf("recommender{%s}", args)
}

type RecommenderClient interface {
	GetReplicaRecommendation(ctx context.Context, request *ReplicaRecommendationRequest) (*ReplicaRecommendationResponse, error)
}

type RecommenderClientImpl struct {
	client           *http.Client
	certificateCache *tlsCertificateCache
	options          *RecommenderOptions
}

type RecommenderOption func(*RecommenderOptions)

type RecommenderOptions struct {
	tlsConfig *v1alpha1.TLSConfig
}

// WithTLSConfig sets a custom default TLS Config for all Recommender API requests
func WithTLSConfig(tlsConfig *v1alpha1.TLSConfig) RecommenderOption {
	return func(option *RecommenderOptions) {
		option.tlsConfig = tlsConfig
	}
}

type ReplicaRecommendationRequest struct {
	Namespace            string
	TargetRef            *v1alpha1.CrossVersionObjectReference
	TargetCluster        string
	Recommender          *v1alpha1.RecommenderSpec
	DesiredReplicas      int32
	CurrentReplicas      int32
	CurrentReadyReplicas int32
	MinReplicas          int32
	MaxReplicas          int32
}

type ReplicaRecommendationResponse struct {
	Replicas            int
	ReplicasLowerBound  int
	ReplicasUpperBound  int
	Timestamp           time.Time
	Details             string
	ObservedTargetValue float64
}

// NewRecommenderClient returns a new RecommenderClient with the given http.Client.
func NewRecommenderClient(client *http.Client, options ...RecommenderOption) RecommenderClient {
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}

	recommenderSettings := &RecommenderOptions{}
	for _, opt := range options {
		opt(recommenderSettings)
	}

	return &RecommenderClientImpl{
		client:           client,
		certificateCache: newTLSCertificateCache(),
		options:          recommenderSettings,
	}
}

// instrumentedClient returns a copy of the client with an instrumented Transport for this recommender.
//
// The returned client is a shallow copy of the original client, with the Transport field replaced
// with an instrumented RoundTripper (which just wraps the original Transport).
func (r *RecommenderClientImpl) instrumentedClient(recommender string, tlsConfig *v1alpha1.TLSConfig) (*http.Client, error) {
	client := *r.client
	if transport, ok := client.Transport.(*http.Transport); ok && tlsConfig != nil {
		tlsTransport, err := NewCertificateReloadingTransport(tlsConfig, r.certificateCache, transport)
		if err != nil {
			return nil, fmt.Errorf("impossible to setup TLS config: %w", err)
		}
		client.Transport = tlsTransport
	}
	client.Transport = instrumentRoundTripper(recommender, client.Transport)
	return &client, nil
}

func InstrumentRoundTripperErrors(v *prometheus.CounterVec, rt http.RoundTripper) promhttp.RoundTripperFunc {
	return func(r *http.Request) (*http.Response, error) {
		resp, err := rt.RoundTrip(r)
		if err != nil {
			v.With(prometheus.Labels{}).Inc()
		}
		return resp, err
	}
}

func instrumentRoundTripper(recommender string, transport http.RoundTripper) http.RoundTripper {
	labels := prometheus.Labels{clientPromLabel: recommender}

	return httptrace.WrapRoundTripper(
		InstrumentRoundTripperErrors(
			requestErrorsTotal.MustCurryWith(labels),
			promhttp.InstrumentRoundTripperCounter(
				requestsTotal.MustCurryWith(labels),
				promhttp.InstrumentRoundTripperInFlight(
					responseInflight.With(labels),
					promhttp.InstrumentRoundTripperDuration(
						requestDuration.MustCurryWith(labels),
						transport,
					),
				),
			),
		))
}

// GetReplicaRecommendation returns a recommendation for the number of replicas to scale to
// based on the given ReplicaRecommendationRequest.
//
// Currently, it supports http based recommendation service, but we need to implement grpc services too.
func (r *RecommenderClientImpl) GetReplicaRecommendation(ctx context.Context, request *ReplicaRecommendationRequest) (*ReplicaRecommendationResponse, error) {
	reco := request.Recommender
	if reco == nil {
		return nil, fmt.Errorf("recommender spec is required")
	}

	u, err := url.Parse(reco.URL)
	if err != nil {
		return nil, fmt.Errorf("error parsing url: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https schemes are supported")
	}

	req, err := buildWorkloadRecommendationRequest(request)
	if err != nil {
		return nil, err
	}

	payload, err := protojson.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("error marshaling request: %w", err)
	}

	// TODO: We might want to make the timeout configurable later.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := r.instrumentedClient(request.Recommender.URL, mergeTLSConfig(r.options.tlsConfig, request.Recommender.TLSConfig))
	if err != nil {
		return nil, fmt.Errorf("error creating http client: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "wpa-controller")
	resp, err := client.Do(httpReq)

	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	if err != nil {
		return nil, fmt.Errorf("error sending request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.Body != nil {
			errorBody, _ := io.ReadAll(resp.Body)
			if len(errorBody) > 100 {
				errorBody = errorBody[:100]
			}
			return nil, fmt.Errorf("unexpected response code: %d (%s)", resp.StatusCode, errorBody)
		}
		return nil, fmt.Errorf("unexpected response code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}

	reply := &autoscaling.WorkloadRecommendationReply{}
	err = protojson.Unmarshal(body, reply)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling response: %w", err)
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
		upperBound = float64(reco.HighWatermark.MilliValue()) / 1000.0
	}
	if reco.LowWatermark != nil {
		lowerBound = float64(reco.LowWatermark.MilliValue()) / 1000.0
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
			Cluster:    request.TargetCluster,
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
	// We expect a single target (since we support a single targets in the request)
	// If we have it we can extract the observed target value
	if reply.GetObservedTargets() != nil && len(reply.GetObservedTargets()) == 1 {
		ret.ObservedTargetValue = reply.GetObservedTargets()[0].GetTargetValue()
	}
	return ret, nil
}

var _ RecommenderClient = &RecommenderClientImpl{}
