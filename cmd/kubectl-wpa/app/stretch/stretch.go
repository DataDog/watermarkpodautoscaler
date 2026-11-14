// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package stretch

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/cmd/kubectl-wpa/app/common"
)

var statusExample = `
	# show the target stretch state of every WPA in the cluster
	kubectl wpa stretch status --all-namespaces

	# show one WPA
	kubectl wpa stretch status foo
`

type statusOptions struct {
	genericclioptions.IOStreams

	configFlags *genericclioptions.ConfigFlags
	client      client.Client

	userNamespace string
	userWPAName   string
	labelSelector string
	outputFormat  string
	allNamespaces bool
}

// NewCmdStretch provides the "stretch" command.
func NewCmdStretch(streams genericclioptions.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "stretch",
		Short:        "inspect the cluster-wide target stretch",
		SilenceUsage: true,
	}
	cmd.AddCommand(newCmdStatus(streams))
	return cmd
}

func newCmdStatus(streams genericclioptions.IOStreams) *cobra.Command {
	o := &statusOptions{configFlags: genericclioptions.NewConfigFlags(false), IOStreams: streams}
	cmd := &cobra.Command{
		Use:          "status [WatermarkPodAutoscaler name]",
		Short:        "show the target stretch state of WPA(s)",
		Example:      statusExample,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.complete(args); err != nil {
				return err
			}
			wpas, err := o.list(context.TODO())
			if err != nil {
				return err
			}
			if o.outputFormat == "csv" {
				return writeCSV(o.Out, wpas)
			}
			return writeTable(o.Out, wpas)
		},
	}
	cmd.Flags().StringVarP(&o.labelSelector, "label-selector", "l", "", "Use to select WPA based in their labels")
	cmd.Flags().BoolVarP(&o.allNamespaces, "all-namespaces", "", false, "Use to search in all namespaces")
	cmd.Flags().StringVarP(&o.outputFormat, "output", "o", "", "use to change the default output formating (csv)")
	o.configFlags.AddFlags(cmd.Flags())
	return cmd
}

func (o *statusOptions) complete(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("at most one WPA name can be given")
	}
	if len(args) == 1 {
		o.userWPAName = args[0]
	}
	if o.outputFormat != "" && o.outputFormat != "csv" {
		return fmt.Errorf("unsupported output format %q", o.outputFormat)
	}
	clientConfig := o.configFlags.ToRawKubeConfigLoader()
	var err error
	if o.client, err = common.NewClient(clientConfig); err != nil {
		return err
	}
	o.userNamespace, _, err = clientConfig.Namespace()
	if err != nil {
		return err
	}
	if o.allNamespaces {
		o.userNamespace = ""
	}
	return nil
}

func (o *statusOptions) list(ctx context.Context) ([]v1alpha1.WatermarkPodAutoscaler, error) {
	if o.userWPAName != "" {
		wpa := &v1alpha1.WatermarkPodAutoscaler{}
		if err := o.client.Get(ctx, types.NamespacedName{Namespace: o.userNamespace, Name: o.userWPAName}, wpa); err != nil {
			return nil, err
		}
		return []v1alpha1.WatermarkPodAutoscaler{*wpa}, nil
	}
	selector, err := labels.Parse(o.labelSelector)
	if err != nil {
		return nil, err
	}
	list := &v1alpha1.WatermarkPodAutoscalerList{}
	if err := o.client.List(ctx, list, &client.ListOptions{Namespace: o.userNamespace, LabelSelector: selector}); err != nil {
		return nil, err
	}
	return list.Items, nil
}

var header = []string{"NAMESPACE", "NAME", "ENABLED-ANNOTATION", "MAX-RATIO", "MODE", "APPLIED-RATIO", "CONDITION", "REPLICAS", "SHADOW-REPLICAS"}

func rows(wpas []v1alpha1.WatermarkPodAutoscaler) ([][]string, map[string]int) {
	sort.Slice(wpas, func(i, j int) bool {
		if wpas[i].Namespace != wpas[j].Namespace {
			return wpas[i].Namespace < wpas[j].Namespace
		}
		return wpas[i].Name < wpas[j].Name
	})
	counts := map[string]int{}
	out := make([][]string, 0, len(wpas))
	for i := range wpas {
		w := &wpas[i]
		orDash := func(s string) string {
			if s == "" {
				return "-"
			}
			return s
		}
		mode, ratio, shadow := "-", "-", "-"
		if ts := w.Status.TargetStretch; ts != nil {
			mode, ratio = ts.Mode, ts.AppliedRatio
			if ts.ReplicasRecommended != nil {
				shadow = fmt.Sprintf("%d", *ts.ReplicasRecommended)
			}
		}
		cond, reason := "-", "None"
		for _, c := range w.Status.Conditions {
			if c.Type == v1alpha1.WatermarkPodAutoscalerStatusTargetStretched {
				cond, reason = fmt.Sprintf("%s/%s", c.Status, c.Reason), c.Reason
			}
		}
		counts[reason]++
		out = append(out, []string{
			w.Namespace, w.Name,
			orDash(w.Annotations[v1alpha1.TargetStretchEnabledAnnotationKey]),
			orDash(w.Annotations[v1alpha1.TargetStretchMaxRatioAnnotationKey]),
			mode, ratio, cond,
			fmt.Sprintf("%d/%d", w.Status.CurrentReplicas, w.Status.DesiredReplicas),
			shadow,
		})
	}
	return out, counts
}

func writeTable(w io.Writer, wpas []v1alpha1.WatermarkPodAutoscaler) error {
	data, counts := rows(wpas)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, r := range append([][]string{header}, data...) {
		for i, c := range r {
			if i > 0 {
				fmt.Fprint(tw, "\t")
			}
			fmt.Fprint(tw, c)
		}
		fmt.Fprint(tw, "\n")
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	reasons := make([]string, 0, len(counts))
	for k := range counts {
		reasons = append(reasons, k)
	}
	sort.Strings(reasons)
	fmt.Fprintf(w, "\n%d WPA(s):", len(data))
	for _, k := range reasons {
		fmt.Fprintf(w, " %s=%d", k, counts[k])
	}
	fmt.Fprint(w, "\n")
	return nil
}

func writeCSV(w io.Writer, wpas []v1alpha1.WatermarkPodAutoscaler) error {
	data, _ := rows(wpas)
	cw := csv.NewWriter(w)
	if err := cw.WriteAll(append([][]string{header}, data...)); err != nil {
		return err
	}
	return cw.Error()
}
