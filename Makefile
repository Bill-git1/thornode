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

.PHONY: build test tools export healthcheck run-mocknet build-mocknet stop-mocknet halt-mocknet ps-mocknet reset-mocknet logs-mocknet openapi

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
TEST_DIR?=${TEST_PATHS}
BUILD_FLAGS := -ldflags '$(ldflags)' -tags ${TAG} -trimpath
TEST_BUILD_FLAGS := -parallel=1 -tags=mocknet
BINARIES?=./cmd/thornode ./cmd/bifrost ./tools/recover-keyshare-backup
GOVERSION=$(shell awk '($$1 == "go") { print $$2 }' go.mod)

# docker tty args are disabled in CI
ifndef CI
DOCKER_TTY_ARGS=-it
endif

HTTPS_GIT := gitlab.com/thorchain/thornode.git

########################################################################################
# Targets
########################################################################################

# ------------------------------ Generate ------------------------------

generate: go-generate openapi proto-gen
	@./scripts/generate.py
	@cd test/simulation && go mod tidy

go-generate:
	@go install golang.org/x/tools/cmd/stringer@v0.28.0
	@go generate ./...

openapi:
	@docker run --rm \
		--user $(shell id -u):$(shell id -g) \
		-v $$PWD/openapi:/mnt \
		openapitools/openapi-generator-cli:v6.0.0@sha256:310bd0353c11863c0e51e5cb46035c9e0778d4b9c6fe6a7fc8307b3b41997a35 \
		generate -i /mnt/openapi.yaml -g go -o /mnt/gen
	@rm openapi/gen/go.mod openapi/gen/go.sum
	@find ./openapi/gen -type f | xargs sed -i '/^[- ]*API version.*$(shell cat version)/d;/APIClient.*$(shell cat version)/d'
	@find ./openapi/gen -type f | grep model | xargs sed -i 's/MarshalJSON(/MarshalJSON_deprecated(/'

protoVer=0.13.2
protoImageName=ghcr.io/cosmos/proto-builder:$(protoVer)
protoImage=docker run --rm -v $(CURDIR):/workspace --workdir /workspace $(protoImageName)

proto-all: proto-format proto-lint proto-gen format

proto-gen:
	@echo "Generating bifrost protobuf files"
	@docker run --rm -v $(shell pwd):/app -w /app golang:$(GOVERSION) make bifrost-protosh
	@echo "Generating Protobuf files"
	@$(protoImage) sh ./scripts/protocgen.sh

proto-format:
	@echo "Formatting Protobuf files"
	@$(protoImage) find ./ -name "*.proto" -exec clang-format -i {} \;

proto-lint:
	@$(protoImage) buf lint --error-format=json

proto-check-breaking:
	@$(protoImage) buf breaking --against $(HTTPS_GIT)#branch=develop

bifrost-protosh:
	@./scripts/bifrostprotocgen.sh

# ------------------------------ Docs ------------------------------

docs-init:
	@cargo install mdbook --version 0.4.44
	@cargo install mdbook-admonish --version 1.18.0
	@cargo install mdbook-katex --version 0.9.2
	@cargo install mdbook-embed --version 0.2.0
	@cd docs && mdbook-admonish install --css-dir theme

docs-generate: docs-init
	@cd docs && mdbook build -d ../public

docs-dev: docs-init
	@cd docs && mdbook serve -d ../public --open

# ------------------------------ Build ------------------------------

build:
	go build ${BUILD_FLAGS} ${BINARIES}

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
	@./scripts/trunk check --no-fix --upstream origin/develop

lint-ci:
	@./scripts/lint.sh
	@./scripts/trunk-ci.sh

# ------------------------------ Unit Tests ------------------------------

test-coverage: test-network-specific
	@go test ${TEST_BUILD_FLAGS} -v -coverprofile=coverage.txt -covermode count ${TEST_DIR}
	sed -i '/\.pb\.go:/d' coverage.txt

coverage-report: test-coverage
	@go tool cover -html=coverage.txt

test-coverage-sum: test-network-specific
	@go run gotest.tools/gotestsum --junitfile report.xml --format testname -- ${TEST_BUILD_FLAGS} -v -coverprofile=coverage.txt -covermode count ${TEST_DIR}
	sed -i '/\.pb\.go:/d' coverage.txt
	@GOFLAGS='${TEST_BUILD_FLAGS}' go run github.com/boumenot/gocover-cobertura < coverage.txt > coverage.xml
	@go tool cover -func=coverage.txt
	@go tool cover -html=coverage.txt -o coverage.html

test: test-network-specific
	@CGO_ENABLED=0 go test ${TEST_BUILD_FLAGS} ${TEST_DIR}

test-all: test-network-specific
	@CGO_ENABLED=0 go test ${TEST_BUILD_FLAGS} "./..."

test-go-tss:
	@go test ${TEST_BUILD_FLAGS} --race "./bifrost/tss/go-tss/..."

test-network-specific:
	@CGO_ENABLED=0 go test -tags stagenet ./common
	@CGO_ENABLED=0 go test -tags mainnet ./common ./bifrost/pkg/chainclients/utxo/...
	@CGO_ENABLED=0 go test -tags mocknet ./common ./bifrost/pkg/chainclients/utxo/...

test-race:
	@go test -race ${TEST_BUILD_FLAGS} ${TEST_DIR}

# ------------------------------ Regression Tests ------------------------------

test-regression: build-test-regression
	@docker run --rm ${DOCKER_TTY_ARGS} \
		-e DEBUG -e RUN -e EXPORT -e TIME_FACTOR -e PARALLELISM -e FAIL_FAST \
		-e AUTO_UPDATE -e IGNORE_FAILURES \
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
		--build-arg COMMIT=$(COMMIT)

test-regression-coverage:
	@go tool cover -html=test/regression/mnt/coverage/coverage.txt

# internal target used in docker build - version pinned for consistent app hashes
_build-test-regression:
	@go install -ldflags '$(ldflags)' -tags=mocknet,regtest ./cmd/thornode
	@go build -ldflags '$(ldflags) -X gitlab.com/thorchain/thornode/v3/constants.Version=9.999.0' \
		-cover -tags=mocknet,regtest -o /regtest/cover-thornode ./cmd/thornode
	@go build -ldflags '$(ldflags) -X gitlab.com/thorchain/thornode/v3/constants.Version=9.999.0' \
		-tags mocknet -o /regtest/regtest ./test/regression/cmd

# internal target used in test run
_test-regression:
	@rm -rf /mnt/coverage && mkdir -p /mnt/coverage
	@cd test/regression && /regtest/regtest
	@go tool covdata textfmt -i /mnt/coverage -o /mnt/coverage/coverage.txt
	@grep -v -E -e archive.go -e 'v[0-9]+.go' -e openapi/gen /mnt/coverage/coverage.txt > /mnt/coverage/coverage-filtered.txt
	@go tool cover -func /mnt/coverage/coverage-filtered.txt > /mnt/coverage/func-coverage.txt
	@awk '/^total:/ {print "Regression Coverage: " $$3}' /mnt/coverage/func-coverage.txt
	@chown -R ${UID}:${GID} /mnt

# ------------------------------ Simulation Tests ------------------------------

test-simulation: build-mocknet reset-mocknet test-simulation-no-reset

test-simulation-no-reset: build-test-simulation
	@docker run --rm ${DOCKER_TTY_ARGS} \
		-e PARALLELISM -e STAGES --network host -w /app \
		thornode-simtest sh -c 'make _test-simulation'

build-test-simulation:
	@DOCKER_BUILDKIT=1 docker build . \
		-t thornode-simtest \
		-f ci/Dockerfile.simtest \
		--build-arg COMMIT=$(COMMIT) \

test-simulation-events:
	@docker compose -f build/docker/docker-compose.yml run --rm events

# internal target used in docker build
_build-test-simulation:
	@cd test/simulation && \
		go build -ldflags '$(ldflags)' -tags mocknet -o /simtest/simtest ./cmd

# internal target used in test run
_test-simulation:
	@cd test/simulation && /simtest/simtest

# ------------------------------ Single Node Mocknet ------------------------------

cli-mocknet:
	@docker compose -f build/docker/docker-compose.yml run --rm cli

run-mocknet:
	@docker compose -f build/docker/docker-compose.yml \
		--profile mocknet --profile midgard up -d

stop-mocknet:
	@docker compose -f build/docker/docker-compose.yml --profile mocknet --profile midgard down -v

# Halt the Mocknet without erasing the blockchain history, so it can be resumed later.
halt-mocknet:
	@docker compose -f build/docker/docker-compose.yml --profile mocknet --profile midgard down

build-mocknet: proto-gen
	@docker compose -f build/docker/docker-compose.yml --profile mocknet --profile midgard build \
		--build-arg COMMIT=$(COMMIT)

bootstrap-mocknet:
	@docker run --rm ${DOCKER_TTY_ARGS} \
		-e PARALLELISM -e STAGES=seed,bootstrap --network host -w /app \
		thornode-simtest sh -c 'make _test-simulation'

ps-mocknet:
	@docker compose -f build/docker/docker-compose.yml --profile mocknet --profile midgard images
	@docker compose -f build/docker/docker-compose.yml --profile mocknet --profile midgard ps

logs-mocknet:
	@docker compose -f build/docker/docker-compose.yml --profile mocknet logs -f thornode bifrost

reset-mocknet: stop-mocknet run-mocknet

# ------------------------------ Mocknet EVM Fork ------------------------------

reset-mocknet-fork-%: stop-mocknet
	@./tools/evm/run-mocknet-fork.sh $*

# ------------------------------ Multi Node Mocknet ------------------------------

run-mocknet-cluster:
	@docker compose -f build/docker/docker-compose.yml --profile mocknet-cluster \
		--profile midgard up -d

stop-mocknet-cluster:
	@docker compose -f build/docker/docker-compose.yml --profile mocknet-cluster --profile midgard down -v

halt-mocknet-cluster:
	@docker compose -f build/docker/docker-compose.yml --profile mocknet-cluster --profile midgard down

build-mocknet-cluster:
	@docker compose -f build/docker/docker-compose.yml --profile mocknet-cluster --profile midgard build

ps-mocknet-cluster:
	@docker compose -f build/docker/docker-compose.yml --profile mocknet-cluster --profile midgard images
	@docker compose -f build/docker/docker-compose.yml --profile mocknet-cluster --profile midgard ps

reset-mocknet-cluster: stop-mocknet-cluster build-mocknet-cluster run-mocknet-cluster

# ------------------------------ Test Sync ------------------------------

test-sync-mainnet:
	@./scripts/test-sync.sh

# ------------------------------ Docker Build ------------------------------

docker-gitlab-login:
	docker login -u ${CI_REGISTRY_USER} -p ${CI_REGISTRY_PASSWORD} ${CI_REGISTRY}

docker-gitlab-push:
	./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode ${BRANCH} $(shell cat version) \
		| xargs -n1 | grep registry | xargs -n1 docker push
	docker push registry.gitlab.com/thorchain/thornode:${GITREF}

docker-gitlab-build:
	docker build . \
		-f build/docker/Dockerfile \
		$(shell sh ./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode ${BRANCH} $(shell cat version)) \
		-t registry.gitlab.com/thorchain/thornode:${GITREF} \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg TAG=$(BUILDTAG)

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
	@docker build . -f tools/events/Dockerfile \
		$(shell sh ./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode events-${BRANCH} $(shell cat version))

events-gitlab-push: docker-gitlab-login
	@./build/docker/semver_tags.sh registry.gitlab.com/thorchain/thornode events-${BRANCH} $(shell cat version) \
		| xargs -n1 | grep registry | xargs -n1 docker push
