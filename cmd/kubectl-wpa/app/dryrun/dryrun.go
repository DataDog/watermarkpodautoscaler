// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package dryrun

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/DataDog/watermarkpodautoscaler/api/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/cmd/kubectl-wpa/app/common"
)

var dryrunExample = `
	# %[1]s configure WPA dry-run option
	kubectl wpa %[1]s foo
`

// dryrunOptions provides information required to manage WatermarkPodAutoscaler.
type dryrunOptions struct {
	configFlags *genericclioptions.ConfigFlags
	args        []string

	client client.Client

	genericclioptions.IOStreams

	userNamespace string
	userWPAName   string
	enabledDryRun bool
}

// newDryrunOptions provides an instance of GetOptions with default values.
func newDryrunOptions(streams genericclioptions.IOStreams, enabled bool) *dryrunOptions {
	return &dryrunOptions{
		configFlags: genericclioptions.NewConfigFlags(false),

		IOStreams: streams,

		enabledDryRun: enabled,
	}
}

// NewCmdDryRun provides a cobra command wrapping dryrunOptions.
func NewCmdDryRun(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams, true)

	cmd := &cobra.Command{
		Use:          "dry-run [WatermarkPodAutoscaler name]",
		Short:        "configure dry-run mode on a WPA",
		Example:      fmt.Sprintf(dryrunExample, "dryrun"),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.complete(c, args); err != nil {
				return err
			}
			if err := o.validate(); err != nil {
				return err
			}

			return o.run()
		},
	}

	cmd.AddCommand(newCmdDryRunEnabled(streams))
	cmd.AddCommand(newCmdDryRunDisabled(streams))

	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// newCmdDryRunEnabled provides a cobra command wrapping dryrunOptions.
func newCmdDryRunEnabled(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams, true)

	cmd := &cobra.Command{
		Use:          "enabled [WatermarkPodAutoscaler name]",
		Short:        "enabled dry-run mode on a WPA",
		Example:      fmt.Sprintf(dryrunExample, "dryrun"),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.complete(c, args); err != nil {
				return err
			}
			if err := o.validate(); err != nil {
				return err
			}

			return o.run()
		},
	}

	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// newCmdDryRunDisabled provides a cobra command wrapping dryrunOptions.
func newCmdDryRunDisabled(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams, true)

	cmd := &cobra.Command{
		Use:          "disabled [WatermarkPodAutoscaler name]",
		Short:        "disabled dry-run mode on a WPA",
		Example:      fmt.Sprintf(dryrunExample, "dryrun"),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.complete(c, args); err != nil {
				return err
			}
			if err := o.validate(); err != nil {
				return err
			}

			return o.run()
		},
	}

	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// complete sets all information required for processing the command.
func (o *dryrunOptions) complete(cmd *cobra.Command, args []string) error {
	o.args = args
	var err error

	clientConfig := o.configFlags.ToRawKubeConfigLoader()
	// Create the Client for Read/Write operations.
	o.client, err = common.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("unable to instantiate client, err: %w", err)
	}

	o.userNamespace, _, err = clientConfig.Namespace()
	if err != nil {
		return err
	}

	ns, err2 := cmd.Flags().GetString("namespace")
	if err2 != nil {
		return err
	}
	if ns != "" {
		o.userNamespace = ns
	}

	if len(args) > 0 {
		o.userWPAName = args[0]
	}

	// TODO: add support of LabelSelector

	return nil
}

// validate ensures that all required arguments and flag values are provided.
func (o *dryrunOptions) validate() error {
	if len(o.args) < 1 {
		return fmt.Errorf("the watermarkpodautoscaler name is required")
	}

	return nil
}

// run used to run the command.
func (o *dryrunOptions) run() error {
	wpa := &v1alpha1.WatermarkPodAutoscaler{}
	err := o.client.Get(context.TODO(), client.ObjectKey{Namespace: o.userNamespace, Name: o.userWPAName}, wpa)
	if err != nil && errors.IsNotFound(err) {
		return fmt.Errorf("WatermarkPodAutoscaler %s/%s not found", o.userNamespace, o.userWPAName)
	} else if err != nil {
		return fmt.Errorf("unable to get WatermarkPodAutoscaler, err: %w", err)
	}

	newWPA := wpa.DeepCopy()

	newWPA.Spec.DryRun = o.enabledDryRun

	patch := client.MergeFrom(wpa)
	if err = o.client.Patch(context.TODO(), newWPA, patch); err != nil {
		return fmt.Errorf("unable to set dry-run option to %v, err: %w", o.enabledDryRun, err)
	}

	fmt.Fprintf(o.Out, "WatermarkPodAutoscaler '%s/%s' dry-run option is now %v\n", o.userNamespace, o.userWPAName, o.enabledDryRun)

	return nil
}
