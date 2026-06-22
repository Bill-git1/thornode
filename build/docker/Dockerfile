########################################################################################
# Build
########################################################################################

ARG DOCKER_MIRROR=
FROM ${DOCKER_MIRROR}golang:1.26.4-bookworm AS build

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

COPY api api
COPY app app
COPY bifrost bifrost
COPY cmd cmd
COPY common common
COPY config config
COPY constants constants
COPY log log
COPY openapi openapi
COPY scripts/check-env.sh scripts/check-env.sh
COPY x x
COPY Makefile Makefile
COPY version version
COPY tools/thorscan tools/thorscan
COPY tools/recover-keyshare-backup tools/recover-keyshare-backup

ARG TAG=mainnet
ARG COMMIT=unknown

RUN make install

########################################################################################
# Deploy
########################################################################################

FROM debian:bookworm-20260610-slim@sha256:96e378d7e6531ac9a15ad505478fcc2e69f371b10f5cdf87857c4b8188404716

RUN apt-get update && \
  apt-get install -y --no-install-recommends \
  ca-certificates=20230311+deb12u1 \
  curl=7.88.1-10+deb12u* \
  jq=1.6-2.1+deb12u1 \
  libffi-dev=3.4.4-1 \
  libssl-dev=3.0.* \
  openssl=3.0.* \
  procps=2:4.0.2-3 \
  python3=3.11.2-1+b1 \
  python3-pip=23.0.1+dfsg-1 && \
  rm -rf /var/lib/apt/lists/* && \
  pip3 install --no-cache-dir --break-system-packages requests==2.32.3 web3==7.5.0 retry==0.9.2

# Copy the compiled binaries over.
COPY --from=build /go/bin/thornode /go/bin/bifrost /go/bin/recover-keyshare-backup /usr/bin/
COPY --from=build /go/pkg/mod/github.com/!cosm!wasm/wasmvm/v2@v2.2.1/internal/api/libwasmvm.*.so /usr/lib

COPY build/scripts /scripts

# default to mainnet
ARG TAG=mainnet
ENV NET=$TAG

# default to fullnode
CMD ["/scripts/fullnode.sh"]
