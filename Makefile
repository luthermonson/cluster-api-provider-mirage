# cluster-api-provider-mirage — a FAKE Cluster API infrastructure/bootstrap/
# control-plane provider for exercising the full CAPI cluster lifecycle with no
# real infrastructure.

IMG ?= ghcr.io/luthermonson/cluster-api-provider-mirage:dev
KIND_CLUSTER ?= mirage-dev
CAPI_VERSION ?= v1.14.4

CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0

.PHONY: all
all: generate manifests build

##@ Development

.PHONY: generate
generate: ## Generate DeepCopy methods.
	$(CONTROLLER_GEN) object paths=./api/...

.PHONY: manifests
manifests: ## Generate CRDs and RBAC.
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:artifacts:config=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=mirage-manager-role paths=./internal/... output:rbac:artifacts:config=config/rbac

.PHONY: build
build: ## Build the manager binary.
	go build -o bin/manager ./cmd

.PHONY: test
test: ## Run unit tests.
	go test ./... -count=1

.PHONY: vet
vet:
	go vet ./...

##@ Images

.PHONY: docker-build
docker-build: ## Build the manager image.
	docker build -t $(IMG) .

.PHONY: kind-load
kind-load: docker-build ## Load the manager image into the kind dev cluster.
	kind load docker-image $(IMG) --name $(KIND_CLUSTER)

##@ Dev cluster (throwaway)

.PHONY: kind-up
kind-up: ## Create the throwaway kind cluster.
	kind create cluster --name $(KIND_CLUSTER) --wait 120s

.PHONY: kind-down
kind-down: ## Delete the throwaway kind cluster.
	kind delete cluster --name $(KIND_CLUSTER)

.PHONY: capi-init
capi-init: ## Install Cluster API CORE ONLY (no kubeadm) + cert-manager.
	CLUSTER_TOPOLOGY=true clusterctl init --core cluster-api:$(CAPI_VERSION) --bootstrap "-" --control-plane "-"

##@ Deploy

.PHONY: deploy
deploy: ## Deploy Mirage (CRDs, RBAC, manager) into the current cluster.
	kubectl apply -k config/default

.PHONY: undeploy
undeploy: ## Remove Mirage from the current cluster.
	kubectl delete -k config/default --ignore-not-found

.PHONY: dev
dev: kind-load deploy ## Rebuild image, load it, and (re)deploy; restart manager.
	kubectl -n mirage-system rollout restart deploy/mirage-controller-manager

##@ Examples

.PHONY: example-cluster
example-cluster: ## Apply the flagship quickstart cluster.
	kubectl apply -f examples/quickstart-cluster.yaml

.PHONY: example-delete
example-delete:
	kubectl delete -f examples/quickstart-cluster.yaml --ignore-not-found

##@ E2E

.PHONY: e2e
e2e: ## Run the e2e test against the current cluster (expects CAPI core + Mirage deployed).
	go test ./test/e2e/... -tags e2e -count=1 -v -timeout 20m
