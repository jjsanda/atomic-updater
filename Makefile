# atomic-updater — top-level build orchestration.
#
# Everything runnable in a plain Linux environment with Go, gcc, Node and
# Docker. Yocto images are NOT built here (see device/meta-atomic/README.md).

SHELL := /bin/bash
.DEFAULT_GOAL := help

REPO_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BIN       := $(REPO_ROOT)/bin
OUT       := $(REPO_ROOT)/out

export GOFLAGS := -trimpath

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

# ---------------------------------------------------------------- toolchain

.PHONY: pki
pki: ## Generate the local development PKI under ./pki/
	scripts/pki/bootstrap-dev-pki.sh

# ---------------------------------------------------------------- server

.PHONY: server-build
server-build: ## Build atomic-server (requires ui-build for the real dashboard)
	mkdir -p $(BIN)
	cd server && go build -o $(BIN)/atomic-server ./cmd/atomic-server

.PHONY: server-test
server-test: ## Server unit + property tests (no external deps)
	cd server && go vet ./... && go test ./...

.PHONY: server-test-integration
server-test-integration: ## Server storage tests against dockerized Postgres (starts/stops its own container)
	scripts/dev/run-storage-tests.sh

# ---------------------------------------------------------------- device

.PHONY: agent-build
agent-build: ## Build the check-in agent (static, CGO off)
	mkdir -p $(BIN)
	cd device/agent && CGO_ENABLED=0 go build -ldflags='-s -w' -o $(BIN)/atomic-agent ./cmd/atomic-agent

.PHONY: agent-test
agent-test: ## Agent unit tests
	cd device/agent && go vet ./... && go test ./...

.PHONY: health-build
health-build: ## Build atomic-health (C)
	$(MAKE) -C device/health

.PHONY: health-test
health-test: ## atomic-health test suite (PATH-injected fakes)
	$(MAKE) -C device/health test

# ---------------------------------------------------------------- ui

.PHONY: ui-build
ui-build: ## Build the React dashboard into server/internal/web/dist
	cd server/ui && npm ci && npm run build

# ---------------------------------------------------------------- bundles & e2e

.PHONY: images
images: ## Build the rauc-tools and device-sim Docker images
	docker build -t atomic-updater/rauc-tools:dev bundle/
	docker build -t atomic-updater/device-sim:dev -f e2e/device-sim/Dockerfile .

.PHONY: server-image
server-image: ## Build the atomic-server Docker image (includes UI build)
	docker build -t atomic-updater/server:dev -f server/Dockerfile .

.PHONY: bundles
bundles: pki ## Build the demo signed bundles used by the e2e suite
	mkdir -p $(OUT)/bundles
	e2e/make-test-bundles.sh $(OUT)/bundles

.PHONY: e2e
e2e: ## Run the full end-to-end OTA suite (compose stack + device sims)
	cd e2e && ATOMIC_E2E=1 go test -v -timeout 45m -count=1 ./...

.PHONY: soak
soak: ## 5-sim x 10-update happy-path soak
	cd e2e && ATOMIC_E2E=1 ATOMIC_E2E_SOAK=1 go test -v -timeout 60m -count=1 -run TestSoak ./...

# ---------------------------------------------------------------- hygiene

.PHONY: test
test: server-test agent-test health-test ## All host-runnable unit tests

.PHONY: build
build: server-build agent-build health-build ## All host-runnable binaries

.PHONY: shellcheck
shellcheck: ## Run shellcheck (containerized) over all shell scripts
	docker run --rm -v $(REPO_ROOT):/mnt:ro koalaman/shellcheck:stable \
	  $$(cd $(REPO_ROOT) && find . -name '*.sh' -not -path './node_modules/*' -not -path './pki/*')

.PHONY: staticcheck
staticcheck: ## staticcheck on both Go modules
	cd server && go run honnef.co/go/tools/cmd/staticcheck@2023.1.7 ./...
	cd device/agent && go run honnef.co/go/tools/cmd/staticcheck@2023.1.7 ./...

.PHONY: clean
clean: ## Remove build artifacts (keeps pki/)
	rm -rf $(BIN) $(OUT)
	$(MAKE) -C device/health clean
	cd e2e && docker compose -f compose.yaml down -v --remove-orphans 2>/dev/null || true

.PHONY: all
all: build test ## Build + unit-test everything host-runnable
