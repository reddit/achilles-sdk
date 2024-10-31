//go:build tools

package main

import (
	_ "github.com/clamoriniere/crd-to-markdown"
	_ "github.com/rinchsan/gosimports/cmd/gosimports"
	_ "honnef.co/go/tools/cmd/staticcheck"
	_ "sigs.k8s.io/controller-runtime/tools/setup-envtest"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
	_ "sigs.k8s.io/kustomize/kustomize/v4"
)
