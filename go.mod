module github.com/DataDog/watermarkpodautoscaler

require (
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/go-logr/logr v0.1.0
	github.com/operator-framework/operator-sdk v0.8.2-0.20190522220659-031d71ef8154
	github.com/prometheus/client_golang v0.9.3-0.20190127221311-3c4408c8b829
	github.com/spf13/pflag v1.0.3
	gopkg.in/zorkian/go-datadog-api.v2 v2.21.0 // indirect
	k8s.io/api v0.0.0-20190624085159-95846d7ef82a
	k8s.io/apimachinery v0.0.0-20190624085041-961b39a1baa0
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/code-generator v0.0.0-20190620073620-d55040311883
	k8s.io/gengo v0.0.0-20190327210449-e17681d19d3a
	k8s.io/heapster v1.5.4 // indirect
	k8s.io/kube-openapi v0.0.0-20190320154901-5e45bb682580
	k8s.io/kubernetes v1.13.1
	k8s.io/metrics v0.0.0-20181213153603-64084e52e000
	sigs.k8s.io/controller-runtime v0.1.10
	sigs.k8s.io/controller-tools v0.1.10
)

// Pinned to kubernetes-1.13.1
replace (
	k8s.io/api => k8s.io/api v0.0.0-20181213150558-05914d821849
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20181213153335-0fe22c71c476
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20181127025237-2b1284ed4c93
	k8s.io/client-go => k8s.io/client-go v0.0.0-20181213151034-8d9ed539ba31
)

replace (
	github.com/coreos/prometheus-operator => github.com/coreos/prometheus-operator v0.29.0
	github.com/operator-framework/operator-sdk => github.com/operator-framework/operator-sdk v0.9.0
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20181117043124-c2090bec4d9b
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20180711000925-0cf8f7e6ed1d
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.1.10
	sigs.k8s.io/controller-tools => sigs.k8s.io/controller-tools v0.1.11-0.20190411181648-9d55346c2bde
)
