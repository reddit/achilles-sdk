SHELL:=/bin/bash

PWD := $(PWD)
CONTROLLER_GEN := $(PWD)/bin/controller-gen
CONTROLLER_GEN_CMD := $(CONTROLLER_GEN)
GOSIMPORTS := $(PWD)/bin/gosimports
GOSIMPORTS_CMD := $(GOSIMPORTS)
STATICCHECK := $(PWD)/bin/staticcheck
STATICCHECK_CMD := $(STATICCHECK)

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.29
ENVTEST := $(PWD)/bin/setup-envtest
ENVTEST_CMD := $(ENVTEST)

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go install -modfile=tools/go.mod $(2) ;\
}
endef

.PHONY: test-manifests
test-manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./pkg/internal/tests/api/..." output:crd:artifacts:config=pkg/internal/tests/cluster/crd/bases
	$(CONTROLLER_GEN) object paths="./pkg/internal/tests/api/..."

.PHONY: generate
generate: test-manifests $(GOSIMPORTS)
	go generate ./...
	$(GOSIMPORTS_CMD) -local github.com/reddit/achilles-sdk -l -w .

KUBEBUILDER_ASSETS = $(shell $(ENVTEST_CMD) --arch=amd64 use $(ENVTEST_K8S_VERSION) -p path)
.PHONY: test
test: $(ENVTEST) test-manifests
	KUBEBUILDER_ASSETS="$(KUBEBUILDER_ASSETS)" go test -race ./...

.PHONY: lint
lint: $(STATICCHECK) $(GOSIMPORTS)
	cd tools && go mod tidy
	go mod tidy
	go fmt ./...
	go list ./... | grep -v encoding/json | xargs go vet # ignore forked encoding/json pkg
	go list ./... | grep -v encoding/json | xargs $(STATICCHECK_CMD) # ignore forked encoding/json pkg
	$(GOSIMPORTS_CMD) -local github.com/reddit/achilles-sdk -l -w .

$(CONTROLLER_GEN):
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen)

$(KUSTOMIZE):
	$(call go-get-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v4)

$(GOSIMPORTS):
	$(call go-get-tool,$(GOSIMPORTS),github.com/rinchsan/gosimports/cmd/gosimports)

$(STATICCHECK):
	$(call go-get-tool,$(STATICCHECK),honnef.co/go/tools/cmd/staticcheck)

$(ENVTEST):
	$(call go-get-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest)
