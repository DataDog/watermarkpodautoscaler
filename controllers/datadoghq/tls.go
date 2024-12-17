// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

type tlsTransport struct {
	wrappedTransport *http.Transport
}

func (t *tlsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.wrappedTransport.RoundTrip(req)
}

func NewCertificateReloadingTransport(config *v1alpha1.TLSConfig, underlying *http.Transport) (http.RoundTripper, error) {
	tlsConfig, err := buildTLSConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS client: %w", err)
	}

	underlying.TLSClientConfig = tlsConfig
	return &tlsTransport{wrappedTransport: underlying}, nil
}

func buildTLSConfig(config *v1alpha1.TLSConfig) (*tls.Config, error) {
	var err error
	var minVersion uint16
	if config.MinVersion != "" {
		minVersion, err = toTLSVersion(config.MinVersion)
		if err != nil {
			return nil, fmt.Errorf("invalid minimum TLS version %q: %w", config.MinVersion, err)
		}
	}

	var maxVersion uint16
	if config.MaxVersion != "" {
		maxVersion, err = toTLSVersion(config.MaxVersion)
		if err != nil {
			return nil, fmt.Errorf("invalid maximum TLS version %q: %w", config.MinVersion, err)
		}
	}

	var rootCA *x509.CertPool
	if config.CAFile != "" {
		caPEM, err := os.ReadFile(config.CAFile)
		if err == nil {
			rootCA = x509.NewCertPool()
			if !rootCA.AppendCertsFromPEM(caPEM) {
				return nil, fmt.Errorf("failed to append root CA to pool")
			}
		}
	}

	// reload client certificate on each client connection
	getClientCertificate := func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
		return &cert, err
	}

	return &tls.Config{
		MinVersion:           minVersion,
		MaxVersion:           maxVersion,
		ServerName:           config.ServerName,
		GetClientCertificate: getClientCertificate,
		RootCAs:              rootCA,
	}, nil
}

func toTLSVersion(version string) (uint16, error) {
	if v, ok := TLSVersions[version]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("unknown TLS version: %s", version)
}

var TLSVersions = map[string]uint16{
	"TLS13": tls.VersionTLS13,
	"TLS12": tls.VersionTLS12,
	"TLS11": tls.VersionTLS11,
	"TLS10": tls.VersionTLS10,
}
