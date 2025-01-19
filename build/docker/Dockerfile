########################################################################################
# Build
########################################################################################

FROM golang:1.22.2-bullseye AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TAG=mainnet
ARG COMMIT=unknown

RUN make install

########################################################################################
# Deploy
########################################################################################

FROM debian:bullseye-20241111-slim@sha256:60a596681410bd31a48e5975806a24cd78328f3fd6b9ee5bc64dca6d46a51f29

RUN apt-get update && \
  apt-get install -y --no-install-recommends \
  ca-certificates=20210119 \
  curl=7.74.0-1.3+deb11u14 \
  jq=1.6-2.1 \
  libffi-dev=3.3-6 \
  libssl-dev=1.1.1w-0+deb11u2 \
  openssl=1.1.1w-0+deb11u2 \
  procps=2:3.3.17-5 \
  python3=3.9.2-3 \
  python3-pip=20.3.4-4+deb11u1 && \
  rm -rf /var/lib/apt/lists/* && \
  pip3 install --no-cache-dir requests==2.32.3 web3==7.5.0 retry==0.9.2

# Copy the compiled binaries over.
COPY --from=build /go/bin/thornode /go/bin/bifrost /go/bin/recover-keyshare-backup /usr/bin/

COPY build/scripts /scripts

# default to mainnet
ARG TAG=mainnet
ENV NET=$TAG

# default to fullnode
CMD ["/scripts/fullnode.sh"]
