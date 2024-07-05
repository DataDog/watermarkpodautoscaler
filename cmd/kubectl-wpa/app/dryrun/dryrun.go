// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package dryrun

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/cmd/kubectl-wpa/app/common"
)

var dryrunExample = `
	# %[1]s configure WPA dry-run option
	kubectl wpa %[1]s foo
`

var dryrunRevertExample = `
	# %[1]s reverts to a state specified in a file
	kubectl wpa dry-run list --all -ocsv > saved_state.csv
	kubectl wpa %[1]s -f saved_state.csv
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
	outputFormat  string
	csvFile       string
	enabledDryRun bool
	allWPA        bool
	allNamespaces bool
}

// newDryrunOptions provides an instance of GetOptions with default values.
func newDryrunOptions(streams genericclioptions.IOStreams) *dryrunOptions {
	o := &dryrunOptions{
		configFlags: genericclioptions.NewConfigFlags(false),

		IOStreams: streams,
	}

	return o
}

// NewCmdDryRun provides a cobra command wrapping dryrunOptions.
func NewCmdDryRun(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams)

	cmd := &cobra.Command{
		Use:          "dry-run",
		Short:        "configure WPA(s) dry-run",
		SilenceUsage: true,
	}

	cmd.AddCommand(newCmdDryRunEnabled(streams))
	cmd.AddCommand(newCmdDryRunDisabled(streams))
	cmd.AddCommand(newCmdRevert(streams))
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
		Example:      fmt.Sprintf(dryrunExample, "dry-run list"),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.complete(c, args); err != nil {
				return err
			}
			if err := o.validate(); err != nil {
				return err
			}

			return o.run(o.list)
		},
	}

	cmd.Flags().StringVarP(&o.labelSelector, "label-selector", "l", "", "Use to select WPA based in their labels")
	cmd.Flags().BoolVarP(&o.allWPA, "all", "", false, "Use select all existing WPA instances in a cluster")
	cmd.Flags().BoolVarP(&o.allNamespaces, "all-namespaces", "", false, "Use to search in all namespaces")
	cmd.Flags().StringVarP(&o.outputFormat, "output", "o", "", "use to change the default output formating (csv)")

	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// newCmdDryRunEnabled provides a cobra command wrapping dryrunOptions.
func newCmdDryRunEnabled(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams)
	o.enabledDryRun = true

	cmd := &cobra.Command{
		Use:          "enable [WatermarkPodAutoscaler name]",
		Short:        "enable WPA(s) dry-run mode",
		Example:      fmt.Sprintf(dryrunExample, "dry-run enable"),
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
	cmd.Flags().BoolVarP(&o.allWPA, "all", "", false, "Use select all existing WPA instances in a cluster")
	cmd.Flags().BoolVarP(&o.allNamespaces, "all-namespaces", "", false, "Use to search in all namespaces")
	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// newCmdDryRunDisabled provides a cobra command wrapping dryrunOptions.
func newCmdDryRunDisabled(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams)
	o.enabledDryRun = false

	cmd := &cobra.Command{
		Use:          "disable [WatermarkPodAutoscaler name]",
		Short:        "disable WPA(s) dry-run mode",
		Example:      fmt.Sprintf(dryrunExample, "dry-run disable"),
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
	cmd.Flags().BoolVarP(&o.allWPA, "all", "", false, "Use select all existing WPA instances in a cluster")
	cmd.Flags().BoolVarP(&o.allNamespaces, "all-namespaces", "", false, "Use to search in all namespaces")
	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// newCmdDryRunDisabled provides a cobra command wrapping dryrunOptions.
func newCmdRevert(streams genericclioptions.IOStreams) *cobra.Command {
	o := newDryrunOptions(streams)
	o.enabledDryRun = false

	cmd := &cobra.Command{
		Use:          "revert -f [saved_state_csv_file]",
		Short:        "revert all WPA instance dry-run configuration from a csv backup file",
		Example:      fmt.Sprintf(dryrunRevertExample, "dry-run revert"),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if o.csvFile == "" {
				return fmt.Errorf("the revert command requires a file as input use `--help` for an example")
			}
			if err := o.complete(c, args); err != nil {
				return err
			}
			return o.runRevert()
		},
	}

	cmd.Flags().StringVarP(&o.csvFile, "csv-file", "f", "", "read WPA dry-run configuration from csv file")
	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// complete sets all information required for processing the command.
func (o *dryrunOptions) complete(_ *cobra.Command, args []string) error {
	o.args = args
	var err error

	clientConfig := o.configFlags.ToRawKubeConfigLoader()
	// Create the Client for Read/Write operations.
	o.client, err = common.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("unable to instantiate client, err: %w", err)
	}

	// Get namespace from the client config or --all-namespaces
	o.userNamespace, _, err = clientConfig.Namespace()
	if err != nil {
		return err
	}

	if o.allNamespaces {
		o.userNamespace = ""
	}

	// get WPA name from argument
	if len(args) > 0 {
		o.userWPAName = args[0]
	}

	return nil
}

// validate ensures that all required arguments and flag values are provided.
func (o *dryrunOptions) validate() error {
	if o.userWPAName == "" && o.labelSelector == "" && !o.allWPA {
		return fmt.Errorf("the watermarkpodautoscaler name or label-selector is required")
	}

	return nil
}

func (o *dryrunOptions) list(wpas []v1alpha1.WatermarkPodAutoscaler) error {
	if len(wpas) == 0 {
		return fmt.Errorf("no matching WatermarkPodAutoscaler intance")
	}

	var writerFunc func(w io.Writer, wpa *v1alpha1.WatermarkPodAutoscaler)
	switch o.outputFormat {
	case "csv":
		csvWriter := csv.NewWriter(o.Out)
		writerFunc = func(w io.Writer, wpa *v1alpha1.WatermarkPodAutoscaler) {
			if err := csvWriter.Write([]string{wpa.Namespace, wpa.Name, dryRunString(wpa)}); err != nil {
				fmt.Fprintf(o.ErrOut, "error")
			}
		}
		defer csvWriter.Flush()
	default:
		writerFunc = func(w io.Writer, wpa *v1alpha1.WatermarkPodAutoscaler) {
			fmt.Fprintf(w, "WatermarkPodAutoscaler '%s/%s' dry-run option is: %v\n", wpa.Namespace, wpa.Name, wpa.Spec.DryRun)
		}
	}

	for _, wpa := range wpas {
		writerFunc(o.Out, &wpa)
	}
	return nil
}

func (o *dryrunOptions) patch(wpas []v1alpha1.WatermarkPodAutoscaler) error {
	if len(wpas) == 0 {
		return fmt.Errorf("no matching WatermarkPodAutoscaler intance")
	}

	for _, wpa := range wpas {
		err := patchWPA(o.client, &wpa, o.enabledDryRun)
		if err != nil {
			fmt.Fprintf(o.ErrOut, "error: %v", err)
		} else {
			fmt.Fprintf(o.Out, "WatermarkPodAutoscaler '%s/%s' dry-run option is now %v\n", wpa.Namespace, wpa.Name, o.enabledDryRun)
		}
	}
	return nil
}

// run used to run the command.
func (o *dryrunOptions) run(actionFunc func(wpas []v1alpha1.WatermarkPodAutoscaler) error) error {
	wpas := &v1alpha1.WatermarkPodAutoscalerList{}

	if o.userWPAName != "" {
		wpa, err := getWpa(o.client, o.userNamespace, o.userWPAName)
		if err != nil {
			return err
		}
		wpas.Items = append(wpas.Items, *wpa)
	} else {
		selector, err := labels.Parse(o.labelSelector)
		if err != nil {
			return fmt.Errorf("invalid label-selector, err: %w", err)
		}

		options := client.ListOptions{Namespace: o.userNamespace, LabelSelector: selector}
		err = o.client.List(context.TODO(), wpas, &options)
		if err != nil && k8serrors.IsNotFound(err) {
			return fmt.Errorf("WatermarkPodAutoscaler not found with namespace: %s, label-selector: %s", o.userNamespace, o.labelSelector)
		} else if err != nil {
			return fmt.Errorf("unable to get WatermarkPodAutoscaler, err: %w", err)
		}
	}

	return actionFunc(wpas.Items)
}

func (o *dryrunOptions) runRevert() error {
	input := o.In
	if o.csvFile != "" {
		f, err := os.Open(o.csvFile)
		if err != nil {
			return fmt.Errorf("unable to open the csv file %s, err: %w", o.csvFile, err)
		}
		defer func() {
			errClose := f.Close()
			if errClose != nil {
				fmt.Fprintf(o.ErrOut, "unable to close the file: %v", errClose)
			}
		}()
		input = f
	}

	csvReader := csv.NewReader(input)
	for {
		record, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fmt.Fprintf(o.ErrOut, "error: %v", err)
			continue
		}
		if len(record) != 3 {
			fmt.Fprintf(o.ErrOut, "invalid line: %v", record)
			continue
		}
		wpa, err := getWpa(o.client, record[0], record[1])
		if err != nil {
			fmt.Fprintf(o.ErrOut, "error: %v", err)
			continue
		}
		previousDryRunValue := o.dryRunBool(record[2])
		if wpa.Spec.DryRun == previousDryRunValue {
			continue
		}
		err = patchWPA(o.client, wpa, previousDryRunValue)
		if err != nil {
			fmt.Fprintf(o.ErrOut, "error: %v", err)
		} else {
			fmt.Fprintf(o.Out, "WatermarkPodAutoscaler '%s/%s' dry-run option is now %v\n", wpa.Namespace, wpa.Name, previousDryRunValue)
		}
	}

	return nil
}

func getWpa(k8sclient client.Client, ns, name string) (*v1alpha1.WatermarkPodAutoscaler, error) {
	wpa := &v1alpha1.WatermarkPodAutoscaler{}
	err := k8sclient.Get(context.TODO(), client.ObjectKey{Namespace: ns, Name: name}, wpa)
	if err != nil && k8serrors.IsNotFound(err) {
		return nil, fmt.Errorf("WatermarkPodAutoscaler %s/%s not found", ns, name)
	} else if err != nil {
		return nil, fmt.Errorf("unable to get WatermarkPodAutoscaler, err: %w", err)
	}
	return wpa, nil
}

func patchWPA(k8sclient client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, dryRun bool) error {
	newWPA := wpa.DeepCopy()
	newWPA.Spec.DryRun = dryRun

	patch := client.MergeFrom(wpa)
	if err := k8sclient.Patch(context.TODO(), newWPA, patch); err != nil {
		return fmt.Errorf("unable to set dry-run option to %v, err: %w", dryRun, err)
	}

	return nil
}

func dryRunString(wpa *v1alpha1.WatermarkPodAutoscaler) string {
	if wpa.Spec.DryRun {
		return enabledString
	}
	return disabledString
}

func (o *dryrunOptions) dryRunBool(input string) bool {
	switch input {
	case disabledString:
		return false
	case enabledString:
		return true
	default:
		fmt.Fprintf(o.ErrOut, "warning: Incorrect value for dry-run: %s, defaulting to true \n", input)
		return true
	}
}

const (
	disabledString = "disabled"
	enabledString  = "enabled"
)
