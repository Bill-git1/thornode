FROM registry.gitlab.com/thorchain/devops/node-launcher:bitcoin-daemon-29.1 as bitcoin
FROM registry.gitlab.com/thorchain/devops/node-launcher:bitcoin-cash-daemon-28.0.1 as bitcoin-cash
FROM registry.gitlab.com/thorchain/devops/node-launcher:dogecoin-daemon-1.14.9 as dogecoin
FROM registry.gitlab.com/thorchain/devops/node-launcher:litecoin-daemon-0.21.4 as litecoin

ARG DOCKER_MIRROR=
FROM ${DOCKER_MIRROR}golang:1.24.6 AS build

# build geth since the image is alpine based
RUN go install github.com/ethereum/go-ethereum/cmd/geth@v1.16.3

# copy chain clis
COPY --from=bitcoin /usr/local/bin/bitcoin-cli /usr/local/bin/bitcoin-cli
COPY --from=bitcoin-cash /usr/local/bin/bitcoin-cli /usr/local/bin/bitcoin-cash-cli
COPY --from=dogecoin /usr/local/bin/dogecoin-cli /usr/local/bin/dogecoin-cli
COPY --from=litecoin /usr/local/bin/litecoin-cli /usr/local/bin/litecoin-cli

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TAG=mocknet
RUN make install