# Bug Bounty Program

## Overview

The THORChain project runs a bug bounty program that pays bounties for identifying bugs in the core protocol
that could lead to loss of funds, down time, and other adverse/undesired conditions on the network.

If you believe you have found an issue, please submit a writeup to:

security@thorchain.org

Fallback (in case of issues with the above): security@sodalabs.xyz

Encrypting submissions is strongly encouraged, using either `age` or old-school GPG:

[age](https://github.com/FiloSottile/age) public key: age1cf6rqud3rz47v227r8zs3z65wj5ey9wqy0eehmcwsm6esvyue3vqvaavg9

GPG key:

```text
pub   ed25519 2025-12-15 [SC] [expires: 2028-12-14]
      B2AAA7A4D276F5F9674C7A4D36CBECE1C28BE080
uid           ThorSec <security@thorchain.org>
sub   cv25519 2025-12-15 [E] [expires: 2028-12-14]
```

## Categories

Critical - any bug that could lead to loss of funds will pay out 10% of the value at risk, up to a cap of $1MM.

Any other type of bug will be considered on the normal spectrum of low/medium/high, and the bounty will be determined
by a function of the total impact to the network. For example, a bug that crashes nodes and corrupts state will be
a higher bounty than a bug that crashes nodes but does not require state to be unwound.

## Code in scope

This bounty program exclusively covers the core protocol, specifically the following repositories:

- https://gitlab.com/thorchain/thornode (Note that thornode now contains the on-chain router contracts, and go-tss)
- https://gitlab.com/thorchain/devops/node-launcher
- https://gitlab.com/thorchain/tss/tss-lib

The THORChain protocol, by design, is a base layer on top of which many interfaces are built.
While these interfaces/wallets/UIs are not covered by this bounty program, the security team will help triage and
make contact for these ecosystem projects if you are having trouble contacting them directly.
