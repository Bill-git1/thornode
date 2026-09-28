# RPC-only tss-lib compatibility shim

This module provides only the structural API surface that the THORNode RPC
binary imports indirectly through its public P2P message and conversion
packages. It does not implement threshold signing, key generation, MPC rounds,
proofs, key shares, or any other cryptographic operation.

This shim is suitable only for the RPC/fullnode `thornode` binary. It must not
be used to build or run Bifrost or a validator. Operational APIs are
intentionally absent, so code that attempts to use them fails to compile rather
than receiving fake cryptographic results.
