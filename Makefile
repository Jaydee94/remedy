.DEFAULT_GOAL := help

.PHONY: help build build-go test vet fmt web-install web-build web-lint dev-server dev-web check

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

build-go: ## Build both Go binaries into ./bin (no UI embedded)
	go build -o bin/remedy-server ./cmd/remedy-server
	go build -o bin/remedy-runner ./cmd/remedy-runner

build: web-build ## Build the UI, then both Go binaries with the UI embedded
	go build -tags webui -o bin/remedy-server ./cmd/remedy-server
	go build -o bin/remedy-runner ./cmd/remedy-runner

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

dev-server: ## Run the control plane on :8080
	go run ./cmd/remedy-server

dev-web: ## Run the Vite dev server
	cd web && npm run dev

check: fmt vet test web-lint web-build ## Everything CI checks
