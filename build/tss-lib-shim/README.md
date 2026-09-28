# RPC-only tss-lib compatibility shim

This module exists only to build the `cmd/thornode` RPC/fullnode binary without
access to THORChain's private `tss-lib-private` module. It implements the
minimal structural API required by the `cmd/thornode` dependency closure. It
is not an implementation of TSS cryptography and does not provide threshold
signing, key generation, MPC rounds, proofs, key shares, or any other
cryptographic operation.

The resulting image is intended only for RPC/fullnode use. This module must not
be used as a replacement for the private library for Bifrost or validator
signing. Operational APIs are intentionally absent, so code that attempts to
use them fails to compile rather than receiving fake cryptographic results.

## Build

From a checkout of the public RPC branch:

```bash
docker build \
  --file build/docker/Dockerfile.rpc-shim \
  --build-arg TAG=mainnet \
  --build-arg COMMIT="$(git rev-parse HEAD)" \
  --tag liquify/thornode:mainnet-3.20.3-quote-lru-rpcshim \
  .
```

Verify the resulting binary:

```bash
docker run --rm \
  --entrypoint thornode \
  liquify/thornode:mainnet-3.20.3-quote-lru-rpcshim \
  version --long
```

The output should include `version: 3.20.3`, `build_tags: mainnet`, and the
commit corresponding to the checked-out branch HEAD.
