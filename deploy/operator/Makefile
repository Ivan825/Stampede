GO              ?= go
IMG             ?= ghcr.io/ivan825/stampede-operator:dev
CONTROLLER_GEN  := sigs.k8s.io/controller-tools/cmd/controller-gen@v0.22.0
SETUP_ENVTEST   := sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.25
ENVTEST_K8S     ?= 1.37.x
ENVTEST_DIR     ?= $(CURDIR)/bin/envtest

.PHONY: generate manifests test envtest build docker-build install deploy fmt vet

## generate: deepcopy functions for the API types
generate:
	$(GO) run $(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths=./api/...

## manifests: CRDs and the manager ClusterRole
manifests:
	$(GO) run $(CONTROLLER_GEN) crd rbac:roleName=manager-role paths=./... \
		output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac

## envtest: download kube-apiserver and etcd for the controller tests
envtest:
	@$(GO) run $(SETUP_ENVTEST) use $(ENVTEST_K8S) --bin-dir $(ENVTEST_DIR) -p path

## test: unit and envtest controller tests
test:
	KUBEBUILDER_ASSETS="$$($(GO) run $(SETUP_ENVTEST) use $(ENVTEST_K8S) --bin-dir $(ENVTEST_DIR) -p path)" \
		$(GO) test -count=1 $(TESTFLAGS) ./...

fmt:
	gofmt -s -w .

vet:
	$(GO) vet ./...

build:
	CGO_ENABLED=0 $(GO) build -trimpath -o bin/manager ./cmd

docker-build:
	docker build -t $(IMG) .

## install: CRDs only
install:
	kubectl apply -k config/crd

## deploy: CRDs, RBAC and the manager in stampede-system
deploy:
	kubectl apply -k config/default
