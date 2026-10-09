.DEFAULT_GOAL := help

IMAGE_TAG ?= dev

.PHONY: help build build-go images test vet fmt web-install web-build web-lint web-test dev-server dev-web chart-check shell-test release-test check \
	dummy-up dummy-down dummy-login dummy-logout dummy-redeploy dummy-reset dummy-smoke dummy-status dummy-logs

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

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

shell-test: ## Run the shell tests: the dummy's directory guard, its smoke decisions and the rule that no script reads the CLI login (needs jq)
	@command -v jq > /dev/null || { echo "jq is needed" >&2; exit 1; }
	sh dev/kind/lib_test.sh
	sh dev/kind/smoke-lib_test.sh
	sh scripts/check-login-dir-untouched_test.sh
	sh scripts/check-login-dir-untouched.sh
	sh scripts/check-release_test.sh
	sh scripts/cli-checksums_test.sh
	sh scripts/set-chart-version_test.sh
	sh scripts/check-pr-title_test.sh

release-test: ## Test the release configuration in a scratch repository and the chart's OCI push (needs node, git, helm, docker)
	cd release && npm ci --ignore-scripts
	sh release/test/dry-run.sh
	sh scripts/chart-oci_test.sh

check: fmt vet test chart-check shell-test web-lint web-test web-build ## Everything CI checks

dummy-up: ## Build up the kind dummy setup: cluster, demo workloads, Argo CD, images, the chart (real agent; login: dummy-login)
	dev/kind/dummy-up.sh

dummy-down: ## Remove the dummy cluster and what was generated; the CLI login in ~/remedy-kind/claude stays
	dev/kind/down.sh

dummy-login: ## The one-time CLI login in the dummy's runner pod (interactive)
	dev/kind/dummy-login.sh

dummy-logout: ## Remove the CLI login from the host directory (asks first)
	dev/kind/dummy-logout.sh

dummy-redeploy: ## Rebuild both images, load them and upgrade the release (the fast loop)
	dev/kind/dummy-redeploy.sh

dummy-reset: ## Empty the dummy's database; keeps the cluster, the secrets and the login
	dev/kind/dummy-reset.sh

dummy-smoke: ## Run two real runs against the dummy (a cluster question and an approved restart); costs subscription quota
	dev/kind/smoke.sh

dummy-status: ## State of the dummy setup as key: value lines
	dev/kind/dummy-status.sh

dummy-logs: ## Follow the dummy's logs
	dev/kind/dummy-logs.sh
