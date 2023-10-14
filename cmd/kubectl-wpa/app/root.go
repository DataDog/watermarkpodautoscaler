// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// Package app contains kubectl root command plugin logic.
package app

import (
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/DataDog/watermarkpodautoscaler/cmd/kubectl-wpa/app/dryrun"
	"github.com/DataDog/watermarkpodautoscaler/cmd/kubectl-wpa/app/metriccheck"
)

// WatermarkPodAutoscalerOptions provides information required to manage WatermarkPodAutoscaler.
type WatermarkPodAutoscalerOptions struct {
	configFlags *genericclioptions.ConfigFlags
	genericclioptions.IOStreams
}

// NewWatermarkPodAutoscalerOptions provides an instance of WatermarkPodAutoscalerOptions with default values.
func NewWatermarkPodAutoscalerOptions(streams genericclioptions.IOStreams) *WatermarkPodAutoscalerOptions {
	return &WatermarkPodAutoscalerOptions{
		configFlags: genericclioptions.NewConfigFlags(false),

		IOStreams: streams,
	}
}

// NewCmdRoot provides a cobra command wrapping WatermarkPodAutoscalerOptions.
func NewCmdRoot(streams genericclioptions.IOStreams) *cobra.Command {
	o := NewWatermarkPodAutoscalerOptions(streams)

	cmd := &cobra.Command{
		Use: "kubectl wpa [subcommand] [flags]",
	}

	cmd.AddCommand(dryrun.NewCmdDryRun(streams))
	cmd.AddCommand(metriccheck.NewCmdMetricCheck(streams))

	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// Complete sets all information required for processing the command.
func (o *WatermarkPodAutoscalerOptions) Complete(cmd *cobra.Command, args []string) error {
	return nil
}

// Validate ensures that all required arguments and flag values are provided.
func (o *WatermarkPodAutoscalerOptions) Validate() error {
	return nil
}

// Run use to run the command.
func (o *WatermarkPodAutoscalerOptions) Run() error {
	return nil
}
