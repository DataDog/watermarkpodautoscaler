// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package metriccheck

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
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
	"github.com/DataDog/datadog-api-client-go/v2/api/datadogV1"

	datadoghq "github.com/DataDog/datadog-operator/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/api/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/cmd/kubectl-wpa/app/common"
)

var metriccheckExample = `
	# %[1]s -n foo bar --site ap1
	kubectl wpa %[1]s -n foo bar --site ap1
`

var (
	siteMapping = map[string]string{
		"us1": "datadoghq.com",
		"ap1": "ap1.datadoghq.com",
		"eu1": "datadoghq.eu",
		"us5": "us5.datadoghq.com",
		"us3": "us3.datadoghq,com",
	}
)

// metricCheckOptions provides information required to manage WatermarkPodAutoscaler.
type metricCheckOptions struct {
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

	allWPA          bool
	allNamespaces   bool
	kubeClusterName string

	sites  []string
	APIKey string
	APPKey string
}

// newMetricCheckOptions provides an instance of GetOptions with default values.
func newMetricCheckOptions(streams genericclioptions.IOStreams) *metricCheckOptions {
	o := &metricCheckOptions{
		configFlags: genericclioptions.NewConfigFlags(false),

		siteContexts: make(map[string]context.Context),

		IOStreams: streams,
	}

	return o
}

// NewCmdMetricCheck provides a cobra command wrapping metricCheckOptions.
func NewCmdMetricCheck(streams genericclioptions.IOStreams) *cobra.Command {
	o := newMetricCheckOptions(streams)

	cmd := &cobra.Command{
		Use:          "metric-check",
		Short:        "Check the WPA(s) query validity on a a specific site",
		Example:      fmt.Sprintf(metriccheckExample, "metric-check"),
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
	cmd.Flags().StringVarP(&o.kubeClusterName, "placeholder-kube-cluster-name", "", "", "kube_cluster_name value")
	cmd.Flags().BoolVarP(&o.allWPA, "all", "", false, "Use select all existing WPA instances in a cluster")
	cmd.Flags().BoolVarP(&o.allNamespaces, "all-namespaces", "", false, "Use to search in all namespaces")
	cmd.Flags().BoolVarP(&o.verbose, "verbose", "", false, "verbose")

	cmd.Flags().StringSliceVarP(&o.sites, "site", "", []string{"us1"}, "Datadog site used to check the query")

	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// complete sets all information required for processing the command.
func (o *metricCheckOptions) complete(_ *cobra.Command, args []string) error {
	o.args = args
	var err error

	clientConfig := o.configFlags.ToRawKubeConfigLoader()
	// Create the Client for Read/Write operations.
	o.client, err = common.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("unable to instantiate client, err: %w", err)
	}

	o.ddClient = newDDClient()

	for _, site := range o.sites {
		apiKey := os.Getenv(fmt.Sprintf("%s_API_KEY", strings.ToUpper(site)))
		appKey := os.Getenv(fmt.Sprintf("%s_APP_KEY", strings.ToUpper(site)))

		contextSite := context.WithValue(
			context.Background(),
			datadog.ContextAPIKeys,
			map[string]datadog.APIKey{
				"apiKeyAuth": {
					Key: apiKey,
				},
				"appKeyAuth": {
					Key: appKey,
				},
			},
		)

		siteURL := site
		if val, found := siteMapping[site]; found {
			siteURL = val
		}

		o.siteContexts[site] = context.WithValue(
			contextSite,
			datadog.ContextServerVariables,
			map[string]string{
				"site": siteURL,
			},
		)
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

func newDDClient() *datadog.APIClient {
	configuration := datadog.NewConfiguration()
	return datadog.NewAPIClient(configuration)
}

// validate ensures that all required arguments and flag values are provided.
func (o *metricCheckOptions) validate() error {
	if o.userWPAName == "" && o.labelSelector == "" && !o.allWPA {
		return fmt.Errorf("the watermarkpodautoscaler name or label-selector is required")
	}

	return nil
}

type WpaResult struct {
	Name      string
	Namespace string
	Orphan    bool
	Metrics   []*MetricsResult
}

type MetricsResult struct {
	DatadogMetricsName string
	DatadogMetricsNS   string

	Query string
	Err   error

	Mismatch bool

	ValueBySite map[string]ValueResult
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

func (o *metricCheckOptions) list(wpas []v1alpha1.WatermarkPodAutoscaler) error {
	if len(wpas) == 0 {
		return fmt.Errorf("no matching WatermarkPodAutoscaler intance")
	}

	table := newGetTable(o.Out, o.sites)
	for id := range wpas {
		wpaResult, err := o.processWPA(&wpas[id])
		if err != nil {
			return err
		}
		wpaCommonData := []string{wpaResult.Namespace, wpaResult.Name, BoolToString(wpaResult.Orphan)}

		for _, metric := range wpaResult.Metrics {
			data := wpaCommonData
			data = append(data, errorString(metric.Err))

			data = append(data, metric.Name())
			if len(o.sites) > 1 {
				data = append(data, BoolToString(!metric.Mismatch))
			}
			for _, site := range o.sites {
				valueResult := metric.ValueBySite[site]
				data = append(data, valueResult.Status)
				data = append(data, toString(valueResult.Value))
				data = append(data, GetTimestamp(valueResult.Timestamp))
				data = append(data, errorString(valueResult.Err))
			}
			table.Append(data)
		}
	}

	table.Render()
	return nil
}

// run used to run the command.
func (o *metricCheckOptions) run(actionFunc func(wpas []v1alpha1.WatermarkPodAutoscaler) error) error {
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

func (o *metricCheckOptions) processWPA(wpa *v1alpha1.WatermarkPodAutoscaler) (*WpaResult, error) {
	result := &WpaResult{
		Name:      wpa.GetName(),
		Namespace: wpa.GetNamespace(),
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
			newMetricsResult.Query = dda.Spec.Query
		} else {
			// todo add pre-post query operation + replace placeholder
			newMetricsResult.Query = metric.External.MetricName
			// TODO for now continue
			continue
		}

		newMetricsResult.ValueBySite = map[string]ValueResult{}

		statusComparison := map[string]string{}

		for _, site := range o.sites {
			valueResult := ValueResult{}
			replaceQuery := strings.ReplaceAll(newMetricsResult.Query, "%%tag_kube_cluster_name%%", o.kubeClusterName)
			api := datadogV1.NewMetricsApi(o.ddClient)
			resp, r, err := api.QueryMetrics(o.siteContexts[site], time.Now().AddDate(0, 0, -1).Unix(), time.Now().Unix(), replaceQuery)

			if err != nil {
				valueResult.Err = err
				valueResult.ErrInfo = fmt.Sprintf("Full HTTP response: %v\n", r)
				valueResult.Status = fmt.Sprintf("error: HTTP %d", r.StatusCode)
			}
			defer func() { _ = r.Body.Close() }()

			if r.StatusCode >= http.StatusBadRequest {
				valueResult.Status = fmt.Sprintf("error: HTTP %d", r.StatusCode)
			}

			if valueResult.Status == "" {
				valueResult.Status = resp.GetStatus()
			}

			if valueResult.Status == "ok" {
				if len(resp.Series) > 0 {
					lastPoint := resp.Series[0].Pointlist[len(resp.Series[0].Pointlist)-1]
					if len(lastPoint) == 2 {
						if lastPoint[1] != nil {
							valueResult.Value = *lastPoint[1]
						}
						if lastPoint[0] != nil {
							valueResult.Timestamp = *lastPoint[0]
						}
					}
				}
			}

			previousVal, found := statusComparison[site]
			if !found {
				statusComparison[site] = valueResult.Status
			} else if previousVal != valueResult.Status {
				newMetricsResult.Mismatch = true
			}

			newMetricsResult.ValueBySite[site] = valueResult
		}

		if o.verbose {
			fmt.Fprintf(o.ErrOut, "- WatermarkPodAutoscaler '%s/%s' orphan:%v, match:%v \n", wpa.Namespace, wpa.Name, result.Orphan, !newMetricsResult.Mismatch)
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

func newGetTable(out io.Writer, sites []string) *tablewriter.Table {
	table := tablewriter.NewWriter(out)
	headers := []string{"Namespace", "Name", "Orphan", "Err", "M.Name"}
	if len(sites) > 1 {
		headers = append(headers, "all-match")
	}
	for _, site := range sites {
		shortSite := strings.Split(site, ".")[0]
		headers = append(headers, fmt.Sprintf("[%s]", shortSite))
		headers = append(headers, fmt.Sprintf("[%s] Value", shortSite))
		headers = append(headers, fmt.Sprintf("[%s] TS", shortSite))
		headers = append(headers, fmt.Sprintf("[%s] Err", shortSite))
	}

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

func toString(i any) string {
	return fmt.Sprintf("%v", i)
}

func errorString(err error) string {
	if err != nil {
		return err.Error()
	}
	return "nil"
}

func BoolToString(v bool) string {
	return fmt.Sprintf("%v", v)
}

func GetTimestamp(ts float64) string {
	return time.Unix(int64(ts), 0).Format(time.Kitchen)
}
