// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/api/resource"

	autoscaling "github.com/DataDog/agent-payload/v5/autoscaling/kubernetes"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

// Mock Recommender Client
// ----------------------

type RecommenderClientMock struct {
	ReturnedResponse ReplicaRecommendationResponse
	Error            error
}

func NewMockRecommenderClient() *RecommenderClientMock {
	return &RecommenderClientMock{
		ReplicaRecommendationResponse{2, 1, 3, time.Now(), "because", 0.5},
		nil,
	}
}

func (m *RecommenderClientMock) GetReplicaRecommendation(_ context.Context, _ *ReplicaRecommendationRequest) (*ReplicaRecommendationResponse, error) {
	return &m.ReturnedResponse, m.Error
}

var _ RecommenderClient = &RecommenderClientMock{}

// Basic Client Tests
// -----------------

// TestRecommenderClient verifies that the client properly handles invalid requests
// by ensuring it returns an error when given an empty request.
func TestRecommenderClient(t *testing.T) {
	client := NewRecommenderClient(http.DefaultClient)
	// This should not work with empty requests.
	resp, err := client.GetReplicaRecommendation(context.Background(), &ReplicaRecommendationRequest{})
	require.Error(t, err)
	require.Nil(t, resp)
}

// TestInstrumentation verifies that the client's instrumentation layer doesn't crash
// when making requests to invalid URLs.
func TestInstrumentation(t *testing.T) {
	client := &http.Client{} // can't use http.DefaultClient here because this test modifies its transport possibly leaking to following tests
	client.Transport = instrumentRoundTripper("http://test", http.DefaultTransport)
	// This simply makes sure the instrumentation does crash.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "fake://fake", nil)
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
}

// Target Building Tests
// --------------------

// TestRecommenderTargetBuild verifies that the client correctly builds workload recommendation targets
// from recommender specifications, handling both small and large watermark values.
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

// TestMetricNameForRecommender verifies that the client correctly generates metric names
// from recommender specifications, including all relevant settings in the name.
func TestMetricNameForRecommender(t *testing.T) {
	wpa := &v1alpha1.WatermarkPodAutoscaler{
		Spec: v1alpha1.WatermarkPodAutoscalerSpec{
			Recommender: &v1alpha1.RecommenderSpec{
				URL: "https://test",
				Settings: map[string]string{
					"lookbackWindowSeconds": "600",
					"consumerGroup":         "my_app",
				},
				TargetType: "memory",
			},
		},
	}
	metricName := metricNameForRecommender(&wpa.Spec)
	assert.Equal(t, "recommender{targetType:memory,consumerGroup:my_app,lookbackWindowSeconds:600}", metricName)
}

// HTTP Client Tests
// ----------------

// Helper Functions
// ---------------

// newTestRequest creates a common test request with default values that can be customized
func newTestRequest(serverURL string, tlsConfig *v1alpha1.TLSConfig) *ReplicaRecommendationRequest {
	return &ReplicaRecommendationRequest{
		Namespace: "test",
		TargetRef: &v1alpha1.CrossVersionObjectReference{
			Kind: "Deployment",
			Name: "test-service",
		},
		TargetCluster: "",
		Recommender: &v1alpha1.RecommenderSpec{
			URL:           serverURL,
			TLSConfig:     tlsConfig,
			Settings:      map[string]string{},
			TargetType:    "memory",
			HighWatermark: resource.NewMilliQuantity(500, resource.DecimalSI),
			LowWatermark:  resource.NewMilliQuantity(705, resource.DecimalSI),
		},
		DesiredReplicas:      0,
		CurrentReplicas:      10,
		CurrentReadyReplicas: 10,
		MinReplicas:          10,
		MaxReplicas:          30,
	}
}

// newExpectedResponse creates a common expected response with default values
func newExpectedResponse(now time.Time) *ReplicaRecommendationResponse {
	return &ReplicaRecommendationResponse{
		Replicas:           6,
		ReplicasLowerBound: 4,
		ReplicasUpperBound: 6,
		Timestamp:          now,
		Details:            "an upscale was needed",
	}
}

// newTestReply creates a common WorkloadRecommendationReply with default values
func newTestReply(now time.Time) *autoscaling.WorkloadRecommendationReply {
	return &autoscaling.WorkloadRecommendationReply{
		Timestamp:          timestamppb.New(now),
		TargetReplicas:     6,
		LowerBoundReplicas: proto.Int32(4),
		UpperBoundReplicas: proto.Int32(6),
		ObservedTargets:    nil,
		Reason:             "an upscale was needed",
	}
}

func startRecommenderStub(now time.Time) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if len(req.TLS.PeerCertificates) == 0 {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
		reply := newTestReply(now)
		response, err := protojson.Marshal(reply)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(response)
	}))
}

func generateCertificates(server *httptest.Server, tmp string) (*x509.Certificate, *rsa.PrivateKey, error) {
	ca, caPEM, caKey, err := generateCA()
	if err != nil {
		return nil, nil, err
	}
	clientCert, clientKey, err := generateClientCertificate(ca, caKey)
	if err != nil {
		return nil, nil, err
	}

	clientRootCAs := x509.NewCertPool()
	clientRootCAs.AppendCertsFromPEM(caPEM)
	server.TLS.ClientCAs = clientRootCAs
	server.TLS.ClientAuth = tls.RequireAndVerifyClientCert

	err = os.WriteFile(filepath.Join(tmp, "cert.pem"), clientCert, 0700)
	if err != nil {
		return nil, nil, err
	}
	err = os.WriteFile(filepath.Join(tmp, "key.pem"), clientKey, 0700)
	if err != nil {
		return nil, nil, err
	}
	err = os.WriteFile(filepath.Join(tmp, "ca.pem"), pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.TLS.Certificates[0].Certificate[0],
	}), 0700)
	if err != nil {
		return nil, nil, err
	}

	return ca, caKey, nil
}

func generateCA() (*x509.Certificate, []byte, *rsa.PrivateKey, error) {
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2019),
		Subject: pkix.Name{
			Organization:  []string{"Datadog, inc."},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"New York"},
			StreetAddress: []string{"620 8th Ave"},
			PostalCode:    []string{"10018"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(40, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, err
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, nil, nil, err
	}
	return ca, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	}), caPrivKey, nil
}

func generateClientCertificate(ca *x509.Certificate, caPrivKey *rsa.PrivateKey) ([]byte, []byte, error) {
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"Datadog, inc."},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"New York"},
			StreetAddress: []string{"620 8th Ave"},
			PostalCode:    []string{"10018"},
		},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	certPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &certPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	certPrivKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey),
	})
	return certPEM, certPrivKeyPEM, nil
}

// TestRedirect verifies that the client properly handles HTTP redirects by:
// 1. Following redirects to the new location
// 2. Successfully retrieving recommendations after the redirect
// 3. Maintaining the correct response structure throughout the redirect chain
func TestRedirect(t *testing.T) {
	now := time.Now().UTC()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestURI := r.RequestURI; requestURI == "/redirect" {
			http.Redirect(w, r, "/redirected", http.StatusSeeOther)
			return
		} else if requestURI == "/redirected" {
			reply := newTestReply(now)
			response, err := protojson.Marshal(reply)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(response)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	rc := NewRecommenderClient(http.DefaultClient)
	request := newTestRequest(server.URL+"/redirect", nil)
	response, err := rc.GetReplicaRecommendation(context.Background(), request)
	require.NoError(t, err)

	expectedResponse := newExpectedResponse(now)
	require.Equal(t, expectedResponse, response)
}

// TestPlaintextRecommendation verifies that the client can successfully:
// 1. Make plain HTTP requests to the recommender service
// 2. Parse and return the recommendation response
// 3. Handle the response without TLS configuration
func TestPlaintextRecommendation(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		reply := newTestReply(now)
		response, err := protojson.Marshal(reply)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(response)
	}))
	defer server.Close()

	rc := NewRecommenderClient(http.DefaultClient)
	request := newTestRequest(server.URL, nil)
	response, err := rc.GetReplicaRecommendation(context.Background(), request)
	require.NoError(t, err)

	expectedResponse := newExpectedResponse(now)
	require.Equal(t, expectedResponse, response)
}

// TestTLSRecommendation verifies that the client can successfully:
// 1. Make HTTPS requests with TLS configuration
// 2. Authenticate using client certificates
// 3. Parse and return the recommendation response with TLS enabled
func TestTLSRecommendation(t *testing.T) {
	now := time.Now().UTC()
	server := startRecommenderStub(now)
	defer server.Close()

	tmp, err := os.MkdirTemp("", "TestTLSClientOption")
	require.NoError(t, err)
	defer func(path string) {
		_ = os.RemoveAll(path)
	}(tmp)

	_, _, err = generateCertificates(server, tmp)
	require.NoError(t, err)

	rc := NewRecommenderClient(http.DefaultClient)
	tlsConfig := &v1alpha1.TLSConfig{
		CAFile:   filepath.Join(tmp, "ca.pem"),
		CertFile: filepath.Join(tmp, "cert.pem"),
		KeyFile:  filepath.Join(tmp, "key.pem"),
	}
	request := newTestRequest(server.URL, tlsConfig)
	response, err := rc.GetReplicaRecommendation(context.Background(), request)
	require.NoError(t, err)

	expectedResponse := newExpectedResponse(now)
	require.Equal(t, expectedResponse, response)
}

// TestTLSRecommendationWithDefaults verifies that the client can successfully:
// 1. Use default TLS configuration set at client creation time
// 2. Make HTTPS requests without per-request TLS configuration
// 3. Parse and return the recommendation response using default TLS settings
func TestTLSRecommendationWithDefaults(t *testing.T) {
	now := time.Now().UTC()
	server := startRecommenderStub(now)
	defer server.Close()

	tmp, err := os.MkdirTemp("", "TestTLSClientOption")
	require.NoError(t, err)
	defer func(path string) {
		_ = os.RemoveAll(path)
	}(tmp)

	_, _, err = generateCertificates(server, tmp)
	require.NoError(t, err)

	tlsConfig := &v1alpha1.TLSConfig{
		CAFile:   filepath.Join(tmp, "ca.pem"),
		CertFile: filepath.Join(tmp, "cert.pem"),
		KeyFile:  filepath.Join(tmp, "key.pem"),
	}
	rc := NewRecommenderClient(http.DefaultClient, WithTLSConfig(tlsConfig))
	request := newTestRequest(server.URL, nil)
	response, err := rc.GetReplicaRecommendation(context.Background(), request)
	require.NoError(t, err)

	expectedResponse := newExpectedResponse(now)
	require.Equal(t, expectedResponse, response)
}

// TestTLSRecommendationWithClientCertificateMismatch verifies that the client properly handles
// certificate validation errors by:
// 1. Detecting mismatched client certificates
// 2. Detecting missing client certificates
// 3. Returning appropriate error messages for certificate-related issues
func TestTLSRecommendationWithClientCertificateMismatch(t *testing.T) {
	server := startRecommenderStub(time.Now().UTC())
	defer server.Close()

	tmp, err := os.MkdirTemp("", "TestTLSClientOption")
	require.NoError(t, err)
	defer func(path string) {
		_ = os.RemoveAll(path)
	}(tmp)

	ca, caKey, err := generateCertificates(server, tmp)
	require.NoError(t, err)

	anotherClientCert, _, err := generateClientCertificate(ca, caKey)
	require.NoError(t, err)

	_ = os.WriteFile(filepath.Join(tmp, "cert.pem"), anotherClientCert, 0700)

	rc := NewRecommenderClient(http.DefaultClient)
	tlsConfig := &v1alpha1.TLSConfig{
		CAFile:   filepath.Join(tmp, "ca.pem"),
		CertFile: filepath.Join(tmp, "cert.pem"),
		KeyFile:  filepath.Join(tmp, "key.pem"),
	}
	request := newTestRequest(server.URL, tlsConfig)

	_, err = rc.GetReplicaRecommendation(context.Background(), request)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tls: private key does not match public key")

	_ = os.Remove(filepath.Join(tmp, "cert.pem"))
	_, err = rc.GetReplicaRecommendation(context.Background(), request)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tls: private key does not match public key")
}
