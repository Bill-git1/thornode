# Constants and Mimir

## Overview

The network launched with a set number of constants, which have not changed. Constants can be overridden via Mimir and nodes have the ability to [vote on](../thornodes/overview.md#node-voting) and change Mimir values. \
See [Halt Management](./concepts/network-halts.md) for halt and pause specific settings.

Mimir setting can be created and changed without a corresponding Constant.

Mimirs have a maximum length of 128 bytes (see MaxMimirLength in THORNode code).

### Values

- Constant Values:[https://midgard.ninerealms.com/v2/thorchain/constants](https://thornode.ninerealms.com/thorchain/constants)
- Mimir Values: [https://thornode.ninerealms.com/thorchain/mimir](https://thornode.ninerealms.com/thorchain/mimir)

### Key

- No Symbol - Constant only, no Mimir override.
- Star (\*) indicates a Mimir override of a Constant.
- Hash (#) indicates Mimir with no Constant.

## Outbound Transactions

- `OutboundTransactionFee`: Amount of rune to withhold on all outbound transactions (1e8 notation)
- `RescheduleCoalesceBlocks`\*: The number of blocks to coalesce rescheduled outbounds
- `MaxOutboundAttempts`: The maximum retries to reschedule a transaction

### Scheduled Outbound

- `MaxTxOutOffset`: Maximum number of blocks a scheduled outbound transaction can be delayed
- `MinTxOutVolumeThreshold`: Quantity of outbound value (in 1e8 rune) in a block before it's considered "full" and additional value is pushed into the next block
- `TxOutDelayMax`: Maximum number of blocks a scheduled transaction can be delayed
- `TxOutDelayRate`\*: Rate of which scheduled transactions are delayed

## Swapping

- `HaltTrading`#: Pause all trading
- `Halt<chain>Trading`#: Pause trading on a specific chain
- `MaxSwapsPerBlock`: Artificial limit on the number of swaps that a single block with process
- `MinSwapsPerBlock`: Process all swaps if the queue is equal to or smaller than this number
- `EnableDerivedAssets`: Enable/disable derived asset swapping (excludes lending)
- `StreamingSwapMinBPFee`\*: Minimum swap fee (in basis points) for a streaming swap trade
- `StreamingSwapMaxLength`: Maximum number of blocks a streaming swap can trade for
- `StreamingSwapMaxLengthNative`\*: Maximum number of blocks native streaming swaps can trade over
- `TradeAccountsEnabled`: Enable/disable trade account
- `CloutReset`: The number of blocks before clout spent gets reset
- `CloutLimit`\*: Max clout allowed to spend
- `MultipleAffiliatesMaxCount`: Maximum number of nested affiliates
- `L1SlipMinBps`: Minimum L1 asset swap fee in basis points
- `TradeAccountsSlipMinBps`: Minimum trade asset swap fee in basis points
- `SecuredAssetSlipMinBps`: Minimum secured asset swap fee in basis points
- `SynthSlipMinBps`: Minimum synth asset swap fee in basis points
- `DerivedSlipMinBps`: Minimum derived asset swap fee in basis points

## TCY Management

- `MinRuneForTCYStakeDistribution`: Minimum RUNE required in the TCY fund to be eligible for distribution (default: 2,100 RUNE (210000000000 in 1e8 notation))
- `MinTCYForTCYStakeDistribution`: Minimum TCY required in the TCY fund to be eligible for distribution (default: 100,000 TCY)
- `TCYStakeSystemIncomeBps`: Percentage (in basis points) of system income allocated to the TCY fund (default: 1000 bps = 10%)
- `TCYClaimingSwapHalt`: Enables/disables RUNE-to-TCY swaps in the claiming module (default: 1, halted)
- `TCYStakeDistributionHalt`: Enables/disables distribution of RUNE revenue to TCY stakers (default: 1, halted)
- `TCYStakingHalt`: Enables/disables staking of TCY tokens (default: 1, halted)
- `TCYUnstakingHalt`: Enables/disables unstaking of TCY tokens (default: 1, halted)
- `TCYClaimingHalt`: Enables/disables claiming of TCY tokens for THORFi deposits (default: 1, halted)

## Secured Asset Management

- `HaltSecuredGlobal`#: Halts operations for all secured assets across both the Base and App Layers
- `HaltSecuredDeposit-<Chain>`#: Disables deposit of secured assets on the specified chain
- `HaltSecuredWithdraw-<Chain>`#: Disables withdrawal of secured asset on the specified chain

## App Layer Management

- `WasmPermissionless`#: Toggles permissionless deployment of CosmWasm smart contracts (default: 0, restricted).
- `WasmMinGasPrice`#: Sets the minimum gas price for CosmWasm transactions in the App Layer in RUNE.
- `HaltWasmGlobal`#: Pauses all CosmWasm smart contract executions in the App Layer.
- `HaltWasmCs-<checksum>`#: Halts a specific CosmWasm contract by its base32-encoded checksum.
- `HaltWasmContract-<address suffix>`#: Halts a specific CosmWasm contract using the last 6 characters of its address.

## LP Management

- `PauseLP`#: Pauses the ability for LPs to add/remove liquidity
- `PauseLP<chain>`#: Pauses the ability for LPs to add/remove liquidity, per chain
- `MaximumLiquidityRune`#: Maximum RUNE capped on the pools known as the ‘soft cap’
- `LiquidityLockUpBlocks`: The number of blocks an LP must wait before they can withdraw their liquidity
- `PendingLiquidityAgeLimit`: The number of blocks the network waits before initiating pending liquidity cleanup. Cleanup of all pools lasts for the same duration.
- `PauseAsymWithdrawal-<Chain>`#: Forces dual-address liquidity providers to withdraw symmetrically rather than asymmetrically.
- `PauseLPDeposit-<Asset>`#: pauses the ability to add liquidity into that pool. E.g. `PAUSELPDEPOSIT-BTC-BTC=1` suspends deposits for the BTC pool

## RunePool

- `RUNEPoolEnabled`: Enable/disable RUNE Pool
- `RUNEPoolDepositMaturityBlocks`: Minimum number of blocks from last RUNEPool deposit when a withdraw is allowed.
- `RUNEPoolMaxReserveBackstop`: Max amount of RUNE above the `POLMaxNetworkDeposit` that the reserve can enter RunePool before withdrawals are disabled.

## Chain Management

- `HaltChainGlobal`#: Pause observations on all chains (chain clients)
- `HaltTrading`: Stops swaps and additions, if done, will result in refunds. Observations still occur.
- `Halt<chain>Chain`#: Pause a specific blockchain
- `SolvencyHalt<Chain>`# Halts a chain if the solvency checker for that chain fails.
- `NodePauseChainGlobal`#: Individual node controlled means to pause all chains
- `NodePauseChainBlocks`: Number of block a node operator can pause/resume the chains for
- `BlocksPerYear`: Blocks in a year
- `MaxUTXOsToSpend`#: Max UTXOs to be spent in one block
- `MinimumNodesForBFT`: Minimum node count to keep the network running. Below this, Ragnarök is performed
- `MaxConfirmations-<Chain>`# : The maximum number of confirmations for a chain
- `ConfMultiplierBasisPoints-<Chain>`#: Increases or decrease the inbound confirmation count block requirement for a chain

### Fee Management

- `NativeTransactionFee`: RUNE fee on all on chain txs
- `TNSRegisterFee`: Registration fee for new THORName, in RUNE
- `TNSFeeOnSale`: fee for TNS sale in basis points
- `TNSFeePerBlock`: per block cost for TNS, in RUNE
- `PreferredAssetOutboundFeeMultiplier`\*: The multiplier of the current preferred asset outbound fee, if RUNE balance > multiplier \* outbound_fee, a preferred asset swap is triggered
- `MinOutboundFeeMultiplierBasisPoints`\*: Minimum multiplier applied to base outbound fee charged to user, in basis points
- `MaxOutboundFeeMultiplierBasisPoints`: Maximum multiplier applied to base outbound fee charged to user, in basis points

### Solvency Checker

- `StopSolvencyCheck`#: Enable/Disable Solvency Checker
- `StopSolvencyCheck<chain>`#: Enable/Disable Solvency Checker, per chain
- `PermittedSolvencyGap`: The amount of funds permitted to be "insolvent". This gives the network a little bit of "wiggle room" for margin of error

## Node Management

- `MinimumBondInRune`\*: Sets a lower bound on bond for a node to be considered to be churned in
- `ValidatorMaxRewardRatio`\*: the ratio to MinimumBondInRune at which validators stop receiving rewards proportional to their bond
- `MaxBondProviders`\*: Maximum number of bond providers per mode
- `NodeOperatorFee`\*: Minimum node operator fee
- `SignerConcurrency`\*: Number of concurrent signers for active and retiring vaults

### Slashing Management

- `LackOfObservationPenalty`: Add two slash points for each block where a node does not observe
- `SigningTransactionPeriod`: How many blocks before a request to sign a tx by yggdrasil pool, is counted as delinquent.
- `DoubleSignMaxAge`: Number of blocks to limit double signing a block
- `FailKeygenSlashPoints`: Slash for 720 blocks, which equals 1 hour
- `FailKeysignSlashPoints`: Slash for 2 blocks
- `ObserveSlashPoints`: The number of slashpoints for making an observation (redeems later if observation reaches consensus)
- `ObservationDelayFlexibility`: Number of blocks of flexibility for a validator to get their slash points taken off for making an observation
- `JailTimeKeygen`: Blocks a node account is jailed for failing to keygen. DO NOT drop below TSS timeout
- `JailTimeKeysign`: Blocks a node account is jailed for failing to keysign. DO NOT drop below TSS timeout
- `BondSlashBan`: RUNE amount to slash bond of banned node

### Churning

- `AsgardSize`\*: Defines the number of members to an Asgard vault
- `MinSlashPointsForBadValidator`: Minimum quantity of slash points needed to be considered "bad" and be marked for churn out
- `BondLockupPeriod`: Lockout period that a node must wait before being allowed to unbond
- `ChurnInterval`\*: Number of blocks between each churn
- `HaltChurning`: Pause churning
- `DesiredValidatorSet`\*: Maximum number of validators
- `FundMigrationInterval`\*: Number of blocks between attempts to migrate funds between asgard vaults during a migration
- `NumberOfNewNodesPerChurn`#: Number of targeted additional nodes added to the validator set each churn
- `BadValidatorRedline`\*: Redline multiplier to find a multitude of bad actors
- `LowBondValidatorRate`: Rate to mark a validator to be rotated out for low bond
- `MaxNodeToChurnOutForLowVersion`\*: Maximum number of validators to churn out for low version each churn
- `MigrationVaultSecurityBps`: Vault bond must be greater than bps of funds value in rune to receive migrations

## Economics

- `EmissionCurve`\*: How quickly rune is emitted from the reserve in block rewards
- `MaxAvailablePools`: Maximum number of pools allowed on the network. Gas pools (native pools) are excluded from this.
- `MinRunePoolDepth`\*: Minimum number of RUNE to be considered to become active
- `PoolCycle`\*: Number of blocks the network will churn the pools (add/remove new available pools)
- `StagedPoolCost`: Number of RUNE (1e8 notation) that a staged pool is deducted on each pool cycle.
- `KillSwitchStart`\*: Block height to start to kill BEP2 and ERC20 RUNE
- `KillSwitchDuration`: Duration (in blocks) until switching is deprecated
- `MinimumPoolLiquidityFee`: Minimum liquidity fee an active pool should accumulate to avoid being demoted, set to 0 to disable demote pool based on liquidity fee
- `MaxRuneSupply`\*: Maximum supply of RUNE
- `PendulumUseEffectiveSecurity`: Determines the [Incentive Pendulum](./concepts/incentive-pendulum.md) perception of the `securing`. If set to 1, `Effective Security Bond` is used; otherwise `Total Effective Bond` is applied.
- `PendulumUseVaultAssets`: Determines the [Incentive Pendulum](./concepts/incentive-pendulum.md) perception of the `securing`. If set to 1, `Total Pooled` is used; otherwise `Vaulted Assets` is applied.
- `PendulumAssetsBasisPoints`: Scales the Incentive Pendulum perception of the `secured` L1 asset size, where values above 100% overestimate and values below 100% underestimate the amount of `secured` assets.
- `TVLCapBasisPoints`\*: If 0, TVL Cap is set to the effective active bond. If non-zero, the value is interrupted as basis points relative to total active bond.

## Attestation Gossip

- `AttestationMaxBatchSize`: Maximum attestations to send in a batch
- `AttestationPeerConcurrentSends`: Maximum batches to concurrently send to peer
- `AttestationPeerConcurrentReceives`: Maximum batches to concurrently receive from a peer

## Miscellaneous

- `DollarsPerRune`: Manual override of number of dollars per one RUNE. Used for metrics data collection and RUNE calculation from MinimumL1OutboundFeeUSD
- `THORNames`: Enable/Disable THORNames
- `TNSRegisterFee`: TNS registration fee of new names
- `TNSFeePerBlock`: TNS cost per block to retain ownership of a name
- `ArtificialRagnarokBlockHeight`: Triggers a chain shutdown and ragnarok
- `NativeTransactionFee`: The RUNE fee for a native transaction (gas cost in 1e8 notation)
- `HALTSIGNING<chain>`#: Halt signing in a specific chain
- `HALTSIGNING#`: Halt signing globally
- `Ragnarok-<Asset>`#: Ragnaroks a specific pool
- `BankSendEnabled`: Enable/Disable cosmos bank send messages
- `ObservationDelayFlexibility`\*: Number of blocks of flexibility for a validator to get their slash points taken off for making an observation

### Router Upgrading (DO NOT TOUCH!)

#### Old keys (pre 1.94.0)

- `MimirRecallFund`: Recalls Chain funds, typically used for router upgrades only
- `MimirUpgradeContract`: Upgrades contract, typically used for router upgrades only

#### New keys (1.94.0 and on)

- `MimirRecallFund<CHAIN>`: Recalls Chain funds, typically used for router upgrades only
- `MimirUpgradeContract<CHAIN>`: Upgrades contract, typically used for router upgrades only
