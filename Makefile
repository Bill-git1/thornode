########################################################################################
# Environment Checks
########################################################################################

CHECK_ENV:=$(shell ./scripts/check-env.sh)
ifneq ($(CHECK_ENV),)
$(error Check environment dependencies.)
endif

########################################################################################
# Config
########################################################################################

.PHONY: build test test-xmr-frost-e2e tools export healthcheck run-mocknet build-mocknet stop-mocknet halt-mocknet ps-mocknet reset-mocknet logs-mocknet openapi test-performance test-performance-quick test-performance-all test-performance-analyze test-performance-profile-cpu test-performance-profile-mem test-performance-trace test-performance-clean test-xmr-e2e

# mocknet docker compose files and overrides for macOS
DOCKER_COMPOSE_FILES ?= -f build/docker/docker-compose.yml
ifeq ($(shell uname -s),Darwin)
DOCKER_COMPOSE_FILES += -f build/docker/docker-compose.darwin.yml
endif

# pull branch name from CI if unset and available
ifdef CI_COMMIT_BRANCH
BRANCH?=${CI_COMMIT_BRANCH}
BUILDTAG?=${CI_COMMIT_BRANCH}
endif

# image build settings
COMMIT?=$(shell git log -1 --format='%H' 2>/dev/null)
BRANCH?=$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)
GITREF?=$(shell git rev-parse --short HEAD 2>/dev/null)
BUILDTAG?=$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)

# Forward CI_JOB_TOKEN into docker builds as a BuildKit secret so the
# in-image `go mod download` can fetch tss-lib-private. Empty when running
# locally without the token (Dockerfile mount is required=false).
ifneq ($(strip $(CI_JOB_TOKEN)),)
DOCKER_BUILD_SECRETS := --secret id=ci_job_token,env=CI_JOB_TOKEN
else
DOCKER_BUILD_SECRETS :=
endif

# compiler flags
VERSION:=$(shell cat version)
TAG?=mocknet
ldflags = -X gitlab.com/thorchain/thornode/v3/constants.Version=$(VERSION) \
      -X gitlab.com/thorchain/thornode/v3/constants.GitCommit=$(COMMIT) \
      -X github.com/cosmos/cosmos-sdk/version.Name=THORChain \
      -X github.com/cosmos/cosmos-sdk/version.AppName=thornode \
      -X github.com/cosmos/cosmos-sdk/version.Version=$(VERSION) \
      -X github.com/cosmos/cosmos-sdk/version.Commit=$(COMMIT) \
      -X github.com/cosmos/cosmos-sdk/version.BuildTags=$(TAG) \
      -buildid=

# golang settings
TEST_PATHS=$(shell go list ./... | grep -v bifrost/tss/go-tss) # Skip compute-intensive tests by default

# Parse test path argument from command line (e.g., "make test ./path")
ifeq (test,$(firstword $(MAKECMDGOALS)))
  TEST_PATH_ARG := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
  ifneq ($(TEST_PATH_ARG),)
    # Check if the argument looks like a path (contains /)
    ifneq ($(findstring /,$(TEST_PATH_ARG)),)
      # Ensure path starts with ./
      ifeq ($(filter ./%,$(TEST_PATH_ARG)),)
        TEST_DIR := ./$(TEST_PATH_ARG)
      else
        TEST_DIR := $(TEST_PATH_ARG)
      endif
      # Prevent make from treating the path as a target
      .PHONY: $(TEST_PATH_ARG)
      $(eval $(TEST_PATH_ARG):;@:)
    endif
  endif
endif

TEST_DIR?=${TEST_PATHS}
BUILD_FLAGS := -ldflags '$(ldflags)' -tags ${TAG} -trimpath
PARALLELISM ?= 1
TEST_BUILD_FLAGS := -parallel=$(PARALLELISM) -tags=mocknet
ifdef RUN
RUN_FLAG := -run=$(RUN)
endif
XMR_E2E_RUN := TestSeraiDistributedSignAndSubmitFlow|TestSeraiDistributedSignFailsClosedWhenThresholdCannotBeMet|TestKeygenFailsClosedWhenPeerCoordinationFails|TestXMRScannerInboundDeferredReferenceMemoAttribution|TestXMRScannerInboundMemolessDeferredAttribution|TestXMRReportSolvencyMultiVaultAccounting
BINARIES?=./cmd/thornode ./cmd/bifrost ./tools/recover-keyshare-backup
GOVERSION=$(shell awk '($$1 == "go") { print $$2 }' go.mod)

# docker tty args are disabled in CI and non-interactive shells
ifndef CI
ifneq ($(shell test -t 0 && test -t 1 && echo tty),)
DOCKER_TTY_ARGS=-it
endif
endif

HTTPS_GIT := https://gitlab.com/thorchain/thornode.git

########################################################################################
# Targets
########################################################################################

# ------------------------------ Generate ------------------------------

generate: go-generate generate-abi openapi proto-gen
	@./scripts/generate.py
	@cd test/simulation && go mod tidy

go-generate:
	@go install golang.org/x/tools/cmd/stringer@v0.28.0
	@go generate ./...

# mirror router abi from single source of truth to reference locations
generate-abi:
	@cp chain/evm/router-abi.json bifrost/pkg/chainclients/evm/abi/router.json
	@cp chain/evm/router-abi.json bifrost/pkg/chainclients/ethereum/abi/router.json
	@cp chain/evm/router-abi.json build/scripts/evm/router-abi.json
	@cp chain/evm/router-abi.json test/simulation/pkg/evm/abi/router.json

openapi:
	@docker run --rm \
		--user $(shell id -u):$(shell id -g) \
		-v $$PWD/openapi:/mnt \
		openapitools/openapi-generator-cli:v6.0.0@sha256:310bd0353c11863c0e51e5cb46035c9e0778d4b9c6fe6a7fc8307b3b41997a35 \
		generate -i /mnt/openapi.yaml -g go -o /mnt/gen
	@rm openapi/gen/go.mod openapi/gen/go.sum
	@find ./openapi/gen -type f -print0 | xargs -0 sed -i.bak \
		-e '/^[- ]*API version.*$(shell cat version)/d' \
		-e '/APIClient.*$(shell cat version)/d'
	@find ./openapi/gen -type f -name '*.bak' -delete
	@find ./openapi/gen -type f -name '*model*' -print0 | xargs -0 sed -i.bak \
		-e 's/MarshalJSON(/MarshalJSON_deprecated(/'
	@find ./openapi/gen -type f -name '*.bak' -delete

protoVer=0.13.2
protoImageName=ghcr.io/cosmos/proto-builder:$(protoVer)
protoImage=docker run --rm -v $(CURDIR):/workspace --workdir /workspace $(protoImageName)

proto-all: proto-format proto-lint proto-gen format

proto-gen:
	@echo "Generating Protobuf files"
	@$(protoImage) sh ./scripts/protocgen.sh

proto-format:
	@echo "Formatting Protobuf files"
	@$(protoImage) find ./ -name "*.proto" -exec clang-format -i {} \;

proto-format-check:
	@echo "Checking Protobuf formatting"
	@find ./ -name "*.proto" -print0 | xargs -0L1 $(protoImage) clang-format --dry-run -Werror

proto-lint:
	@$(protoImage) sh -c "cd proto/thorchain/v1 && buf lint --error-format=json"

proto-check-breaking:
	@$(protoImage) sh -c "cd proto/thorchain/v1 && buf breaking --against $(HTTPS_GIT)#branch=develop,subdir=proto/thorchain/v1"

# ------------------------------ Docs ------------------------------

docs-init:
	@cargo install mdbook --version 0.4.52
	@cargo install mdbook-admonish --version 1.18.0
	@cargo install mdbook-katex --version 0.9.4
	@cargo install mdbook-embed --version 0.2.0
	@cargo install mdbook-mermaid --version 0.14.0
	@cd docs && mdbook-admonish install --css-dir theme
	@cd docs && mdbook-mermaid install

docs-generate: docs-init
	@cd docs && mdbook build -d ../public

docs-dev: docs-init
	@cd docs && mdbook serve -d ../public --open

# ------------------------------ Build ------------------------------

build:
	go build ${BUILD_FLAGS} ${BINARIES}

test-xmr-frost-e2e:
	go test -count=1 -tags=mocknet ./bifrost/pkg/chainclients/monero/... -run '$${XMR_FROST_E2E_RUN}'

install:
	go install ${BUILD_FLAGS} ${BINARIES}

tools:
	go install -tags ${TAG} ./tools/pubkey2address
	go install -tags ${TAG} ./tools/p2p-check
	go install -tags ${TAG} ./tools/recover-keyshare-backup

# ------------------------------ Gitlab CI ------------------------------

gitlab-trigger-ci:
	@./scripts/gitlab-trigger-ci.sh

# ------------------------------ Housekeeping ------------------------------

format:
	@git ls-files '*.go' | grep -v -e '^docs/' -e '^api/' -e '^openapi/gen/' -e '.pb.go$$' -e '.pb.gw.go$$' -e '_gen.go$$' -e 'wire_gen.go$$' |\
		xargs gofumpt -w

lint:
	@./scripts/lint.sh
	@./scripts/trunk check --no-fix --show-existing --upstream origin/develop

lint-ci:
	@./scripts/lint.sh
	@./scripts/trunk-ci.sh

# ------------------------------ Unit Tests ------------------------------

test-coverage: test-network-specific
	@go test ${TEST_BUILD_FLAGS} -v -coverprofile=coverage.txt -covermode count ${TEST_DIR}
	sed -i.bak '/\.pb\.go:/d' coverage.txt
	@rm -f coverage.txt.bak

coverage-report: test-coverage
	@go tool cover -html=coverage.txt

test-coverage-sum: test-network-specific
	@go run gotest.tools/gotestsum@v1.13.0 --junitfile report.xml --format testname -- ${TEST_BUILD_FLAGS} -v -coverprofile=coverage.txt -covermode count ${TEST_DIR}
	sed -i.bak '/\.pb\.go:/d' coverage.txt
	@rm -f coverage.txt.bak
	@GOFLAGS='${TEST_BUILD_FLAGS}' go run github.com/boumenot/gocover-cobertura@v1.4.0 < coverage.txt > coverage.xml
	@go tool cover -func=coverage.txt
	@go tool cover -html=coverage.txt -o coverage.html

test:
ifeq ($(TEST_DIR),$(TEST_PATHS))
	@$(MAKE) test-network-specific
endif
	@go test ${TEST_BUILD_FLAGS} ${RUN_FLAG} ${TEST_DIR}
	@TAG=stagenet $(MAKE) build

test-all: test-network-specific
	@go test ${TEST_BUILD_FLAGS} ${RUN_FLAG} "./..."

test-go-tss:
	@go test ${TEST_BUILD_FLAGS} ${RUN_FLAG} --race "./bifrost/tss/go-tss/..."

test-xmr-e2e:
	@go test -count=1 ./bifrost/pkg/chainclients/monero -run '${XMR_E2E_RUN}'

test-network-specific:
	@go test -tags stagenet ${RUN_FLAG} ./common
	@go test -tags chainnet ${RUN_FLAG} ./common
	@go test -tags mainnet ${RUN_FLAG} ./common ./bifrost/pkg/chainclients/utxo/...
	@go test -tags mocknet ${RUN_FLAG} ./common ./bifrost/pkg/chainclients/utxo/...

test-race:
	@go test -race ${TEST_BUILD_FLAGS} ${RUN_FLAG} ${TEST_DIR}

# ------------------------------ Performance Tests ------------------------------

# Run comprehensive benchmarks with profiling (recommended)
test-performance:
	@echo "Running comprehensive performance benchmarks with profiling..."
	@./test/performance/run_quote_benchmarks.sh

# Quick benchmark comparison (Queue Disabled vs Enabled)
test-performance-quick:
	@echo "Running quick performance comparison..."
	@cd x/thorchain && go test -run='^$$' -bench='^BenchmarkQuoteSwapComparison$$' -benchtime=10x -benchmem

# Run all benchmarks without profiling
test-performance-all:
	@echo "Running all benchmark tests..."
	@cd x/thorchain && go test -run='^$$' -bench=. -benchtime=5x -benchmem

# Analyze latest performance results
test-performance-analyze:
	@echo "Analyzing latest performance results..."
	@LATEST=$$(ls -td test/performance/results/*/ 2>/dev/null | head -1); \
	if [ -n "$$LATEST" ]; then \
		./test/performance/analyze_profiles.sh "$$LATEST"; \
	else \
		echo "No performance results found. Run 'make test-performance' first."; \
		exit 1; \
	fi

# Open interactive CPU profile viewer
test-performance-profile-cpu:
	@LATEST=$$(ls -td test/performance/results/*/ 2>/dev/null | head -1); \
	if [ -n "$$LATEST" ] && [ -f "$$LATEST/cpu.prof" ]; then \
		echo "Opening CPU profile at http://localhost:8080..."; \
		go tool pprof -http=:8080 "$$LATEST/cpu.prof"; \
	else \
		echo "No CPU profile found. Run 'make test-performance' first."; \
		exit 1; \
	fi

# Open interactive memory profile viewer
test-performance-profile-mem:
	@LATEST=$$(ls -td test/performance/results/*/ 2>/dev/null | head -1); \
	if [ -n "$$LATEST" ] && [ -f "$$LATEST/mem.prof" ]; then \
		echo "Opening memory profile at http://localhost:8080..."; \
		go tool pprof -http=:8080 "$$LATEST/mem.prof"; \
	else \
		echo "No memory profile found. Run 'make test-performance' first."; \
		exit 1; \
	fi

# View execution trace
test-performance-trace:
	@LATEST=$$(ls -td test/performance/results/*/ 2>/dev/null | head -1); \
	if [ -n "$$LATEST" ] && [ -f "$$LATEST/trace.out" ]; then \
		echo "Opening execution trace viewer..."; \
		go tool trace "$$LATEST/trace.out"; \
	else \
		echo "No trace found. Run 'make test-performance' first."; \
		exit 1; \
	fi

# Clean performance test results
test-performance-clean:
	@echo "Cleaning performance test results..."
	@rm -rf test/performance/results/*
	@echo "Performance results cleaned."

# ------------------------------ Regression Tests ------------------------------

REGTEST_BIN_DIR ?= /regtest
REGTEST_MNT_DIR ?= /mnt

test-regression: build-test-regression
	@docker run --rm ${DOCKER_TTY_ARGS} \
		-e DEBUG -e RUN -e EXPORT -e TIME_FACTOR -e PARALLELISM -e FAIL_FAST \
		-e AUTO_UPDATE -e IGNORE_FAILURES -e CI -e UNFILTER_EXPORT \
		-e UID=$(shell id -u) -e GID=$(shell id -g) \
		-p 1317:1317 -p 26657:26657 \
		-v $(shell pwd)/test/regression/mnt:/mnt \
		-v $(shell pwd)/test/regression/suites:/app/test/regression/suites \
		-v $(shell pwd)/test/regression/templates:/app/test/regression/templates \
		-w /app thornode-regtest sh -c 'make _test-regression'

build-test-regression:
	@DOCKER_BUILDKIT=1 docker build . \
		-t thornode-regtest \
		-f ci/Dockerfile.regtest \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DOCKER_MIRROR \
		$(DOCKER_BUILD_SECRETS)

test-regression-coverage:
	@go tool cover -html=test/regression/mnt/coverage/coverage.txt

# internal target used in docker build - version pinned for consistent app hashes
_build-test-regression:
	@go install -ldflags '$(ldflags)' -tags=mocknet,regtest ./cmd/thornode
	@go build -ldflags '$(ldflags) -X gitlab.com/thorchain/thornode/v3/constants.Version=9.999.0' \
		-cover -tags=mocknet,regtest -o $(REGTEST_BIN_DIR)/cover-thornode ./cmd/thornode
	@go build -ldflags '$(ldflags) -X gitlab.com/thorchain/thornode/v3/constants.Version=9.999.0' \
		-tags mocknet -o $(REGTEST_BIN_DIR)/regtest ./test/regression/cmd

# internal target used in test run
_test-regression:
	@rm -rf $(REGTEST_MNT_DIR)/coverage && mkdir -p $(REGTEST_MNT_DIR)/coverage
	@cd test/regression && REGTEST_BIN_DIR=$(REGTEST_BIN_DIR) REGTEST_MNT_DIR=$(REGTEST_MNT_DIR) $(REGTEST_BIN_DIR)/regtest
	@go tool covdata textfmt -i $(REGTEST_MNT_DIR)/coverage -o $(REGTEST_MNT_DIR)/coverage/coverage.txt
	@grep -v -E -e archive.go -e 'v[0-9]+.go' -e openapi/gen $(REGTEST_MNT_DIR)/coverage/coverage.txt > $(REGTEST_MNT_DIR)/coverage/coverage-filtered.txt
	@go tool cover -func $(REGTEST_MNT_DIR)/coverage/coverage-filtered.txt > $(REGTEST_MNT_DIR)/coverage/func-coverage.txt
	@awk '/^total:/ {print "Regression Coverage: " $$3}' $(REGTEST_MNT_DIR)/coverage/func-coverage.txt
	@chown -R ${UID}:${GID} $(REGTEST_MNT_DIR)

# ------------------------------ Simulation Tests ------------------------------

test-simulation: build-mocknet reset-mocknet test-simulation-no-reset

TEST_SIMULATION_STAGES ?= seed,bootstrap,xmr-coverage,arb,swaps,memoless-swaps,consolidate,inactive-vault-refunds,memoless-refunds,solvency,ragnarok
TEST_SIMULATION_CLUSTER_STAGES ?= seed,bootstrap,swaps,memoless-swaps,inactive-vault-refunds,memoless-refunds,solvency,xmr-post-churn,ragnarok

test-simulation-cluster: build-test-simulation build-mocknet-cluster reset-mocknet-cluster
	@docker stop docker-midgard-1 docker-midgard-db-1 >/dev/null 2>&1 || true
	@STAGES=$(TEST_SIMULATION_CLUSTER_STAGES) docker run --rm ${DOCKER_TTY_ARGS} \
		-e PARALLELISM=$(PARALLELISM) -e STAGES \
		-e RANDOM_SEED_HEX \
		-e SIMULATION_PPROF_ADDRESS \
		-e XMR_SIM_HELPER_ENDPOINT -e XMR_SIM_HELPER_AUTH_BEARER \
		-e XMR_FROST_ENDPOINT -e XMR_FROST_AUTH_BEARER \
		--network host -w /app \
		thornode-simtest sh -c 'make _test-simulation'

# Reproduce the XMR monovault keygen mass-jailing (issue: keygen timing skew
# jails members that participated). One bifrost (pig) gets a test-only
# shortened FROST round-3 wait via XMR_KEYGEN_JAIL_ROUND3_TIMEOUT_MS, and
# netem adds symmetric latency on every bifrost's p2p port so transcript
# verifications lag round payloads by one hop. The victim then abandons a DKG
# its peers can finish, and the xmr-keygen-jail actor asserts the resulting
# split: new XMR vault created AND a member jailed for "failed to vote keygen
# in time". Tune the two knobs if the split does not reproduce: the round-3
# timeout must sit between one and two netem delays after round-3 start.
# The delay must be small enough that the regular Asgard go-tss keygen (n-of-n
# over the same p2p port, much chattier than FROST) still succeeds, or the new
# XMR monovault is never activated ("waiting for regular vault rotation before
# activating xmr monovault") and the repro stalls. 500ms is mainnet-realistic;
# the round-3 timeout must sit between one and two netem delays.
XMR_KEYGEN_JAIL_ROUND3_TIMEOUT_MS ?= 750
XMR_KEYGEN_JAIL_NETEM_DELAY_MS ?= 500

test-simulation-keygen-jail: export XMR_KEYGEN_JAIL_ROUND3_TIMEOUT_MS := $(XMR_KEYGEN_JAIL_ROUND3_TIMEOUT_MS)
test-simulation-keygen-jail: build-test-simulation build-mocknet-cluster reset-mocknet-cluster
	@docker stop docker-midgard-1 docker-midgard-db-1 >/dev/null 2>&1 || true
	@echo "running seed+bootstrap BEFORE latency injection so the bifrost p2p mesh can form"
	@echo "(netem-from-start makes the multi-round-trip libp2p handshake exceed the dial"
	@echo " timeout, so no p2p connection ever forms and every churn keygen times out)"
	@STAGES=seed,bootstrap docker run --rm ${DOCKER_TTY_ARGS} \
		-e PARALLELISM=$(PARALLELISM) -e STAGES \
		-e XMR_SIM_HELPER_ENDPOINT -e XMR_SIM_HELPER_AUTH_BEARER \
		-e XMR_FROST_ENDPOINT -e XMR_FROST_AUTH_BEARER \
		--network host -w /app \
		thornode-simtest sh -c 'make _test-simulation'
	@echo "applying netem delay $(XMR_KEYGEN_JAIL_NETEM_DELAY_MS)ms to bifrost p2p (port 5040)"
	@for c in docker-bifrost-1 docker-bifrost-cat-1 docker-bifrost-fox-1 docker-bifrost-pig-1; do \
		docker run --rm --net container:$$c --cap-add NET_ADMIN alpine:3.20 sh -c "\
			apk add -q iproute2 && \
			tc qdisc replace dev eth0 root handle 1: prio bands 4 priomap 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 && \
			tc qdisc add dev eth0 parent 1:4 handle 40: netem delay $(XMR_KEYGEN_JAIL_NETEM_DELAY_MS)ms && \
			tc filter add dev eth0 parent 1: protocol ip u32 match ip dport 5040 0xffff flowid 1:4 && \
			tc filter add dev eth0 parent 1: protocol ip u32 match ip sport 5040 0xffff flowid 1:4" \
			|| exit 1; \
	done
	@STAGES=xmr-keygen-jail docker run --rm ${DOCKER_TTY_ARGS} \
		-e PARALLELISM=$(PARALLELISM) -e STAGES \
		-e XMR_SIM_HELPER_ENDPOINT -e XMR_SIM_HELPER_AUTH_BEARER \
		-e XMR_FROST_ENDPOINT -e XMR_FROST_AUTH_BEARER \
		--network host -w /app \
		thornode-simtest sh -c 'make _test-simulation'

test-simulation-no-reset: build-test-simulation
	@docker stop docker-midgard-1 docker-midgard-db-1 >/dev/null 2>&1 || true
	@STAGES=$(TEST_SIMULATION_STAGES) docker run --rm ${DOCKER_TTY_ARGS} \
		-e PARALLELISM=$(PARALLELISM) -e STAGES \
		-e RANDOM_SEED_HEX \
		-e SIMULATION_PPROF_ADDRESS \
		-e XMR_SIM_HELPER_ENDPOINT -e XMR_SIM_HELPER_AUTH_BEARER \
		-e XMR_FROST_ENDPOINT -e XMR_FROST_AUTH_BEARER \
		--network host -w /app \
		thornode-simtest sh -c 'make _test-simulation'

build-test-simulation:
	@DOCKER_BUILDKIT=1 docker build . \
		-t thornode-simtest \
		-f ci/Dockerfile.simtest \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DOCKER_MIRROR \
		$(DOCKER_BUILD_SECRETS)

test-simulation-events:
	@docker compose $(DOCKER_COMPOSE_FILES) run --rm events

# internal target used in docker build
_build-test-simulation:
	@cd test/simulation && \
		go build -ldflags '$(ldflags)' -tags mocknet -o /simtest/simtest ./cmd

# internal target used in test run
_test-simulation:
	@cd test/simulation && /simtest/simtest

# ------------------------------ Single Node Mocknet ------------------------------

cli-mocknet:
	@docker compose $(DOCKER_COMPOSE_FILES) run --rm cli

run-mocknet:
	@docker compose $(DOCKER_COMPOSE_FILES) \
		--profile mocknet --profile midgard up -d

stop-mocknet:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet --profile midgard down -v

# Halt the Mocknet without erasing the blockchain history, so it can be resumed later.
halt-mocknet:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet --profile midgard down

build-mocknet:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet build \
		--build-arg COMMIT=$(COMMIT)

bootstrap-mocknet:
	@docker run --rm ${DOCKER_TTY_ARGS} \
		-e PARALLELISM=$(PARALLELISM) -e STAGES=seed,bootstrap \
		-e XMR_SIM_HELPER_ENDPOINT -e XMR_SIM_HELPER_AUTH_BEARER \
		-e XMR_FROST_ENDPOINT -e XMR_FROST_AUTH_BEARER \
		--network host -w /app \
		thornode-simtest sh -c 'make _test-simulation'

ps-mocknet:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet --profile midgard images
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet --profile midgard ps

logs-mocknet:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet logs -f thornode bifrost

reset-mocknet: stop-mocknet run-mocknet

# ------------------------------ Mocknet EVM Fork ------------------------------

reset-mocknet-fork-%: stop-mocknet
	@./tools/evm/run-mocknet-fork.sh $*

# ------------------------------ Multi Node Mocknet ------------------------------

run-mocknet-cluster:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet-cluster \
		--profile midgard up -d
	@# Compose can leave dependent validator thornodes in Created under detached
	@# startup when the primary thornode healthcheck is not yet satisfied.
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet-cluster \
		--profile midgard up -d thornode-cat thornode-fox thornode-pig

stop-mocknet-cluster:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet-cluster --profile midgard down -v

halt-mocknet-cluster:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet-cluster --profile midgard down

build-mocknet-cluster:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet-cluster build

ps-mocknet-cluster:
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet-cluster --profile midgard images
	@docker compose $(DOCKER_COMPOSE_FILES) --profile mocknet-cluster --profile midgard ps

reset-mocknet-cluster: stop-mocknet-cluster build-mocknet-cluster run-mocknet-cluster

# ------------------------------ Test Sync ------------------------------

test-sync-mainnet:
	@./scripts/test-sync.sh mainnet

test-sync-stagenet:
	@./scripts/test-sync.sh stagenet

# ------------------------------ Docker Build ------------------------------

docker-gitlab-login:
	docker login -u ${CI_REGISTRY_USER} -p ${CI_REGISTRY_PASSWORD} ${CI_REGISTRY}

docker-gitlab-push:
	./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode ${BRANCH} $(shell cat version) \
		| xargs -n1 | grep registry | xargs -n1 docker push
	docker push registry.gitlab.com/thorchain/thornode:${GITREF}

docker-gitlab-build:
	DOCKER_BUILDKIT=1 docker build . \
		-f build/docker/Dockerfile \
		$(shell sh ./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode ${BRANCH} $(shell cat version)) \
		-t registry.gitlab.com/thorchain/thornode:${GITREF} \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg TAG=$(BUILDTAG) \
		--build-arg DOCKER_MIRROR \
		$(DOCKER_BUILD_SECRETS)

########################################################################################
# Tools
########################################################################################

thorscan-build:
	@docker build tools/thorscan -f tools/thorscan/Dockerfile \
		$(shell sh ./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode thorscan-${BRANCH} $(shell cat version))

thorscan-gitlab-push: docker-gitlab-login
	@./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode thorscan-${BRANCH} $(shell cat version) \
		| xargs -n1 | grep registry | xargs -n1 docker push

events-build:
	@DOCKER_BUILDKIT=1 docker build . -f tools/events/Dockerfile \
		$(shell sh ./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode events-${BRANCH} $(shell cat version)) \
		--build-arg TAG=$(BUILDTAG) \
		--build-arg DOCKER_MIRROR \
		$(DOCKER_BUILD_SECRETS)

events-gitlab-push: docker-gitlab-login
	@./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode events-${BRANCH} $(shell cat version) \
		| xargs -n1 | grep registry | xargs -n1 docker push
