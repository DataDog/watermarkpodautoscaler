module github.com/DataDog/watermarkpodautoscaler

go 1.16

require (
	github.com/Microsoft/go-winio v0.5.1 // indirect
	github.com/go-logr/logr v1.2.0
	github.com/mikefarah/yq/v3 v3.0.0-20200615114226-086f0ec6b9aa
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.17.0
	github.com/philhofer/fwd v1.1.1 // indirect
	github.com/prometheus/client_golang v1.11.0
	github.com/spf13/cobra v1.2.1
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.7.0
	go.uber.org/zap v1.19.1
	gopkg.in/DataDog/dd-trace-go.v1 v1.34.0
	k8s.io/api v0.23.5
	k8s.io/apimachinery v0.23.5
	k8s.io/cli-runtime v0.22.4
	k8s.io/client-go v0.23.5
	k8s.io/code-generator v0.23.5
	k8s.io/controller-manager v0.23.5
	k8s.io/gengo v0.0.0-20210813121822-485abfe95c7c
	k8s.io/heapster v1.5.4
	k8s.io/klog/v2 v2.30.0
	k8s.io/kube-aggregator v0.23.5
	k8s.io/kube-openapi v0.0.0-20220124234850-424119656bbf
	k8s.io/metrics v0.23.5
	sigs.k8s.io/controller-runtime v0.11.2
)

// Pinned to kubernetes-v0.23.5
replace (
	k8s.io/api => k8s.io/api v0.23.5
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.23.5
	k8s.io/apimachinery => k8s.io/apimachinery v0.23.5
	k8s.io/apiserver => k8s.io/apiserver v0.23.5
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.23.5
	k8s.io/client-go => k8s.io/client-go v0.23.5
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.23.5
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.23.5
	k8s.io/code-generator => k8s.io/code-generator v0.23.5
	k8s.io/component-base => k8s.io/component-base v0.23.5
	k8s.io/component-helpers => k8s.io/component-helpers v0.23.5
	k8s.io/controller-manager => k8s.io/controller-manager v0.23.5
	k8s.io/cri-api => k8s.io/cri-api v0.23.5
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.23.5
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.23.5
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.23.5
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.23.5
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.23.5
	k8s.io/kubectl => k8s.io/kubectl v0.23.5
	k8s.io/kubelet => k8s.io/kubelet v0.23.5
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.23.5
	k8s.io/metrics => k8s.io/metrics v0.23.5
	k8s.io/mount-utils => k8s.io/mount-utils v0.20.3-rc.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.23.5
	k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.23.5
	k8s.io/sample-controller => k8s.io/sample-controller v0.23.5
)
