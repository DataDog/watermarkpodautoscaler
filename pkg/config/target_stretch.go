// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

const (
	// TargetStretchModeEnvVar is the env var that selects the cluster-wide target stretch mode.
	TargetStretchModeEnvVar = "WPA_STRETCH_MODE"
	// TargetStretchRatioEnvVar is the env var that sets the cluster-wide target stretch ratio.
	TargetStretchRatioEnvVar = "WPA_STRETCH_RATIO"

	// TargetStretchRatioMinMilli is the smallest accepted ratio (1.0), in milli.
	TargetStretchRatioMinMilli int64 = 1000
	// TargetStretchRatioMaxMilli is the largest accepted ratio (2.0), in milli.
	TargetStretchRatioMaxMilli int64 = 2000
)

// TargetStretchMode is the cluster-wide target stretch mode.
type TargetStretchMode string

const (
	// TargetStretchModeDisabled turns the feature off.
	TargetStretchModeDisabled TargetStretchMode = "disabled"
	// TargetStretchModeDryRunOptIn computes a shadow recommendation for opted-in WPAs.
	TargetStretchModeDryRunOptIn TargetStretchMode = "dry-run-opt-in"
	// TargetStretchModeDryRunOptOut computes a shadow recommendation for every WPA that did not opt out.
	TargetStretchModeDryRunOptOut TargetStretchMode = "dry-run-opt-out"
	// TargetStretchModeEnabledOptIn stretches the watermarks of opted-in WPAs.
	TargetStretchModeEnabledOptIn TargetStretchMode = "enabled-opt-in"
	// TargetStretchModeEnabledOptOut stretches the watermarks of every WPA that did not opt out.
	TargetStretchModeEnabledOptOut TargetStretchMode = "enabled-opt-out"
)

// AllTargetStretchModes lists every supported mode.
var AllTargetStretchModes = []TargetStretchMode{
	TargetStretchModeDisabled,
	TargetStretchModeDryRunOptIn,
	TargetStretchModeDryRunOptOut,
	TargetStretchModeEnabledOptIn,
	TargetStretchModeEnabledOptOut,
}

// IsActive returns true when the mode is not disabled.
func (m TargetStretchMode) IsActive() bool { return m.IsDryRun() || m.IsEnforced() }

// IsDryRun returns true for the dry-run modes.
func (m TargetStretchMode) IsDryRun() bool {
	return m == TargetStretchModeDryRunOptIn || m == TargetStretchModeDryRunOptOut
}

// IsEnforced returns true for the enabled modes.
func (m TargetStretchMode) IsEnforced() bool {
	return m == TargetStretchModeEnabledOptIn || m == TargetStretchModeEnabledOptOut
}

// IsOptOut returns true for the opt-out modes.
func (m TargetStretchMode) IsOptOut() bool {
	return m == TargetStretchModeDryRunOptOut || m == TargetStretchModeEnabledOptOut
}

// TargetStretchConfig is the resolved cluster-wide target stretch configuration.
type TargetStretchConfig struct {
	// Mode is the effective mode. It is disabled when the configuration is invalid.
	Mode TargetStretchMode
	// RatioMilli is the effective ratio multiplied by 1000 (1000..2000).
	RatioMilli int64
	// Valid is false when the env values could not be parsed.
	Valid bool
	// Err is the parsing error when Valid is false.
	Err error
}

// TargetStretchConfigProvider returns the current target stretch configuration.
type TargetStretchConfigProvider interface {
	TargetStretchConfig() TargetStretchConfig
}

// StaticTargetStretchConfigProvider returns a fixed configuration.
type StaticTargetStretchConfigProvider struct {
	Config TargetStretchConfig
}

// TargetStretchConfig implements TargetStretchConfigProvider.
func (p *StaticTargetStretchConfigProvider) TargetStretchConfig() TargetStretchConfig {
	return p.Config
}

// DisabledTargetStretchConfig is the configuration used when the feature is off.
func DisabledTargetStretchConfig() TargetStretchConfig {
	return TargetStretchConfig{Mode: TargetStretchModeDisabled, RatioMilli: TargetStretchRatioMinMilli, Valid: true}
}

// LoadTargetStretchConfigFromOSEnv reads the configuration from the process environment.
func LoadTargetStretchConfigFromOSEnv() TargetStretchConfig {
	return LoadTargetStretchConfigFromEnv(os.LookupEnv)
}

// LoadTargetStretchConfigFromEnv parses the configuration with the given lookup function.
// An invalid configuration resolves to the disabled mode with Valid=false and Err set.
func LoadTargetStretchConfigFromEnv(lookup func(string) (string, bool)) TargetStretchConfig {
	cfg, err := parseTargetStretch(lookup)
	if err != nil {
		invalid := DisabledTargetStretchConfig()
		invalid.Valid = false
		invalid.Err = err
		return invalid
	}
	return cfg
}

func parseTargetStretch(lookup func(string) (string, bool)) (TargetStretchConfig, error) {
	rawMode, _ := lookup(TargetStretchModeEnvVar)
	rawRatio, _ := lookup(TargetStretchRatioEnvVar)
	cfg, err := ResolveTargetStretch(rawMode, rawRatio)
	if err != nil {
		return cfg, fmt.Errorf("%w (from %s / %s)", err, TargetStretchModeEnvVar, TargetStretchRatioEnvVar)
	}
	return cfg, nil
}

// ResolveTargetStretch validates a mode and a ratio, shared by the env vars and the controller configuration file.
// An empty mode is disabled; the ratio is required when the mode is active and must be in [1, 2].
func ResolveTargetStretch(rawMode, rawRatio string) (TargetStretchConfig, error) {
	cfg := DisabledTargetStretchConfig()
	rawMode = strings.ToLower(strings.TrimSpace(rawMode))
	if rawMode != "" {
		found := false
		for _, m := range AllTargetStretchModes {
			if string(m) == rawMode {
				cfg.Mode = m
				found = true
				break
			}
		}
		if !found {
			return cfg, fmt.Errorf("invalid target stretch mode %q, expected one of %v", rawMode, AllTargetStretchModes)
		}
	}

	rawRatio = strings.TrimSpace(rawRatio)
	if rawRatio == "" {
		if cfg.Mode.IsActive() {
			return cfg, fmt.Errorf("a target stretch ratio is required when the mode is %q", cfg.Mode)
		}
		return cfg, nil
	}
	milli, err := ParseTargetStretchRatioMilli(rawRatio)
	if err != nil {
		return cfg, fmt.Errorf("invalid target stretch ratio: %w", err)
	}
	cfg.RatioMilli = milli
	return cfg, nil
}

// ParseTargetStretchRatioMilli parses a ratio in [1, 2], rounds it to 3 decimals and returns it in milli.
func ParseTargetStretchRatioMilli(raw string) (int64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", raw)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%q is not a finite number", raw)
	}
	// Check the parsed value before rounding so that 0.9999 or 2.0004 are rejected.
	if f < 1 || f > 2 {
		return 0, fmt.Errorf("%q is outside [1, 2]", raw)
	}
	return int64(math.Round(f * 1000)), nil
}

// FormatRatioMilli formats a milli ratio as a short decimal string (e.g. 1150 -> "1.15").
func FormatRatioMilli(milli int64) string {
	return strconv.FormatFloat(float64(milli)/1000, 'f', -1, 64)
}

// ControllerTargetStretchProvider resolves the target stretch from the controller configuration file when its
// targetStretch section is set, and from the WPA_STRETCH_MODE / WPA_STRETCH_RATIO env vars otherwise. The file is
// validated on load (an invalid file keeps the previous configuration), so the section always resolves.
type ControllerTargetStretchProvider struct {
	// Config is the controller configuration, nil without a file.
	Config Provider
	// Env is the configuration read from the env vars at startup.
	Env TargetStretchConfig
}

// TargetStretchConfig implements TargetStretchConfigProvider.
func (p *ControllerTargetStretchProvider) TargetStretchConfig() TargetStretchConfig {
	if p.Config != nil {
		if s := p.Config.Get().TargetStretch; s != nil {
			cfg, err := s.Resolve()
			if err != nil {
				invalid := DisabledTargetStretchConfig()
				invalid.Valid = false
				invalid.Err = err
				return invalid
			}
			return cfg
		}
	}
	return p.Env
}

var _ TargetStretchConfigProvider = (*ControllerTargetStretchProvider)(nil)
