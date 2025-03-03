# Copyright (c) Microsoft Corporation.
# Licensed under the MIT license.

##@ Build Dependencies
## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

CONTROLLER_TOOLS_VERSION ?= v0.17.2
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary. If wrong version is installed, it will be overwritten.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=_output/crds
	cp _output/crds/kaito.sh_checkpoints.yaml charts/grit-manager/crds/
		cp _output/crds/kaito.sh_restores.yaml charts/grit-manager/crds/
	rm -rf _output/crds

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: verify-mod
verify-mod:
	@echo "verifying go.mod and go.sum"
	go mod tidy
	@if [ -n "$$(git status --porcelain go.mod go.sum)" ]; then \
		echo "Error: go.mod/go.sum is not up-to-date. please run `go mod tidy` and commit the changes."; \
		git diff go.mod go.sum; \
		exit 1; \
	fi

.PHONY: verify-manifests
verify-manifests: manifests
	@echo "verifying manifests"
	@if [ -n "$$(git status --porcelain ./charts/grit-manager/crds)" ]; then \
		echo "Error: manifests are not up-to-date. please run 'make manifests' and commit the changes."; \
		git diff ./charts/grit-manager/crds; \
		exit 1; \
	fi