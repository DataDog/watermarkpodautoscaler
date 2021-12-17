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
	"k8s.io/apimachinery/pkg/labels"
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
	labelSelector string
	enabledDryRun bool
}

// newDryrunOptions provides an instance of GetOptions with default values.
func newDryrunOptions(streams genericclioptions.IOStreams) *dryrunOptions {
	return &dryrunOptions{
		configFlags: genericclioptions.NewConfigFlags(false),

		IOStreams: streams,
	}
}

// NewCmdDryRun provides a cobra command wrapping dryrunOptions.
func NewCmdDryRun(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams)

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

			return nil
		},
	}

	cmd.AddCommand(newCmdDryRunEnabled(streams))
	cmd.AddCommand(newCmdDryRunDisabled(streams))
	cmd.AddCommand(newCmdDryRunList(streams))

	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// newCmdDryRunEnabled provides a cobra command wrapping dryrunOptions.
func newCmdDryRunList(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams)

	cmd := &cobra.Command{
		Use:          "list [WatermarkPodAutoscaler name]",
		Short:        "list dry-run mode of wpa(s)",
		Example:      fmt.Sprintf(dryrunExample, "dryrun"),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.complete(c, args); err != nil {
				return err
			}

			return o.run(o.list)
		},
	}

	cmd.Flags().StringVarP(&o.labelSelector, "label-selector", "l", "", "Use to select WPA based in their labels")
	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// newCmdDryRunEnabled provides a cobra command wrapping dryrunOptions.
func newCmdDryRunEnabled(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams)
	o.enabledDryRun = true

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

			return o.run(o.patch)
		},
	}

	cmd.Flags().StringVarP(&o.labelSelector, "label-selector", "l", "", "Use to select WPA based in their labels")
	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// newCmdDryRunDisabled provides a cobra command wrapping dryrunOptions.
func newCmdDryRunDisabled(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams)
	o.enabledDryRun = false

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

			return o.run(o.patch)
		},
	}

	cmd.Flags().StringVarP(&o.labelSelector, "label-selector", "l", "", "Use to select WPA based in their labels")
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

	if cmd.Flags().Lookup("namespace").Changed {
		ns, err2 := cmd.Flags().GetString("namespace")
		if err2 != nil {
			return err
		}
		o.userNamespace = ns
	}

	if len(args) > 0 {
		o.userWPAName = args[0]
	}

	return nil
}

// validate ensures that all required arguments and flag values are provided.
func (o *dryrunOptions) validate() error {
	if len(o.args) < 1 && o.labelSelector != "" {
		return fmt.Errorf("the watermarkpodautoscaler name or label-selector is required")
	}

	return nil
}

func (o *dryrunOptions) list(wpas []v1alpha1.WatermarkPodAutoscaler) error {
	for _, wpa := range wpas {
		fmt.Fprintf(o.Out, "WatermarkPodAutoscaler '%s/%s' dry-run option is: %v\n", wpa.Namespace, wpa.Name, wpa.Spec.DryRun)
	}
	return nil
}

func (o *dryrunOptions) patch(wpas []v1alpha1.WatermarkPodAutoscaler) error {
	for _, wpa := range wpas {
		newWPA := wpa.DeepCopy()
		newWPA.Spec.DryRun = o.enabledDryRun

		patch := client.MergeFrom(&wpa)
		if err := o.client.Patch(context.TODO(), newWPA, patch); err != nil {
			return fmt.Errorf("unable to set dry-run option to %v, err: %w", o.enabledDryRun, err)
		}

		fmt.Fprintf(o.Out, "WatermarkPodAutoscaler '%s/%s' dry-run option is now %v\n", wpa.Namespace, wpa.Name, o.enabledDryRun)
	}
	return nil
}

// run used to run the command.
func (o *dryrunOptions) run(actionFunc func(wpas []v1alpha1.WatermarkPodAutoscaler) error) error {
	wpas := &v1alpha1.WatermarkPodAutoscalerList{}

	if o.userWPAName != "" {
		wpa := &v1alpha1.WatermarkPodAutoscaler{}
		err := o.client.Get(context.TODO(), client.ObjectKey{Namespace: o.userNamespace, Name: o.userWPAName}, wpa)
		if err != nil && errors.IsNotFound(err) {
			return fmt.Errorf("WatermarkPodAutoscaler %s/%s not found", o.userNamespace, o.userWPAName)
		} else if err != nil {
			return fmt.Errorf("unable to get WatermarkPodAutoscaler, err: %w", err)
		}
		wpas.Items = append(wpas.Items, *wpa)
	} else {
		selector, err := labels.Parse(o.labelSelector)
		if err != nil {
			return fmt.Errorf("invalid label-selector, err: %w", err)
		}

		options := client.ListOptions{Namespace: o.userNamespace, LabelSelector: selector}
		err = o.client.List(context.TODO(), wpas, &options)
		if err != nil && errors.IsNotFound(err) {
			return fmt.Errorf("WatermarkPodAutoscaler not found with namespace: %s, label-selector: %s", o.userNamespace, o.labelSelector)
		} else if err != nil {
			return fmt.Errorf("unable to get WatermarkPodAutoscaler, err: %w", err)
		}
	}

	return actionFunc(wpas.Items)
}
