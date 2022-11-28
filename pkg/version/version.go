// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package version WatermarkPodAutoscaler controller version management.
package version

import (
	"fmt"
	"io"
	"runtime"

	"github.com/go-logr/logr"
)

var (
	// Version binary version
	Version = "0.0.0"
	// BuildTime binary build time
	BuildTime = ""
	// Commit current git commit
	Commit = ""
	// Tag possible git Tag on current commit
	Tag = ""
)

// PrintVersionWriter print versions information in to writer interface
func PrintVersionWriter(writer io.Writer) {
	fmt.Fprintf(writer, "Version:\n")
	for _, val := range printVersionSlice() {
		fmt.Fprintf(writer, "- %s\n", val)
	}
}

// PrintVersionLogs print versions information in logs
func PrintVersionLogs(logger logr.Logger) {
	for _, val := range printVersionSlice() {
		logger.Info(val)
	}
}

func printVersionSlice() []string {
	output := []string{
		fmt.Sprintf("Version: %v", Version),
		fmt.Sprintf("Build time: %v", BuildTime),
		fmt.Sprintf("Git tag: %v", Tag),
		fmt.Sprintf("Git Commit: %v", Commit),
		fmt.Sprintf("Go Version: %s", runtime.Version()),
		fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH),
	}
	return output
}
