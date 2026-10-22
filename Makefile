.DEFAULT_GOAL := help

IMAGE_TAG ?= dev

# What the binaries and the images say about themselves (internal/version).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
LDFLAGS = -X github.com/Jaydee94/remedy/internal/version.Version=$(VERSION) -X github.com/Jaydee94/remedy/internal/version.Commit=$(COMMIT)

.PHONY: help build build-go images test vet fmt web-install web-build web-lint web-test dev-server dev-web chart-check shell-test release-test check pg-up pg-down \
	dummy-up dummy-down dummy-login dummy-logout dummy-redeploy dummy-reset dummy-smoke dummy-status dummy-logs

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

build-go: ## Build the Go binaries into ./bin (no UI embedded): server, runner
	go build -ldflags '$(LDFLAGS)' -o bin/remedy-server ./cmd/remedy-server
	go build -ldflags '$(LDFLAGS)' -o bin/remedy-runner ./cmd/remedy-runner

build: web-build ## Build the UI, then the Go binaries with the UI embedded (server, runner)
	go build -tags webui -ldflags '$(LDFLAGS)' -o bin/remedy-server ./cmd/remedy-server
	go build -ldflags '$(LDFLAGS)' -o bin/remedy-runner ./cmd/remedy-runner

images: ## Build the control plane and runner images for this machine (tag $(IMAGE_TAG), default dev)
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t remedy-server:$(IMAGE_TAG) .
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -f Dockerfile.runner -t remedy-runner:$(IMAGE_TAG) .

test: ## Run Go tests (they need a Postgres: starts a throwaway one with dev/pg/up.sh when REMEDY_TEST_DATABASE_URL is unset and docker exists)
	@if [ -n "$$REMEDY_TEST_DATABASE_URL" ]; then \
		go test ./...; \
	elif command -v docker > /dev/null 2>&1; then \
		echo "REMEDY_TEST_DATABASE_URL is unset: using the throwaway Postgres of dev/pg/up.sh (make pg-down removes it)"; \
		sh dev/pg/up.sh && . dev/pg/env.sh && go test ./...; \
	else \
		echo "REMEDY_TEST_DATABASE_URL is unset and docker is not installed: the tests that need a Postgres will fail." >&2; \
		echo "Point REMEDY_TEST_DATABASE_URL at a Postgres 16 server you may create schemas in, or install docker and run make pg-up (see dev/pg/README.md)." >&2; \
		go test ./...; \
	fi

pg-up: ## Start a throwaway Postgres for the tests (docker); writes dev/pg/env.sh, then: . dev/pg/env.sh
	sh dev/pg/up.sh

pg-down: ## Remove the throwaway test Postgres
	sh dev/pg/down.sh

vet: ## Run go vet, also over the tests that need the kind or SSH testbed (they are not run, but they must compile)
	go vet ./...
	go vet -tags kind,kindwrite,sshlive ./...

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

dev-server: ## Run the control plane on :8080 (needs REMEDY_DATABASE_URL, REMEDY_ADMIN_PASSWORD, REMEDY_RUNNER_TOKEN, REMEDY_MASTER_KEY)
	@test -n "$$REMEDY_DATABASE_URL" || { echo "REMEDY_DATABASE_URL is unset: point it at a PostgreSQL 16 database (README quick start; make pg-up starts a throwaway one for the tests)" >&2; exit 1; }
	go run ./cmd/remedy-server

dev-web: ## Run the Vite dev server
	cd web && npm run dev

chart-check: ## Lint and render the Helm chart (needs helm; kubeconform when installed), then run its render tests
	@command -v helm > /dev/null || { echo "helm is needed" >&2; exit 1; }
	helm lint deploy/chart -f deploy/chart/ci/lint-values.yaml
	helm lint deploy/chart -f deploy/chart/ci/postgres-values.yaml
	@if command -v kubeconform > /dev/null; then \
		helm template remedy deploy/chart --namespace remedy-system -f deploy/chart/ci/lint-values.yaml | kubeconform -strict -summary && \
		helm template remedy deploy/chart --namespace remedy-system -f deploy/chart/ci/postgres-values.yaml | kubeconform -strict -summary; \
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
	sh scripts/check-expected-version_test.sh

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
