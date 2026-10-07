.DEFAULT_GOAL := help

IMAGE_TAG ?= dev

.PHONY: help build build-go images test vet fmt web-install web-build web-lint web-test dev-server dev-web chart-check check

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

build-go: ## Build both Go binaries into ./bin (no UI embedded)
	go build -o bin/remedy-server ./cmd/remedy-server
	go build -o bin/remedy-runner ./cmd/remedy-runner

build: web-build ## Build the UI, then both Go binaries with the UI embedded
	go build -tags webui -o bin/remedy-server ./cmd/remedy-server
	go build -o bin/remedy-runner ./cmd/remedy-runner

images: ## Build the control plane and runner images for this machine (tag $(IMAGE_TAG), default dev)
	docker build -t remedy-server:$(IMAGE_TAG) .
	docker build -f Dockerfile.runner -t remedy-runner:$(IMAGE_TAG) .

test: ## Run Go tests
	go test ./...

vet: ## Run go vet, also over the tests that need the kind testbed (they are not run, but they must compile)
	go vet ./...
	go vet -tags kind,kindwrite ./...

fmt: ## Fail if Go files are not gofmt-clean
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

web-install: ## Install web dependencies
	cd web && npm ci

web-build: ## Typecheck and build the web UI
	cd web && npm run build

web-lint: ## Lint the web UI
	cd web && npm run lint

web-test: ## Run the web unit tests (node --test, pure logic only)
	cd web && npm test

dev-server: ## Run the control plane on :8080
	go run ./cmd/remedy-server

dev-web: ## Run the Vite dev server
	cd web && npm run dev

chart-check: ## Lint and render the Helm chart (needs helm; kubeconform when installed), then run its render tests
	@command -v helm > /dev/null || { echo "helm is needed" >&2; exit 1; }
	helm lint deploy/chart -f deploy/chart/ci/lint-values.yaml
	@if command -v kubeconform > /dev/null; then \
		helm template remedy deploy/chart --namespace remedy-system -f deploy/chart/ci/lint-values.yaml | kubeconform -strict -summary; \
	else echo "kubeconform is not installed: skipping the schema check (CI runs it)"; fi
	REMEDY_REQUIRE_HELM=1 go test ./deploy -count=1

check: fmt vet test chart-check web-lint web-test web-build ## Everything CI checks
