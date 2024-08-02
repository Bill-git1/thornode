#
# THORChain
#

#
# Build
#
FROM golang:1.22.2 AS build

ENV GOBIN=/go/bin
ENV GOPATH=/go
ENV CGO_ENABLED=0
ENV GOOS=linux

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TAG=mainnet
ARG COMMIT=unknown

RUN make protob install

#
# Main
#
FROM registry.gitlab.com/thorchain/thornode:runner-base-v1@sha256:8fc433efa3e853b59cafe920654705e37c94228ae99cba6892933a61594c11f3

# Copy the compiled binaries over.
COPY --from=build /go/bin/thornode /go/bin/bifrost /go/bin/recover-keyshare-backup /usr/bin/

COPY build/scripts /scripts

# default to mainnet
ARG TAG=mainnet
ENV NET=$TAG

# default to fullnode
CMD ["/scripts/fullnode.sh"]
