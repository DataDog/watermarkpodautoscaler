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

	autoscaling "github.com/DataDog/agent-payload/v5/autoscaling/kubernetes"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
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

func TestPlaintextRecommendation(t *testing.T) {
	now := time.Now().UTC()
	// Start a local HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		reply := &autoscaling.WorkloadRecommendationReply{
			Timestamp:          timestamppb.New(now),
			TargetReplicas:     6,
			LowerBoundReplicas: proto.Int32(4),
			UpperBoundReplicas: proto.Int32(6),
			ObservedTargets:    nil,
			Reason:             "an upscale was needed",
		}
		response, err := protojson.Marshal(reply)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write(response)
	}))
	defer server.Close()

	// inject a stub recommendation request
	rc := NewRecommenderClient(http.DefaultClient)
	request := &ReplicaRecommendationRequest{
		Namespace: "test",
		TargetRef: &v1alpha1.CrossVersionObjectReference{
			Kind: "Deployment",
			Name: "test-service",
		},
		TargetCluster: "",
		Recommender: &v1alpha1.RecommenderSpec{
			URL:           server.URL,
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
	response, err := rc.GetReplicaRecommendation(request)
	require.NoError(t, err)

	expectedResponse := &ReplicaRecommendationResponse{
		Replicas:           6,
		ReplicasLowerBound: 4,
		ReplicasUpperBound: 6,
		Timestamp:          now,
		Details:            "an upscale was needed",
	}
	require.Equal(t, expectedResponse, response)
}

//nolint:errcheck
func TestTLSRecommendation(t *testing.T) {
	now := time.Now().UTC()
	// Start a local HTTP server
	server := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// make sure client used for the request has client certificate
		if len(req.TLS.PeerCertificates) == 0 {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
		reply := &autoscaling.WorkloadRecommendationReply{
			Timestamp:          timestamppb.New(now),
			TargetReplicas:     6,
			LowerBoundReplicas: proto.Int32(4),
			UpperBoundReplicas: proto.Int32(6),
			ObservedTargets:    nil,
			Reason:             "an upscale was needed",
		}
		response, err := protojson.Marshal(reply)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		rw.WriteHeader(http.StatusOK)
		rw.Write(response)
	}))
	defer server.Close()

	// generate a self-signed CA and generate a client certificate
	// signed by this CA certificate
	ca, caPEM, caKey, err := generateCA()
	require.NoError(t, err)
	clientCert, clientKey, err := generateClientCertificate(ca, caKey)
	require.NoError(t, err)

	// make the server actually verify our client certificate
	clientRootCAs := x509.NewCertPool()
	clientRootCAs.AppendCertsFromPEM(caPEM)
	server.TLS.ClientCAs = clientRootCAs
	server.TLS.ClientAuth = tls.RequireAndVerifyClientCert

	// dump certificates to temporary disk
	tmp, err := os.MkdirTemp("", "TestTLSClientOption")
	require.NoError(t, err)
	defer os.RemoveAll(tmp)

	// dump client certificate to a temporary folder for the recommender client
	// to use them, and the server CA
	os.WriteFile(filepath.Join(tmp, "cert.pem"), clientCert, 0700)
	os.WriteFile(filepath.Join(tmp, "key.pem"), clientKey, 0700)
	os.WriteFile(filepath.Join(tmp, "ca.pem"), pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.TLS.Certificates[0].Certificate[0],
	}), 0700)

	// inject a stub recommendation request
	rc := NewRecommenderClient(http.DefaultClient)
	request := &ReplicaRecommendationRequest{
		Namespace: "test",
		TargetRef: &v1alpha1.CrossVersionObjectReference{
			Kind: "Deployment",
			Name: "test-service",
		},
		TargetCluster: "",
		Recommender: &v1alpha1.RecommenderSpec{
			URL: server.URL,
			TLSConfig: &v1alpha1.TLSConfig{
				CAFile:   filepath.Join(tmp, "ca.pem"),
				CertFile: filepath.Join(tmp, "cert.pem"),
				KeyFile:  filepath.Join(tmp, "key.pem"),
			},
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
	response, err := rc.GetReplicaRecommendation(request)
	require.NoError(t, err)

	expectedResponse := &ReplicaRecommendationResponse{
		Replicas:           6,
		ReplicasLowerBound: 4,
		ReplicasUpperBound: 6,
		Timestamp:          now,
		Details:            "an upscale was needed",
	}
	require.Equal(t, expectedResponse, response)
}

//nolint:errcheck
func TestTLSRecommendationWithDefaults(t *testing.T) {
	now := time.Now().UTC()
	// Start a local HTTP server
	server := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// make sure client used for the request has client certificate
		if len(req.TLS.PeerCertificates) == 0 {
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
		reply := &autoscaling.WorkloadRecommendationReply{
			Timestamp:          timestamppb.New(now),
			TargetReplicas:     6,
			LowerBoundReplicas: proto.Int32(4),
			UpperBoundReplicas: proto.Int32(6),
			ObservedTargets:    nil,
			Reason:             "an upscale was needed",
		}
		response, err := protojson.Marshal(reply)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		rw.WriteHeader(http.StatusOK)
		rw.Write(response)
	}))
	defer server.Close()

	// generate a self-signed CA and generate a client certificate
	// signed by this CA certificate
	ca, caPEM, caKey, err := generateCA()
	require.NoError(t, err)
	clientCert, clientKey, err := generateClientCertificate(ca, caKey)
	require.NoError(t, err)

	// make the server actually verify our client certificate
	clientRootCAs := x509.NewCertPool()
	clientRootCAs.AppendCertsFromPEM(caPEM)
	server.TLS.ClientCAs = clientRootCAs
	server.TLS.ClientAuth = tls.RequireAndVerifyClientCert

	// dump certificates to temporary disk
	tmp, err := os.MkdirTemp("", "TestTLSClientOption")
	require.NoError(t, err)
	defer os.RemoveAll(tmp)

	// dump client certificate to a temporary folder for the recommender client
	// to use them, and the server CA
	os.WriteFile(filepath.Join(tmp, "cert.pem"), clientCert, 0700)
	os.WriteFile(filepath.Join(tmp, "key.pem"), clientKey, 0700)
	os.WriteFile(filepath.Join(tmp, "ca.pem"), pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.TLS.Certificates[0].Certificate[0],
	}), 0700)

	// inject a stub recommendation request
	rc := NewRecommenderClient(http.DefaultClient, WithTLSConfig(&v1alpha1.TLSConfig{
		CAFile:   filepath.Join(tmp, "ca.pem"),
		CertFile: filepath.Join(tmp, "cert.pem"),
		KeyFile:  filepath.Join(tmp, "key.pem"),
	}))
	request := &ReplicaRecommendationRequest{
		Namespace: "test",
		TargetRef: &v1alpha1.CrossVersionObjectReference{
			Kind: "Deployment",
			Name: "test-service",
		},
		TargetCluster: "",
		Recommender: &v1alpha1.RecommenderSpec{
			URL:           server.URL,
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
	response, err := rc.GetReplicaRecommendation(request)
	require.NoError(t, err)

	expectedResponse := &ReplicaRecommendationResponse{
		Replicas:           6,
		ReplicasLowerBound: 4,
		ReplicasUpperBound: 6,
		Timestamp:          now,
		Details:            "an upscale was needed",
	}
	require.Equal(t, expectedResponse, response)
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

	// sign certificate wit our client root CA
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
