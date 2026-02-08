FROM rust:1.80-bookworm AS build

ARG AGAVE_TAG=v2.3.8

RUN apt-get update && apt-get install -y --no-install-recommends \
  git=1:2.39.5-0+deb12u3 \
  clang=1:14.0-55.7~deb12u1 \
  llvm=1:14.0-55.7~deb12u1 \
  libclang-dev=1:14.0-55.7~deb12u1 \
  pkg-config=1.8.1-1 \
  libssl-dev=3.0.18-1~deb12u2 \
  protobuf-compiler=3.21.12-3 \
  cmake=3.25.1-1 \
  libudev-dev=252.39-1~deb12u1 \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN git clone --depth=1 --branch="$AGAVE_TAG" https://github.com/anza-xyz/agave.git . && \
  cargo build --release \
  --bin solana-test-validator \
  --bin solana-genesis \
  --bin solana \
  --bin solana-keygen


FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
  ca-certificates=20230311+deb12u1 \
  libssl3=3.0.18-1~deb12u2 \
  libudev1=252.39-1~deb12u1 \
  && rm -rf /var/lib/apt/lists/*

COPY --from=build /src/target/release/solana-test-validator /usr/local/bin/
COPY --from=build /src/target/release/solana-genesis /usr/local/bin/
COPY --from=build /src/target/release/solana /usr/local/bin/
COPY --from=build /src/target/release/solana-keygen /usr/local/bin/
