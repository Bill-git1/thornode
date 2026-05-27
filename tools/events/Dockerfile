########################################################################################
# Build
########################################################################################

ARG DOCKER_MIRROR=
FROM ${DOCKER_MIRROR}golang:1.24.6-bullseye AS build

ENV GOBIN=/go/bin
ENV GOPATH=/go
ENV GOOS=linux
# tss-lib-private is a private GitLab subgroup repo; skip proxy.golang.org/sumdb
# so `go mod download` resolves it via the git insteadOf below.
ENV GOPRIVATE=gitlab.com/thorchain/tss/tss-lib-private.git

WORKDIR /app
COPY go.mod go.sum ./
# Mount CI_JOB_TOKEN as a BuildKit secret to authenticate the private fetch.
# required=false keeps local `make` builds working when CI_JOB_TOKEN is unset.
# File-based mount instead of env= so hadolint 2.12.1-beta can parse the RUN.
RUN --mount=type=secret,id=ci_job_token,required=false \
    if [ -s /run/secrets/ci_job_token ]; then \
      git config --global url."https://gitlab-ci-token:$(cat /run/secrets/ci_job_token)@gitlab.com/".insteadOf "https://gitlab.com/"; \
    fi && \
    go mod download && \
    rm -f /root/.gitconfig

COPY . .
ARG TAG=mainnet
RUN go install -tags $TAG ./tools/events

########################################################################################
# Deploy
########################################################################################

FROM debian:bullseye-20241111-slim@sha256:60a596681410bd31a48e5975806a24cd78328f3fd6b9ee5bc64dca6d46a51f29

COPY --from=build /go/bin/events /usr/bin/
COPY --from=build /go/pkg/mod/github.com/!cosm!wasm/wasmvm/v2@v2.2.1/internal/api/libwasmvm.*.so /usr/lib

RUN apt-get update && \
  apt-get install -y --no-install-recommends curl=7.74.0-1.3+deb11u16 ca-certificates=20210119 && \
  rm -rf /var/lib/apt/lists/*

CMD ["events"]