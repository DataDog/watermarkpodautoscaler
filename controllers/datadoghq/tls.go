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
	"sync"
	"time"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

const (
	certificateCacheTimeout           = 10 * time.Minute
	certificateCacheExpirationTimeout = 10 * time.Minute
	certificateErrorCacheTimeout      = 1 * time.Minute
)

type tlsTransport struct {
	wrappedTransport *http.Transport
}

func (t *tlsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.wrappedTransport.RoundTrip(req)
}

func NewCertificateReloadingTransport(config *v1alpha1.TLSConfig, cache *tlsCertificateCache, underlying *http.Transport) (http.RoundTripper, error) {
	tlsConfig, err := buildTLSConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS client: %w", err)
	}

	transport := &tlsTransport{
		wrappedTransport: underlying,
	}

	tlsConfig.GetClientCertificate = cache.GetClientCertificateReloadingFunc(config.CertFile, config.KeyFile)
	underlying.TLSClientConfig = tlsConfig
	return transport, nil
}

// tlsCertificateCache is an expiring cache to store certificates in memory
// used for TLS connections
type tlsCertificateCache struct {
	mu    sync.RWMutex
	cache map[string]*tlsCacheEntry
}

func newTLSCertificateCache() *tlsCertificateCache {
	cache := &tlsCertificateCache{
		cache: make(map[string]*tlsCacheEntry),
	}
	go cache.run()
	return cache
}

// tlsCacheEntry is an entry of the certificate cache
type tlsCacheEntry struct {
	certificate *tls.Certificate
	err         error
	lastUpdate  time.Time
	lastAccess  time.Time
}

func (c *tlsCacheEntry) isExpired(now time.Time, duration time.Duration) bool {
	return now.After(c.lastUpdate.Add(duration))
}

func (c *tlsCacheEntry) isCertificateExpired(now time.Time) bool {
	// Before Go 1.23 Certificate.Leaf is not populated after tls.LoadX509KeyPair
	return c.certificate.Leaf != nil && now.After(c.certificate.Leaf.NotAfter)
}

func (c *tlsCertificateCache) GetClientCertificateReloadingFunc(certFile, keyFile string) func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		now := time.Now()
		c.mu.RLock()
		entry, ok := c.cache[certFile]
		c.mu.RUnlock()

		// there's a small race condition here, but it does no harm except
		// possibly reloading the certificate from disk more than once at a given time
		if !ok || (entry.err == nil && (entry.isExpired(now, certificateCacheTimeout) || entry.isCertificateExpired(now))) {
			certificate, err := retryLoadingX09Keypair(5, 50*time.Millisecond, certFile, keyFile)

			c.mu.Lock()
			entry = &tlsCacheEntry{
				certificate: certificate,
				err:         err,
				lastUpdate:  now,
				lastAccess:  now,
			}
			c.cache[certFile] = entry
			c.mu.Unlock()
		} else {
			c.mu.Lock()
			entry.lastAccess = now
			c.mu.Unlock()
		}

		return entry.certificate, entry.err
	}
}

// run will evict cached certificate that haven't been accessed after certificateCacheExpirationTimeout
// to free memory.
func (c *tlsCertificateCache) run() {
	for range time.Tick(certificateCacheExpirationTimeout) {
		c.mu.Lock()
		now := time.Now()
		for key, entry := range c.cache {
			if now.After(entry.lastAccess.Add(certificateCacheExpirationTimeout)) {
				delete(c.cache, key)
			}
		}
		c.mu.Unlock()
	}
}

// retryLoadingX09Keypair will attempt to read the certificate/key pair from disk until there's no "private key does not match public key"
// errors. This can happen because filesystems are not atomic and it's possible for a process renewing the certificate and key
// to write while we're trying to read them.
func retryLoadingX09Keypair(attempts int, sleep time.Duration, certFile, keyFile string) (*tls.Certificate, error) {
	var err error
	for range attempts {
		var certificate tls.Certificate
		certificate, err = tls.LoadX509KeyPair(certFile, keyFile)
		if err == nil {
			return &certificate, nil
		}

		// any other errors than certificate not matching key aborts
		if err.Error() != "tls: private key does not match public key" {
			return nil, err
		}

		// let's give us a bit of time to reload properly the files
		time.Sleep(sleep)
	}
	return nil, fmt.Errorf("impossible to load a matching certificate and key after %d attempts, last error: %w", attempts, err)
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

	return &tls.Config{
		MinVersion: minVersion,
		MaxVersion: maxVersion,
		ServerName: config.ServerName,
		RootCAs:    rootCA,
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

// mergeTLSConfig will merge the default TLS config coming from the executable flags and those from the RecommenderSpec
func mergeTLSConfig(defaults *v1alpha1.TLSConfig, recommender *v1alpha1.TLSConfig) *v1alpha1.TLSConfig {
	if defaults == nil {
		return recommender
	}

	if recommender == nil {
		return defaults
	}

	merged := &v1alpha1.TLSConfig{
		CAFile:             recommender.CAFile,
		CertFile:           recommender.CertFile,
		KeyFile:            recommender.KeyFile,
		ServerName:         recommender.ServerName,
		InsecureSkipVerify: recommender.InsecureSkipVerify,
		MinVersion:         recommender.MinVersion,
		MaxVersion:         recommender.MaxVersion,
	}

	if recommender.CAFile == "" {
		merged.CAFile = defaults.CAFile
	}
	if recommender.CertFile == "" {
		merged.CertFile = defaults.CertFile
	}
	if recommender.KeyFile == "" {
		merged.KeyFile = defaults.KeyFile
	}

	return merged
}
