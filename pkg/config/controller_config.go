// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	sigmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/yaml"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

// ControllerConfigVersionV1 is the only supported version of the controller configuration file.
const ControllerConfigVersionV1 = "v1"

var configValid = prometheus.NewGauge(prometheus.GaugeOpts{
	Subsystem: "wpa_controller",
	Name:      "controller_config_valid",
	Help:      "1 if the last read of the controller configuration file was valid (or no file is configured), 0 otherwise",
})

func init() {
	sigmetrics.Registry.MustRegister(configValid)
	configValid.Set(1)
}

// ControllerConfig is the content of the controller configuration file.
// Unknown keys are ignored so that new sections can be added without breaking older controllers.
type ControllerConfig struct {
	// Version of the file format.
	Version string `json:"version"`
	// Defaults are applied to the WPAs that do not set the corresponding value.
	Defaults Defaults `json:"defaults,omitempty"`
}

// Defaults contains the default values of the WPA settings.
type Defaults struct {
	// ConvergeTowardsWatermark is used when the WPA does not set spec.convergeTowardsWatermark.
	ConvergeTowardsWatermark datadoghqv1alpha1.ConvergeTowardsWatermarkType `json:"convergeTowardsWatermark,omitempty"`
}

// ParseControllerConfig parses and validates the content of a controller configuration file.
// An empty (or comment-only) document is a valid, empty configuration.
func ParseControllerConfig(data []byte) (*ControllerConfig, error) {
	cfg := &ControllerConfig{}
	if len(data) == 0 {
		return cfg, nil
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("unable to parse the controller configuration: %w", err)
	}
	if reflect.DeepEqual(cfg, &ControllerConfig{}) {
		// blank or comment-only document
		return cfg, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks the configuration.
func (c *ControllerConfig) Validate() error {
	if c.Version != ControllerConfigVersionV1 {
		return fmt.Errorf("unsupported controller configuration version %q, expected %q", c.Version, ControllerConfigVersionV1)
	}
	switch c.Defaults.ConvergeTowardsWatermark {
	case "", datadoghqv1alpha1.ConvergeUpwards, datadoghqv1alpha1.ConvergeDownwards, datadoghqv1alpha1.ConvergeDisabled:
	default:
		return fmt.Errorf("invalid defaults.convergeTowardsWatermark %q, expected one of %q, %q, %q or empty",
			c.Defaults.ConvergeTowardsWatermark, datadoghqv1alpha1.ConvergeUpwards, datadoghqv1alpha1.ConvergeDownwards, datadoghqv1alpha1.ConvergeDisabled)
	}
	return nil
}

// ConfigProvider gives access to the current controller configuration.
type ConfigProvider interface {
	// Get returns the current configuration. It never returns nil.
	Get() *ControllerConfig
}

// StaticConfigProvider is a ConfigProvider always returning the same configuration.
type StaticConfigProvider struct {
	Config *ControllerConfig
}

// Get implements ConfigProvider.
func (s StaticConfigProvider) Get() *ControllerConfig {
	if s.Config == nil {
		return &ControllerConfig{}
	}
	return s.Config
}

// FileProvider loads the controller configuration from a file and reloads it when it changes.
// It implements manager.Runnable (to be added with mgr.Add) and exposes a channel of events
// fired each time the configuration changes.
type FileProvider struct {
	path    string
	log     logr.Logger
	current atomic.Pointer[ControllerConfig]
	changes chan event.GenericEvent
}

// NewFileProvider creates a FileProvider and loads the file once.
// A missing file or an invalid content results in an empty configuration (feature off), reported in the logs
// and in the controller_config_valid metric, until a valid file shows up.
func NewFileProvider(path string, log logr.Logger) *FileProvider {
	p := &FileProvider{
		path:    path,
		log:     log,
		changes: make(chan event.GenericEvent, 1),
	}
	p.current.Store(&ControllerConfig{})
	p.load(true)
	return p
}

// Get implements ConfigProvider.
func (p *FileProvider) Get() *ControllerConfig {
	return p.current.Load()
}

// Changes returns the channel on which an event is sent each time the configuration changes.
func (p *FileProvider) Changes() <-chan event.GenericEvent {
	return p.changes
}

// reload reloads the file and notifies the subscribers if the configuration changed.
func (p *FileProvider) reload() {
	if !p.load(false) {
		return
	}
	select {
	case p.changes <- event.GenericEvent{Object: &datadoghqv1alpha1.WatermarkPodAutoscaler{}}:
	default: // a notification is already pending
	}
}

// load reads the file and swaps the current configuration, it returns true if the configuration changed.
// The last valid configuration is kept on error. A missing file is an empty configuration at startup only:
// once a configuration is loaded, a file that disappears (volume being remounted, ConfigMap deleted) must not
// silently drop the defaults. They are removed by setting them to empty in the file.
func (p *FileProvider) load(startup bool) bool {
	data, err := os.ReadFile(p.path)
	if err != nil && (!startup || !errors.Is(err, os.ErrNotExist)) {
		p.log.Error(err, "unable to read the controller configuration, keeping the previous one", "path", p.path)
		configValid.Set(0)
		return false
	}
	cfg, err := ParseControllerConfig(data)
	if err != nil {
		p.log.Error(err, "invalid controller configuration, keeping the previous one", "path", p.path)
		configValid.Set(0)
		return false
	}
	configValid.Set(1)

	if reflect.DeepEqual(cfg, p.current.Load()) {
		return false
	}
	p.current.Store(cfg)
	p.log.Info("controller configuration loaded", "path", p.path, "defaults", cfg.Defaults)
	return true
}

// resyncPeriod is the interval of the periodic reload, a safety net if a file system event is missed
// or if the directory could not be watched.
const resyncPeriod = time.Minute

// Start watches the configuration file until the context is canceled.
// The parent directory is watched rather than the file: kubelet updates ConfigMap volumes by swapping
// the "..data" symlink, which never touches the file itself. The file is also reloaded periodically, and
// a directory that cannot be watched is not fatal (it is logged, and the periodic reload still applies changes).
func (p *FileProvider) Start(ctx context.Context) error {
	var events <-chan fsnotify.Event
	var errs <-chan error
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		defer func() { _ = watcher.Close() }()
		if err = watcher.Add(filepath.Dir(p.path)); err == nil {
			events, errs = watcher.Events, watcher.Errors
		}
	}
	if err != nil {
		p.log.Error(err, "unable to watch the controller configuration directory, relying on periodic reloads", "path", p.path)
	}

	ticker := time.NewTicker(resyncPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			p.reload()
		case werr, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			p.log.Error(werr, "controller configuration watcher error")
		case <-ticker.C:
			p.reload()
		}
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable: every replica needs its configuration.
func (p *FileProvider) NeedLeaderElection() bool {
	return false
}

// ensure the interfaces are satisfied.
var (
	_ ConfigProvider = (*FileProvider)(nil)
	_ ConfigProvider = StaticConfigProvider{}
	_ client.Object  = (*datadoghqv1alpha1.WatermarkPodAutoscaler)(nil)
)
