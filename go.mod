module github.com/DataDog/watermarkpodautoscaler

go 1.16

require (
	github.com/Microsoft/go-winio v0.5.1 // indirect
	github.com/go-logr/logr v0.3.0
	github.com/go-openapi/spec v0.19.3
	github.com/mikefarah/yq/v3 v3.0.0-20200615114226-086f0ec6b9aa
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/prometheus/client_golang v1.7.1
	github.com/stretchr/testify v1.7.0
	github.com/spf13/cobra v1.1.1
	github.com/spf13/pflag v1.0.5
	go.uber.org/zap v1.15.0
	gopkg.in/DataDog/dd-trace-go.v1 v1.34.0
	k8s.io/api v0.20.11
	k8s.io/apimachinery v0.20.11
	k8s.io/cli-runtime v0.22.4
	k8s.io/client-go v0.20.11
	k8s.io/code-generator v0.20.11
	k8s.io/controller-manager v0.20.11
	k8s.io/gengo v0.0.0-20201113003025-83324d819ded
	k8s.io/heapster v1.5.4
	k8s.io/klog/v2 v2.4.0
	k8s.io/kube-aggregator v0.20.11
	k8s.io/kube-openapi v0.0.0-20201113171705-d219536bb9fd
	k8s.io/metrics v0.20.11
	sigs.k8s.io/controller-runtime v0.7.2
)

// Pinned to kubernetes-v0.20.11
replace (
	k8s.io/api => k8s.io/api v0.20.11
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.20.11
	k8s.io/apimachinery => k8s.io/apimachinery v0.20.11
	k8s.io/apiserver => k8s.io/apiserver v0.20.11
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.20.11
	k8s.io/client-go => k8s.io/client-go v0.20.11
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.20.11
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.20.11
	k8s.io/code-generator => k8s.io/code-generator v0.20.11
	k8s.io/component-base => k8s.io/component-base v0.20.11
	k8s.io/component-helpers => k8s.io/component-helpers v0.20.11
	k8s.io/controller-manager => k8s.io/controller-manager v0.20.11
	k8s.io/cri-api => k8s.io/cri-api v0.20.11
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.20.11
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.20.11
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.20.11
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.20.11
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.20.11
	k8s.io/kubectl => k8s.io/kubectl v0.20.11
	k8s.io/kubelet => k8s.io/kubelet v0.20.11
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.20.11
	k8s.io/metrics => k8s.io/metrics v0.20.11
	k8s.io/mount-utils => k8s.io/mount-utils v0.20.3-rc.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.20.11
	k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.20.11
	k8s.io/sample-controller => k8s.io/sample-controller v0.20.11
)
