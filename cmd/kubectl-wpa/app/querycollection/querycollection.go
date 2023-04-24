// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package querycollection

import (
	"context"
	"encoding/csv"
	goerrors "errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/DataDog/datadog-api-client-go/v2/api/datadog"

	datadoghq "github.com/DataDog/datadog-operator/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/cmd/kubectl-wpa/app/common"
)

var querycollectionExample = `
	# %[1]s -n foo bar
	kubectl wpa %[1]s -n foo bar
`

const (
	// DefaultOutputFormat is the default output format.
	OutputFormatTable   = "table"
	OutputFormatCSV     = "csv"
	DefaultOutputFormat = OutputFormatTable
)

var headers = []string{"Namespace", "Name", "Orphan", "Team", "Service", "KubeContext", "TargetRefKind", "TargetRefNS", "TargetRefName", "Query"}

// queryCollectionOptions provides information required to manage WatermarkPodAutoscaler.
type queryCollectionOptions struct {
	configFlags *genericclioptions.ConfigFlags
	args        []string

	client       client.Client
	ddClient     *datadog.APIClient
	siteContexts map[string]context.Context

	genericclioptions.IOStreams

	userNamespace string
	userWPAName   string
	labelSelector string
	verbose       bool

	allWPA             bool
	allNamespaces      bool
	kubeClusterName    string
	currentContextName string
	outputFormat       string
}

// newMetricCheckOptions provides an instance of GetOptions with default values.
func newMetricCheckOptions(streams genericclioptions.IOStreams) *queryCollectionOptions {
	o := &queryCollectionOptions{
		configFlags: genericclioptions.NewConfigFlags(false),

		siteContexts: make(map[string]context.Context),

		IOStreams: streams,
	}

	return o
}

// NewCmdQueryCollectionCheck provides a cobra command wrapping queryCollectionOptions.
func NewCmdQueryCollectionCheck(streams genericclioptions.IOStreams) *cobra.Command {
	o := newMetricCheckOptions(streams)

	cmd := &cobra.Command{
		Use:          "metric-query",
		Short:        "Report all metrics query used by each WPA",
		Example:      fmt.Sprintf(querycollectionExample, "metric-query"),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.complete(c, args); err != nil {
				return err
			}
			if err := o.validate(); err != nil {
				return err
			}
			switch o.outputFormat {
			case OutputFormatTable:
				return o.run(o.listTable)
			case OutputFormatCSV:
				return o.run(o.listCSV)
			default:
				return fmt.Errorf("invalid output format: %s", o.outputFormat)
			}
		},
	}

	cmd.Flags().StringVarP(&o.labelSelector, "label-selector", "l", "", "Use to select WPA based in their labels")
	cmd.Flags().StringVarP(&o.kubeClusterName, "placeholder-kube-cluster-name", "", "", "kube_cluster_name value")
	cmd.Flags().BoolVarP(&o.allWPA, "all", "", false, "Use select all existing WPA instances in a cluster")
	cmd.Flags().BoolVarP(&o.allNamespaces, "all-namespaces", "", false, "Use to search in all namespaces")
	cmd.Flags().BoolVarP(&o.verbose, "verbose", "v", false, "verbose")

	cmd.Flags().StringVarP(&o.outputFormat, "output", "o", DefaultOutputFormat, "Use to select output format (table, csv)")

	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// complete sets all information required for processing the command.
func (o *queryCollectionOptions) complete(_ *cobra.Command, args []string) error {
	o.args = args
	var err error

	clientConfig := o.configFlags.ToRawKubeConfigLoader()
	config, _ := clientConfig.RawConfig()
	o.currentContextName = config.CurrentContext
	// Create the Client for Read/Write operations.
	o.client, err = common.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("unable to instantiate client, err: %w", err)
	}

	o.ddClient = newDDClient()

	// Get namespace from the client config or --all-namespaces
	o.userNamespace, _, err = clientConfig.Namespace()
	if err != nil {
		return err
	}

	if o.allNamespaces {
		o.userNamespace = ""
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

func newDDClient() *datadog.APIClient {
	configuration := datadog.NewConfiguration()
	return datadog.NewAPIClient(configuration)
}

// validate ensures that all required arguments and flag values are provided.
func (o *queryCollectionOptions) validate() error {
	if o.userWPAName == "" && o.labelSelector == "" && !o.allWPA {
		return fmt.Errorf("the watermarkpodautoscaler name or label-selector is required")
	}

	return nil
}

type WpaResult struct {
	Name      string
	Namespace string
	Team      string
	Service   string
	Orphan    bool
	TargetRef v1alpha1.CrossVersionObjectReference
	Metrics   []*MetricsResult
}

type MetricsResult struct {
	DatadogMetricsName string
	DatadogMetricsNS   string

	Query string
	Err   error
}

func (m *MetricsResult) Name() string {
	if m.DatadogMetricsName != "" {
		return fmt.Sprintf("dda@%s/%s", m.DatadogMetricsNS, m.DatadogMetricsName)
	}
	return fmt.Sprintf("query@%s", m.Query)
}

type ValueResult struct {
	Status  string
	Err     error
	ErrInfo string

	Value     float64
	Timestamp float64
}

func (o *queryCollectionOptions) listTable(wpas []v1alpha1.WatermarkPodAutoscaler) error {
	if len(wpas) == 0 {
		return fmt.Errorf("no matching WatermarkPodAutoscaler intance")
	}

	table := newGetTable(o.Out)
	writeFunc := func(data []string) error {
		table.Append(data)
		return nil
	}
	var errs []error
	for id := range wpas {
		if err := o.generateRaw(&wpas[id], writeFunc); err != nil {
			errs = append(errs, err)
		}
	}

	table.Render()
	return goerrors.Join(errs...)
}

func (o *queryCollectionOptions) listCSV(wpas []v1alpha1.WatermarkPodAutoscaler) error {
	if len(wpas) == 0 {
		return fmt.Errorf("no matching WatermarkPodAutoscaler intance")
	}
	writer := csv.NewWriter(o.Out)
	defer writer.Flush()

	var errs []error
	// add header
	if err := writer.Write(headers); err != nil {
		return fmt.Errorf("unable to write header, err: %w", err)
	}
	for id := range wpas {
		if err := o.generateRaw(&wpas[id], writer.Write); err != nil {
			errs = append(errs, err)
		}
		writer.Flush()
	}

	return goerrors.Join(errs...)
}

func (o *queryCollectionOptions) generateRaw(wpa *v1alpha1.WatermarkPodAutoscaler, writeRawFunc func([]string) error) error {
	wpaResult, err := o.processWPA(wpa)
	if err != nil {
		return err
	}
	wpaCommonData := []string{wpaResult.Namespace, wpaResult.Name, BoolToString(wpaResult.Orphan), wpaResult.Team, wpaResult.Service}

	wpaCommonData = append(wpaCommonData, o.currentContextName)
	wpaCommonData = append(wpaCommonData, wpaResult.TargetRef.Kind)
	wpaCommonData = append(wpaCommonData, wpaResult.Namespace)
	wpaCommonData = append(wpaCommonData, wpaResult.TargetRef.Name)

	var errors []error
	for _, metric := range wpaResult.Metrics {
		data := wpaCommonData
		data = append(data, metric.Query)
		if err := writeRawFunc(data); err != nil {
			errors = append(errors, err)
		}
	}
	return goerrors.Join(errors...)
}

// run used to run the command.
func (o *queryCollectionOptions) run(actionFunc func(wpas []v1alpha1.WatermarkPodAutoscaler) error) error {
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

func queryClearner(query string) string {
	return strings.NewReplacer("\n", "", "\r", "").Replace(query)
}

func (o *queryCollectionOptions) processWPA(wpa *v1alpha1.WatermarkPodAutoscaler) (*WpaResult, error) {
	result := &WpaResult{
		Name:      wpa.GetName(),
		Namespace: wpa.GetNamespace(),
		TargetRef: wpa.Spec.ScaleTargetRef,
	}

	if wpa.GetLabels() != nil {
		if team, ok := wpa.GetLabels()["team"]; ok {
			result.Team = team
		}
		if service, ok := wpa.GetLabels()["service"]; ok {
			result.Service = service
		}
	}

	for _, condition := range wpa.Status.Conditions {
		status := true
		if condition.Status == v1.ConditionFalse {
			status = false
		}
		switch condition.Type {
		case v1alpha1.ConditionReasonSuccessfulGetScale:
			result.Orphan = status
		}
	}

	for _, metric := range wpa.Spec.Metrics {
		newMetricsResult := &MetricsResult{}
		result.Metrics = append(result.Metrics, newMetricsResult)
		if strings.HasPrefix(metric.External.MetricName, "datadogmetric@") {
			metricNsName := strings.Split(strings.TrimPrefix(metric.External.MetricName, "datadogmetric@"), ":")
			if len(metricNsName) != 2 {
				return nil, fmt.Errorf("wrong size")
			}
			newMetricsResult.DatadogMetricsNS = metricNsName[0]
			newMetricsResult.DatadogMetricsName = metricNsName[1]

			dda, err := getDatadogMetric(o.client, newMetricsResult.DatadogMetricsNS, newMetricsResult.DatadogMetricsName)
			if err != nil {
				newMetricsResult.Err = err
				continue
			}
			newMetricsResult.Query = queryClearner(dda.Spec.Query)
		} else {
			// todo add pre-post query operation + replace placeholder
			newMetricsResult.Query = queryClearner(metric.External.MetricName)
			// TODO for now continue
			continue
		}

		if o.verbose {
			fmt.Fprintf(o.ErrOut, "- WatermarkPodAutoscaler '%s/%s' orphan:%v\n", wpa.Namespace, wpa.Name, result.Orphan)
		}
	}
	return result, nil
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

func getDatadogMetric(k8sclient client.Client, ns, name string) (*datadoghq.DatadogMetric, error) {
	dda := &datadoghq.DatadogMetric{}
	err := k8sclient.Get(context.TODO(), client.ObjectKey{Namespace: ns, Name: name}, dda)
	if err != nil && k8serrors.IsNotFound(err) {
		return nil, fmt.Errorf("DatadogMetric %s/%s not found", ns, name)
	} else if err != nil {
		return nil, fmt.Errorf("unable to get DatadogMetric, err: %w", err)
	}
	return dda, nil
}

func newGetTable(out io.Writer) *tablewriter.Table {
	table := tablewriter.NewWriter(out)

	table.SetHeader(headers)
	table.SetBorders(tablewriter.Border{Left: false, Top: false, Right: false, Bottom: false})
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(false)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetHeaderLine(false)

	return table
}

func BoolToString(v bool) string {
	return fmt.Sprintf("%v", v)
}

func GetTimestamp(ts float64) string {
	return time.Unix(int64(ts), 0).Format(time.Kitchen)
}
