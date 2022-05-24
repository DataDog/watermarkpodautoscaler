//go:build tools
// +build tools

package tools

import (
	// Code generators built at runtime.
	_ "github.com/mikefarah/yq/v3"
	_ "k8s.io/code-generator/cmd/deepcopy-gen"
	_ "k8s.io/gengo/args"
	_ "k8s.io/kube-openapi/cmd/openapi-gen"
)
