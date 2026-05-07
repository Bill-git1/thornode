package thorchain

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"cosmossdk.io/core/comet"
	"github.com/cometbft/cometbft/crypto"
	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/hashicorp/go-metrics"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
	mem "gitlab.com/thorchain/thornode/v3/x/thorchain/memo"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// Slasher is current implementation of slasher
type SlasherImpl struct {
	keeper   keeper.Keeper
	eventMgr EventManager
}

// newSlasher create a new instance of Slasher
func newSlasher(keeper keeper.Keeper, eventMgr EventManager) *SlasherImpl {
	return &SlasherImpl{keeper: keeper, eventMgr: eventMgr}
}

// BeginBlock called when a new block get proposed to detect whether there are duplicate vote
func (s *SlasherImpl) BeginBlock(ctx cosmos.Context, constAccessor constants.ConstantValues) {
	var doubleSignEvidence []comet.Evidence
	// Iterate through any newly discovered evidence of infraction
	// Slash any validators (and since-unbonded liquidity within the unbonding period)
	// who contributed to valid infractions
	for i := range ctx.CometInfo().GetEvidence().Len() {
		evidence := ctx.CometInfo().GetEvidence().Get(i)
		switch evidence.Type() {
		case comet.DuplicateVote:
			doubleSignEvidence = append(doubleSignEvidence, evidence)
		default:
			ctx.Logger().Error("ignored unknown evidence type", "type", evidence.Type)
		}
	}

	// Identify validators which didn't sign the previous block
	var missingSignAddresses []crypto.Address
	var successfulSignAddresses []crypto.Address
	for i := range ctx.CometInfo().GetLastCommit().Votes().Len() {
		voteInfo := ctx.CometInfo().GetLastCommit().Votes().Get(i)
		if voteInfo.GetBlockIDFlag() != comet.BlockIDFlagAbsent {
			successfulSignAddresses = append(successfulSignAddresses, voteInfo.Validator().Address())
		} else {
			missingSignAddresses = append(missingSignAddresses, voteInfo.Validator().Address())
		}
	}

	// Derive Active node validator addresses once.
	nas, err := s.keeper.ListActiveValidators(ctx)
	if err != nil {
		ctx.Logger().Error("fail to list active validators", "error", err)
		return
	}
	var validatorAddresses []nodeAddressValidatorAddressPair
	for _, na := range nas {
		pk, err := cosmos.GetPubKeyFromBech32(cosmos.Bech32PubKeyTypeConsPub, na.ValidatorConsPubKey)
		if err != nil {
			ctx.Logger().Error("fail to derive validator address", "error", err)
			continue
		}
		var pair nodeAddressValidatorAddressPair
		pair.nodeAddress = na.NodeAddress
		pair.validatorAddress = pk.Address()
		validatorAddresses = append(validatorAddresses, pair)
	}

	// Act on double signs.
	for _, evidence := range doubleSignEvidence {
		if err := s.HandleDoubleSign(ctx, evidence.Validator().Address(), evidence.Height(), constAccessor, validatorAddresses); err != nil {
			ctx.Logger().Error("fail to slash for double signing a block", "error", err)
		}
	}

	// Act on missing signs.
	for _, missingSignAddress := range missingSignAddresses {
		if err := s.HandleMissingSign(ctx, missingSignAddress, constAccessor, validatorAddresses); err != nil {
			ctx.Logger().Error("fail to slash for missing signing a block", "error", err)
		}
	}

	// Act on successful signs.
	for _, successfulSignAddress := range successfulSignAddresses {
		if err := s.HandleSuccessfulSign(ctx, successfulSignAddress, constAccessor, validatorAddresses); err != nil {
			ctx.Logger().Error("fail to mark for successfully signing a block", "error", err)
		}
	}
}

// HandleDoubleSign - slashes a validator for signing two blocks at the same
// block height
// https://blog.cosmos.network/consensus-compare-casper-vs-tendermint-6df154ad56ae
func (s *SlasherImpl) HandleDoubleSign(ctx cosmos.Context, addr crypto.Address, infractionHeight int64, constAccessor constants.ConstantValues, validatorAddresses []nodeAddressValidatorAddressPair) error {
	// check if we're recent enough to slash for this behavior
	maxAge := constAccessor.GetInt64Value(constants.DoubleSignMaxAge)
	if (ctx.BlockHeight() - infractionHeight) > maxAge {
		ctx.Logger().Info("double sign detected but too old to be slashed", "infraction height", infractionHeight, "address", addr.String())
		return nil
	}

	doubleBlockSignSlashPoints := s.keeper.GetConfigInt64(ctx, constants.DoubleBlockSignSlashPoints)

	// First search active validators, then fall back to all validators with bond
	// to handle churned-out validators whose evidence arrives after churn.
	var nodeAddress cosmos.AccAddress
	for _, pair := range validatorAddresses {
		if addr.String() == pair.validatorAddress.String() {
			nodeAddress = pair.nodeAddress
			break
		}
	}
	if nodeAddress.Empty() {
		allValidators, err := s.keeper.ListValidatorsWithBond(ctx)
		if err != nil {
			return fmt.Errorf("fail to list validators with bond: %w", err)
		}
		for _, na := range allValidators {
			pk, err := cosmos.GetPubKeyFromBech32(cosmos.Bech32PubKeyTypeConsPub, na.ValidatorConsPubKey)
			if err != nil {
				continue
			}
			if addr.String() == pk.Address().String() {
				nodeAddress = na.NodeAddress
				break
			}
		}
	}
	if nodeAddress.Empty() {
		return fmt.Errorf("could not find node account with validator address: %s", addr)
	}

	na, err := s.keeper.GetNodeAccount(ctx, nodeAddress)
	if err != nil {
		return err
	}

	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
		telemetry.NewLabel("address", na.NodeAddress.String()),
		telemetry.NewLabel("reason", "double_block_sign"),
	}))
	if err := s.keeper.IncNodeAccountSlashPoints(slashCtx, na.NodeAddress, doubleBlockSignSlashPoints); err != nil {
		return fmt.Errorf("fail to increase node account slash point: %w", err)
	}

	return s.keeper.SetNodeAccount(ctx, na)
}

// HandleSuccessfulSign - decrement missing blocks from a validator for signing a block
func (s *SlasherImpl) HandleSuccessfulSign(ctx cosmos.Context, addr crypto.Address, constAccessor constants.ConstantValues, validatorAddresses []nodeAddressValidatorAddressPair) error {
	for _, pair := range validatorAddresses {
		if addr.String() != pair.validatorAddress.String() {
			continue
		}

		na, err := s.keeper.GetNodeAccount(ctx, pair.nodeAddress)
		if err != nil {
			return err
		}

		if na.MissingBlocks == 0 {
			return nil
		}

		// decrement the number of blocks that weren't signed
		na.MissingBlocks -= 1

		return s.keeper.SetNodeAccount(ctx, na)
	}

	return fmt.Errorf("could not find active node account with validator address: %s", addr)
}

// HandleMissingSign - slashes a validator for not signing a block
func (s *SlasherImpl) HandleMissingSign(ctx cosmos.Context, addr crypto.Address, constAccessor constants.ConstantValues, validatorAddresses []nodeAddressValidatorAddressPair) error {
	missBlockSignSlashPoints := s.keeper.GetConfigInt64(ctx, constants.MissBlockSignSlashPoints)
	maxTrack := s.keeper.GetConfigInt64(ctx, constants.MaxTrackMissingBlock)

	for _, pair := range validatorAddresses {
		if addr.String() != pair.validatorAddress.String() {
			continue
		}

		na, err := s.keeper.GetNodeAccount(ctx, pair.nodeAddress)
		if err != nil {
			return err
		}

		slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
			telemetry.NewLabel("address", na.NodeAddress.String()),
			telemetry.NewLabel("reason", "miss_block_sign"),
		}))
		if err := s.keeper.IncNodeAccountSlashPoints(slashCtx, na.NodeAddress, missBlockSignSlashPoints); err != nil {
			ctx.Logger().Error("fail to increase node account slash points", "error", err, "address", na.NodeAddress.String())
		}

		// increment the number of blocks that weren't signed
		na.MissingBlocks += 1
		if na.MissingBlocks > uint64(maxTrack) {
			na.MissingBlocks = uint64(maxTrack)
		}

		return s.keeper.SetNodeAccount(ctx, na)
	}

	return fmt.Errorf("could not find active node account with validator address: %s", addr)
}

// xmrSigningPeriodMultiplier scales SigningTransactionPeriod for XMR
// outbounds. FROST threshold signing is a multi-round p2p protocol that
// takes noticeably longer than ECDSA/EDDSA keysign, and XMR chain
// confirmation + observation round-trip adds further delay. Processing
// XMR outbounds at the same window as other chains races the slasher
// against in-flight signatures — the ToI gets rescheduled before its
// observation arrives, and when the original vault's signed tx finally
// lands on-chain thornode emits a "missing tx out" security event.
// Process XMR outbounds from a 3x-older batch to give FROST and chain
// confirmation enough headroom.
const xmrSigningPeriodMultiplier = 3

// LackSigning slash account that fail to sign tx
func (s *SlasherImpl) LackSigning(ctx cosmos.Context, mgr Manager) error {
	var resultErr error
	maxOutboundAttempts := mgr.Keeper().GetConfigInt64(ctx, constants.MaxOutboundAttempts)
	signingTransPeriod := mgr.Keeper().GetConfigInt64(ctx, constants.SigningTransactionPeriod)
	if signingTransPeriod == 0 {
		return fmt.Errorf("invalid signing transaction period: %d", signingTransPeriod)
	}
	enableMemolessOutbound := mgr.Keeper().GetConfigInt64(ctx, constants.EnableMemolessOutbound)

	if ctx.BlockHeight() < signingTransPeriod {
		return nil
	}
	// round up to nearest multiple of RescheduleCoalesceBlocks
	rescheduleHeight := ctx.BlockHeight()
	rescheduleCoalesceBlocks := mgr.Keeper().GetConfigInt64(ctx, constants.RescheduleCoalesceBlocks)
	if rescheduleCoalesceBlocks > 1 {
		overBlocks := rescheduleHeight % rescheduleCoalesceBlocks
		if overBlocks != 0 {
			rescheduleHeight += rescheduleCoalesceBlocks - overBlocks
		}
	}

	// Each pass examines a different TxOut batch and processes items whose
	// chain matches the pass. Non-XMR chains are checked at the standard
	// SigningTransactionPeriod. XMR outbounds are checked at a longer
	// period (see xmrSigningPeriodMultiplier above).
	xmrSigningTransPeriod := signingTransPeriod * xmrSigningPeriodMultiplier
	type rescheduleBatch struct {
		height  int64
		wantXMR bool
	}
	batches := []rescheduleBatch{
		{height: ctx.BlockHeight() - signingTransPeriod, wantXMR: false},
	}
	if ctx.BlockHeight() >= xmrSigningTransPeriod {
		batches = append(batches, rescheduleBatch{
			height:  ctx.BlockHeight() - xmrSigningTransPeriod,
			wantXMR: true,
		})
	}

	for _, batch := range batches {
		height := batch.height
		wantXMR := batch.wantXMR
		txs, err := s.keeper.GetTxOut(ctx, height)
		if err != nil {
			return fmt.Errorf("fail to get txout from block height(%d): %w", height, err)
		}

		for i, toi := range txs.TxArray {
			if toi.Chain.Equals(common.XMRChain) != wantXMR {
				// XMR items are processed in the extended-period pass;
				// non-XMR items are processed in the standard pass.
				continue
			}
			if !common.CurrentChainNetwork.SoftEquals(toi.ToAddress.GetNetwork(toi.Chain)) {
				continue // skip this transaction
			}
			if toi.OutHash.IsEmpty() {
				// Slash node account for not sending funds
				vault, err := s.keeper.GetVault(ctx, toi.VaultPubKey)
				if err != nil {
					ctx.Logger().Error("Unable to get vault", "error", err, "vault pub key", toi.VaultPubKey.String())
					resultErr = fmt.Errorf("failed to get vault: %w", err)
					continue
				}

				// if the vault is frozen, reschedule to same vault with no changes
				frozen := false
				if len(vault.Frozen) > 0 {
					var chains common.Chains
					chains, err = common.NewChains(vault.Frozen)
					if err != nil {
						ctx.Logger().Error("failed to convert chains", "error", err)
					}
					if chains.Has(toi.Coin.Asset.GetChain()) {
						etx := common.Tx{
							ID:        toi.InHash,
							Chain:     toi.Chain,
							ToAddress: toi.ToAddress,
							Coins:     []common.Coin{toi.Coin},
							Gas:       toi.MaxGas,
							Memo:      toi.Memo,
						}
						eve := NewEventSecurity(etx, "frozen vault reschedule")
						if err = mgr.EventMgr().EmitEvent(ctx, eve); err != nil {
							ctx.Logger().Error("fail to emit security event", "error", err)
						}
						frozen = true
					}
				}

				memo, _ := ParseMemoWithTHORNames(ctx, s.keeper, toi.GetMemo()) // ignore err
				if memo.IsInternal() {
					// there is a different mechanism for rescheduling outbound
					// transactions for migration transactions
					continue
				}
				var voter ObservedTxVoter
				if memo.IsType(TxRagnarok) {
					// A Ragnarok outbound has no ObservedTxInVoter,
					// so check MaxOutboundAttempts against the memo height.
					ragnarokHeight := memo.GetBlockHeight()
					// Though a negative is not expected to cause problems,
					// nevertheless keep the default 0 as the minimum.
					if ragnarokHeight > 0 {
						voter.FinalisedHeight = ragnarokHeight
					}
				} else {
					voter, err = s.keeper.GetObservedTxInVoter(ctx, toi.InHash)
					if err != nil {
						ctx.Logger().Error("fail to get observed tx voter", "error", err)
						resultErr = fmt.Errorf("failed to get observed tx voter: %w", err)
						continue
					}
				}

				// If vault is inactive, check if it still has sufficient funds to fulfill
				// the outbound. If so, reschedule to the same vault (retired vault recovery).
				// If the vault has been drained (e.g. migration completed before this outbound
				// was signed), fall through to active vault selection so the outbound can be
				// fulfilled from a vault that actually holds the funds.
				if vault.Status == InactiveVault {
					maxRetiredVaultRecoveryAttempts := mgr.Keeper().GetConfigInt64(ctx, constants.MaxRetiredVaultRecoveryAttempts)
					age := ctx.BlockHeight() - voter.FinalisedHeight
					attempts := age / signingTransPeriod
					if attempts >= maxRetiredVaultRecoveryAttempts {
						ctx.Logger().Info("too many attempts for retired vault recovery",
							"hash", toi.InHash,
							"attempts", attempts,
							"max", maxRetiredVaultRecoveryAttempts)
						continue
					}

					// Check if the inactive vault has enough funds for the outbound.
					// Include gas cost when the gas asset is the same as the outbound asset.
					requiredAmount := toi.Coin.Amount
					for _, gas := range toi.MaxGas {
						if gas.Asset.Equals(toi.Coin.Asset) {
							requiredAmount = requiredAmount.Add(gas.Amount)
						}
					}
					vaultBalance := vault.GetCoin(toi.Coin.Asset).Amount
					if vaultBalance.GTE(requiredAmount) {
						// Vault still has funds — reschedule to the same vault for signing.
						err = mgr.TxOutStore().UnSafeAddTxOutItem(ctx, mgr, toi, rescheduleHeight)
						if err != nil {
							ctx.Logger().Error("fail to add outbound to queue for retired vault recovery", "error", err)
							resultErr = fmt.Errorf("failed to add outbound to queue for retired vault recovery: %w", err)
						} else {
							txs.TxArray[i].OutHash = common.BlankTxID
						}
						continue
					}

					// Vault has been drained — fall through to active vault selection.
					ctx.Logger().Info("inactive vault has insufficient funds, reassigning to active vault",
						"hash", toi.InHash,
						"vault", vault.PubKey,
						"asset", toi.Coin.Asset,
						"required", requiredAmount,
						"available", vaultBalance)
				}

				if maxOutboundAttempts > 0 {
					age := ctx.BlockHeight() - voter.FinalisedHeight
					attempts := age / signingTransPeriod
					if attempts >= maxOutboundAttempts {
						// Skip treasury recovery if there's already an outbound associated or
						// at least one observation of the outbound (outbound is in progress)
						hasOutboundAssociated := len(voter.OutTxs) > 0
						outboundObservations := mgr.Keeper().GetObservedLink(ctx, toi.InHash)
						hasOutboundObservation := len(outboundObservations) > 0
						if hasOutboundAssociated || hasOutboundObservation {
							ctx.Logger().Info("skipping treasury recovery, outbound in progress",
								"hash", toi.InHash,
								"has_outbound", hasOutboundAssociated,
								"has_observation", hasOutboundObservation)
							continue
						}

						// For user-facing outbounds (refund, outbound, ragnarok), send to treasury recovery
						if memo.IsOutbound() {
							treasuryErr := s.sendFailedOutboundToTreasury(ctx, mgr, toi, vault, memo)
							if treasuryErr != nil {
								ctx.Logger().Error("failed to send outbound to treasury recovery", "error", treasuryErr, "hash", toi.InHash)
							} else {
								txs.TxArray[i].OutHash = common.BlankTxID
							}
						} else {
							// For non-outbound transactions (shouldn't happen often), just log and drop
							ctx.Logger().Info("txn dropped, too many attempts (non-outbound)", "hash", toi.InHash)
						}
						continue
					}
				}

				if !frozen && s.needsNewVault(ctx, mgr, vault, signingTransPeriod, voter.FinalisedHeight, toi) {
					var active types.Vaults
					active, err = s.keeper.GetAsgardVaultsByStatus(ctx, ActiveVault)
					if err != nil {
						return fmt.Errorf("fail to get active asgard vaults: %w", err)
					}
					active = filterActiveVaultsForOutboundChain(active, toi.Chain)
					// Deduct the asset's pending outbound funds to represent only the available funds.
					pendingOutbounds := mgr.Keeper().GetPendingOutbounds(ctx, toi.Coin.Asset)
					for i := range active {
						active[i].DeductVaultPendingOutbounds(pendingOutbounds)

						// If the currently-assigned vault is an ActiveVault and the only one with enough funds for the outbound,
						// the item should be reassigned to the same vault rather than assigned to another vault without enough funds;
						// this is especially important for GAIA outbounds, for which insufficient-funds failures
						// have THORChain-unobserved gas costs (causing churn-migration-jamming vault insolvency).
						// As such, re-add the (now free) funds of the outbound being replaced.
						if active[i].PubKey.Equals(toi.VaultPubKey) {
							active[i].Coins = active[i].Coins.Add(toi.Coin)
							active[i].Coins = active[i].Coins.Add(toi.MaxGas...)
						}
					}

					available := active
					mainCoin := toi.Coin
					var maxGasCoin common.Coin
					maxGasCoin, err = mgr.GasMgr().GetMaxGas(ctx, toi.Chain)
					if err != nil {
						ctx.Logger().Error("fail to get max gas", "error", err)
					}
					if mainCoin.Asset.Equals(maxGasCoin.Asset) {
						// If the main coin and the gas coin are the same asset,
						// ensure the assigned vault has enough available for both.
						mainCoin.Amount = mainCoin.Amount.Add(maxGasCoin.Amount)
					} else {
						// If the gas coin isn't the main asset,
						// directly ensure the assigned vault has enough available for it.
						available = active.Has(maxGasCoin)
					}
					available = available.Has(mainCoin)

					if len(available) == 0 {
						// we need to give it somewhere to send from, even if that
						// asgard doesn't have enough funds. This is because if we
						// don't the transaction will just be dropped on the floor,
						// which is bad. Instead it may try to send from an asgard that
						// doesn't have enough funds, fail, and then get rescheduled
						// again later. Maybe by then the network will have enough
						// funds to satisfy.
						// TODO add split logic to send it out from multiple asgards in
						// this edge case.
						ctx.Logger().Error("unable to determine asgard vault to send funds, trying first asgard")
						if len(active) > 0 {
							// Fall back on the vault with the most available funds.
							vault = active.SortBy(mainCoin.Asset)[0]
						}
					} else {
						// Use InHash for vault selection if available, otherwise use block height.
						// For internal protocol transactions (ragnarok, migrations, etc.), InHash
						// is set to BlankTxID since there is no associated inbound transaction.
						inHashValue := toi.InHash.Int64()
						rep := int(inHashValue + ctx.BlockHeight()/signingTransPeriod)
						if vault.PubKey.Equals(available[rep%len(available)].PubKey) {
							// looks like the new vault is going to be the same as the
							// old vault, increment rep to ensure a differ asgard is
							// chosen (if there is more than one option)
							rep++
						}
						vault = available[rep%len(available)]
					}
					if !memo.IsType(TxRagnarok) {
						// update original toi action in observed tx
						// check observedTx has done status. Skip if it does already.
						voterTx := voter.GetTx(NodeAccounts{})
						if voterTx.IsDone(len(voter.Actions)) {
							if len(voterTx.OutHashes) > 0 && len(voterTx.GetOutHashes()) > 0 {
								txs.TxArray[i].OutHash = voterTx.GetOutHashes()[0]
							}
							continue
						}

						// update the actions in the voter with the new vault pubkey
						for i, action := range voter.Actions {
							if action.Equals(toi) {
								voter.Actions[i].VaultPubKey = vault.PubKey
								voter.Actions[i].VaultPubKeyEddsa = txOutVaultPubKeyEddsa(toi.Chain, vault.PubKeyEddsa)
								voter.Actions[i].VaultPubKeyFrost = vault.PubKeyFrost
								if toi.Aggregator != "" || toi.AggregatorTargetAsset != "" || toi.AggregatorTargetLimit != nil {
									ctx.Logger().Info("clearing aggregator fields on outbound reassignment", "hash", toi.InHash)

									// Here, simultaneously clear the Aggregator information for a reassigned TxOutItem and its Actions item
									// so that a SwapOut will send the THORChain output asset instead of cycling and swallowingif
									// (and maybe failing with slashes) if something goes wrong.
									toi.Aggregator = ""
									toi.AggregatorTargetAsset = ""
									toi.AggregatorTargetLimit = nil
									voter.Actions[i].Aggregator = ""
									voter.Actions[i].AggregatorTargetAsset = ""
									voter.Actions[i].AggregatorTargetLimit = nil
								}
							}
						}
						s.keeper.SetObservedTxInVoter(ctx, voter)

					}
					// Save the toi to as a new toi, select Asgard to send it this time.
					toi.VaultPubKey = vault.PubKey
					toi.VaultPubKeyEddsa = txOutVaultPubKeyEddsa(toi.Chain, vault.PubKeyEddsa)
					toi.VaultPubKeyFrost = vault.PubKeyFrost

					// update max gas - update voter first, only update txOut if voter succeeds
					if !maxGasCoin.IsEmpty() {
						if err = updateTxOutGas(ctx, s.keeper, toi, common.Gas{maxGasCoin}); err != nil {
							ctx.Logger().Error("Failed to update MaxGas of action in ObservedTxVoter", "hash", toi.InHash, "error", err)
						} else {
							toi.MaxGas = common.Gas{maxGasCoin}
						}
					}
					// Equals checks GasRate so update actions GasRate too (before updating in the queue item)
					// for future updates of MaxGas, which must match for matchActionItem in AddOutTx.
					// Only update txOut GasRate if voter update succeeds to prevent permanent desync.
					gasRate := int64(mgr.GasMgr().GetGasRate(ctx, toi.Chain).Uint64())
					if err = updateTxOutGasRate(ctx, s.keeper, toi, gasRate); err != nil {
						ctx.Logger().Error("Failed to update GasRate of action in ObservedTxVoter", "hash", toi.InHash, "error", err)
					} else {
						toi.GasRate = gasRate
					}
				}

				// if a pool with the asset name doesn't exist, skip rescheduling
				if !toi.Coin.IsRune() && !s.keeper.PoolExist(ctx, toi.Coin.Asset) {
					ctx.Logger().Error("fail to add outbound to queue", "error", "coin is not rune and does not have an associated pool")
					continue
				}

				// Apply memoless outbound logic before rescheduling
				applyMemolessOutboundLogic(mgr.GetVersion(), &toi, enableMemolessOutbound)

				err = mgr.TxOutStore().UnSafeAddTxOutItem(ctx, mgr, toi, rescheduleHeight)
				if err != nil {
					ctx.Logger().Error("fail to add outbound to queue", "error", err)
					resultErr = fmt.Errorf("failed to add outbound to queue: %w", err)
					continue
				}
				// because the txout item has been rescheduled, thus mark the replaced tx out item as already send out, even it is not
				// in this way bifrost will not send it out again cause node to be slashed
				txs.TxArray[i].OutHash = common.BlankTxID
			}
		}
		if !txs.IsEmpty() {
			if err := s.keeper.SetTxOut(ctx, txs); err != nil {
				return fmt.Errorf("fail to save tx out : %w", err)
			}
		}
	}

	return resultErr
}

// getOriginalSwapDetails retrieves the original swap transaction details for a failed outbound
// Returns the original transaction, swap memo, and any error encountered
func (s *SlasherImpl) getOriginalSwapDetails(ctx cosmos.Context, mgr Manager, inboundHash common.TxID) (ObservedTx, SwapMemo, error) {
	voter, err := mgr.Keeper().GetObservedTxInVoter(ctx, inboundHash)
	if err != nil {
		return ObservedTx{}, SwapMemo{}, fmt.Errorf("could not get observed tx voter: %w", err)
	}

	if voter.Tx.Tx.ID.IsEmpty() {
		return ObservedTx{}, SwapMemo{}, fmt.Errorf("voter tx ID is empty")
	}

	originalMemo, err := ParseMemoWithTHORNames(ctx, mgr.Keeper(), voter.Tx.Tx.Memo)
	if err != nil {
		return ObservedTx{}, SwapMemo{}, fmt.Errorf("could not parse original memo: %w", err)
	}

	if !originalMemo.IsType(TxSwap) {
		return ObservedTx{}, SwapMemo{}, fmt.Errorf("original tx is not a swap (type: %s)", originalMemo.GetType())
	}

	swapMemo, ok := originalMemo.(SwapMemo)
	if !ok {
		return ObservedTx{}, SwapMemo{}, fmt.Errorf("could not cast to SwapMemo")
	}

	if len(voter.Tx.Tx.Coins) == 0 {
		return ObservedTx{}, SwapMemo{}, fmt.Errorf("voter tx has no coins")
	}

	return voter.Tx, swapMemo, nil
}

// generateRecoveryTxID creates a unique transaction ID for a recovery swap by hashing
// the original transaction ID with the recovery type suffix and block height. This prevents
// collisions with the original swap in the swap queue, especially important for streaming swaps
// that may still be in progress when the recovery is triggered.
func (s *SlasherImpl) generateRecoveryTxID(ctx cosmos.Context, originalTxID common.TxID, recoveryType string) common.TxID {
	// Create a unique hash by combining original txID + recovery type + block height
	// This ensures no collision with the original swap queue entry
	hashInput := fmt.Sprintf("%s:%s:%d", originalTxID.String(), recoveryType, ctx.BlockHeight())
	hash := crypto.Sha256([]byte(hashInput))
	return common.TxID(fmt.Sprintf("%X", hash))
}

// queueRecoverySwap is a helper function that queues a swap for failed outbound recovery.
// It handles the common logic of creating a swap transaction, determining the queue version,
// and emitting the appropriate events.
func (s *SlasherImpl) queueRecoverySwap(
	ctx cosmos.Context,
	mgr Manager,
	toi TxOutItem,
	vault Vault,
	txID common.TxID,
	targetAsset common.Asset,
	destination common.Address,
	memo string,
	recoveryType string,
) error {
	// Generate a unique transaction ID for the recovery swap to prevent collisions
	// with the original swap in the queue (especially important for streaming swaps)
	recoveryTxID := s.generateRecoveryTxID(ctx, txID, recoveryType)

	// Get vault's address for the chain of this coin for tracking purposes.
	vaultAddr, err := vault.GetAddress(toi.Coin.Asset.GetChain())
	if err != nil {
		return fmt.Errorf("failed to get vault address for chain %s: %w", toi.Coin.Asset.GetChain(), err)
	}

	// Create Tx for the swap.
	// Design notes:
	// - ID uses a uniquely generated hash to prevent swap queue collisions with the original transaction.
	// - Chain is set to THORChain because this is an internal protocol-initiated swap,
	//   not an observed transaction from an external chain. The swap handler doesn't
	//   validate or use the Chain field, so this is safe.
	// - FromAddress uses the external chain address (e.g., BTC address) for tracking
	//   purposes. The swap handler only uses FromAddress for validation checks (e.g.,
	//   NoopAddress comparisons) and doesn't enforce chain-address consistency.
	// - Gas is empty because this is an internal operation. The swap handler doesn't
	//   use the Gas field - gas is only relevant for external chain transactions.
	// - Coins contains the external chain asset (e.g., BTC) because the vault holds
	//   these funds in THORChain's accounting, and we're swapping from that asset.
	recoveryTx := common.Tx{
		ID:          recoveryTxID,
		Chain:       common.THORChain,
		FromAddress: vaultAddr,
		ToAddress:   vaultAddr,
		Coins:       common.Coins{toi.Coin},
		Memo:        memo,
		Gas:         common.Gas{},
	}

	// Get Asgard module address for signer
	asgardAddr := mgr.Keeper().GetModuleAccAddress(AsgardName)

	// Determine which swap queue to use and set appropriate version
	enableAdvQueue := mgr.Keeper().GetConfigInt64(ctx, constants.EnableAdvSwapQueue)
	swapVersion := types.SwapVersion_v1
	if enableAdvQueue > 0 {
		swapVersion = types.SwapVersion_v2
	}

	// Create swap message
	swapMsg := NewMsgSwap(
		recoveryTx,
		targetAsset,       // target asset
		destination,       // destination
		cosmos.ZeroUint(), // tradeTarget (market order)
		common.NoAddress,  // no affiliate
		cosmos.ZeroUint(), // no affiliate fee
		"",                // no aggregator
		"",                // no agg target
		nil,               // no agg limit
		MarketSwap,        // swap type
		0,                 // quantity (let handler decide)
		1,                 // interval
		swapVersion,       // v1 for legacy queue, v2 for advanced queue
		asgardAddr,        // Asgard module account as signer
	)

	// Add to swap queue
	if enableAdvQueue > 0 {
		if err := mgr.AdvSwapQueueMgr().AddSwapQueueItem(ctx, mgr, swapMsg); err != nil {
			return fmt.Errorf("failed to add %s recovery swap to adv queue: %w", recoveryType, err)
		}
	} else {
		if err := mgr.Keeper().SetSwapQueueItem(ctx, *swapMsg, 0); err != nil {
			return fmt.Errorf("failed to add %s recovery swap to queue: %w", recoveryType, err)
		}
	}

	ctx.Logger().Info("recovery swap queued",
		"original_hash", txID,
		"recovery_hash", recoveryTxID,
		"recovery_type", recoveryType,
		"from_asset", toi.Coin.Asset,
		"to_asset", targetAsset,
		"destination", destination)

	// Emit failed outbound recovery event
	recoveryEvent := NewEventFailedOutboundRecovery(
		txID,
		toi.Coin,
		recoveryType,
		destination,
		memo,
	)
	if err := mgr.EventMgr().EmitEvent(ctx, recoveryEvent); err != nil {
		ctx.Logger().Error("failed to emit recovery event", "error", err)
	}

	return nil
}

// sendFailedSwapReverseSwap creates a reverse swap to refund the original sender when a swap outbound fails
func (s *SlasherImpl) sendFailedSwapReverseSwap(ctx cosmos.Context, mgr Manager, toi TxOutItem, vault Vault, originalTx ObservedTx, swapMemo SwapMemo, inboundHash common.TxID) error {
	ctx.Logger().Info("attempting reverse swap for failed swap outbound", "hash", toi.InHash, "coin", toi.Coin)

	// Determine destination address: use custom refund address if specified, otherwise original sender
	destination := originalTx.Tx.FromAddress
	if !swapMemo.GetRefundAddress().IsEmpty() {
		// Validate that refund address is on the correct chain
		if swapMemo.GetRefundAddress().IsChain(originalTx.Tx.Coins[0].Asset.GetChain()) {
			destination = swapMemo.GetRefundAddress()
			ctx.Logger().Info("using custom refund address for reverse swap", "address", destination)
		} else {
			ctx.Logger().Info("custom refund address chain mismatch, using original sender",
				"refund_address", swapMemo.GetRefundAddress(),
				"expected_chain", originalTx.Tx.Coins[0].Asset.GetChain())
		}
	}

	// XMR unknown sender guard: if the destination is still an XMR unknown
	// sender marker, skip the reverse swap so the caller falls through to
	// treasury recovery immediately rather than attempting outbounds to an
	// undeliverable address that would fail MaxOutboundAttempts times first.
	destinationInfo := common.ResolveObservedSender(originalTx.Tx.Coins[0].Asset.GetChain(), destination)
	if originalTx.Tx.Coins[0].Asset.GetChain().Equals(common.XMRChain) && destinationInfo.RefundAddressRequired {
		return fmt.Errorf("cannot reverse swap to XMR unknown sender address %s; falling back to treasury", destination)
	}

	// Queue reverse swap to original asset
	targetAsset := originalTx.Tx.Coins[0].Asset
	memo := fmt.Sprintf("=:%s:%s", targetAsset, destination)
	return s.queueRecoverySwap(ctx, mgr, toi, vault, inboundHash, targetAsset, destination, memo, "reverse_swap")
}

// sendFailedOutboundToTreasury creates a swap to convert failed outbound assets to RUNE
// and send them to the treasury address when MaxOutboundAttempts is reached.
// For swap outbounds, attempts to perform a reverse swap instead.
func (s *SlasherImpl) sendFailedOutboundToTreasury(ctx cosmos.Context, mgr Manager, toi TxOutItem, vault Vault, memo mem.Memo) error {
	ctx.Logger().Info("max attempts reached for outbound", "hash", toi.InHash, "coin", toi.Coin)

	// Extract the inbound hash from the memo (format: "OUT:<hash>" or "REFUND:<hash>")
	inboundHash := toi.InHash
	if memo.IsType(TxOutbound) {
		if outboundMemo, ok := memo.(OutboundMemo); ok {
			if !outboundMemo.GetTxID().IsEmpty() {
				inboundHash = outboundMemo.GetTxID()
			}
		}
	} else if memo.IsType(TxRefund) {
		if refundMemo, ok := memo.(RefundMemo); ok {
			if !refundMemo.GetTxID().IsEmpty() {
				inboundHash = refundMemo.GetTxID()
			}
		}
		// As an additional safety measure, explicitly parse REFUND:<hash> format
		// from the memo string in case the typed memo didn't extract it
		if inboundHash.Equals(toi.InHash) && strings.HasPrefix(toi.Memo, "REFUND:") {
			parts := strings.SplitN(toi.Memo, ":", 2)
			if len(parts) == 2 && len(parts[1]) > 0 {
				if txID, err := common.NewTxID(parts[1]); err == nil && !txID.IsEmpty() {
					inboundHash = txID
				}
			}
		}
	}

	// Try to get the original transaction to check if this was a swap
	originalTx, swapMemo, err := s.getOriginalSwapDetails(ctx, mgr, inboundHash)
	if err == nil {
		// This is a failed swap outbound - attempt reverse swap
		ctx.Logger().Info("detected failed swap outbound, attempting reverse swap",
			"hash", inboundHash,
			"original_asset", originalTx.Tx.Coins[0].Asset,
			"failed_asset", toi.Coin.Asset)

		reverseErr := s.sendFailedSwapReverseSwap(ctx, mgr, toi, vault, originalTx, swapMemo, inboundHash)
		if reverseErr == nil {
			// Reverse swap succeeded
			return nil
		}
		// Reverse swap failed, log error and fall back to treasury recovery
		ctx.Logger().Error("reverse swap failed, falling back to treasury recovery",
			"error", reverseErr,
			"hash", inboundHash)
	}

	// Either not a swap, or reverse swap failed - proceed with treasury recovery
	ctx.Logger().Info("sending to treasury recovery", "hash", inboundHash, "coin", toi.Coin)

	// Queue swap to treasury
	return s.queueRecoverySwap(ctx, mgr, toi, vault, inboundHash, common.RuneAsset(), common.TreasuryAddress, fmt.Sprintf("=:RUNE:%s", common.TreasuryAddress), "treasury")
}

// SlashVault thorchain keep monitoring the outbound tx from asgard pool
// usually the txout is triggered by thorchain itself by
// adding an item into the txout array, refer to TxOutItem for the detail, the
// TxOutItem contains a specific coin and amount.  if somehow thorchain
// discover signer send out fund more than the amount specified in TxOutItem,
// it will slash the node account who does that by taking 1.5 * extra fund from
// node account's bond and subsidise the pool that actually lost it.
func (s *SlasherImpl) SlashVault(ctx cosmos.Context, vaultPK common.PubKey, coins common.Coins, mgr Manager) error {
	if coins.IsEmpty() {
		return nil
	}

	vault, err := s.keeper.GetVault(ctx, vaultPK)
	if err != nil {
		return fmt.Errorf("fail to get slash vault (pubkey %s), %w", vaultPK, err)
	}
	membership := vault.GetMembership()

	// sum the total bond of membership of the vault
	totalBond := cosmos.ZeroUint()
	for _, member := range membership {
		na, err := s.keeper.GetNodeAccountByPubKey(ctx, member)
		if err != nil {
			ctx.Logger().Error("fail to get node account bond", "pk", member, "error", err)
			continue
		}
		totalBond = totalBond.Add(na.Bond)
	}

	for _, coin := range coins {
		if coin.IsEmpty() {
			continue
		}
		pool, err := s.keeper.GetPool(ctx, coin.Asset)
		if err != nil {
			ctx.Logger().Error("fail to get pool for slash", "asset", coin.Asset, "error", err)
			continue
		}
		// THORChain doesn't even have a pool for the asset
		if pool.IsEmpty() {
			ctx.Logger().Error("cannot slash for an empty pool", "asset", coin.Asset)
			continue
		}

		stolenAssetValue := coin.Amount
		vaultAmount := vault.GetCoin(coin.Asset).Amount
		if stolenAssetValue.GT(vaultAmount) {
			stolenAssetValue = vaultAmount
		}
		if stolenAssetValue.GT(pool.BalanceAsset) {
			stolenAssetValue = pool.BalanceAsset
		}

		// stolenRuneValue is the value in RUNE of the missing funds
		stolenRuneValue := pool.AssetValueInRune(stolenAssetValue)

		if stolenRuneValue.IsZero() {
			continue
		}

		penaltyPts := mgr.Keeper().GetConfigInt64(ctx, constants.SlashPenalty)
		// total slash amount is penaltyPts the RUNE value of the missing funds
		totalRuneToSlash := common.GetUncappedShare(cosmos.NewUint(uint64(penaltyPts)), cosmos.NewUint(10_000), stolenRuneValue)
		totalRuneSlashed := cosmos.ZeroUint()
		pauseOnSlashThreshold := mgr.Keeper().GetConfigInt64(ctx, constants.PauseOnSlashThreshold)
		if pauseOnSlashThreshold > 0 && totalRuneToSlash.GTE(cosmos.NewUint(uint64(pauseOnSlashThreshold))) {
			// set mimirs to pause signing
			haltsignKey := fmt.Sprintf(constants.MimirTemplateHaltSigning, coin.Asset.Chain)
			s.keeper.SetMimir(ctx, haltsignKey, ctx.BlockHeight())
			mimirEvent1 := NewEventSetMimir(haltsignKey, strconv.FormatInt(ctx.BlockHeight(), 10))
			if err := mgr.EventMgr().EmitEvent(ctx, mimirEvent1); err != nil {
				ctx.Logger().Error("fail to emit set_mimir event", "error", err)
			}
			// set mimirs to pause trading
			halttradeKey := fmt.Sprintf(constants.MimirTemplateHaltTrading, coin.Asset.Chain)
			s.keeper.SetMimir(ctx, halttradeKey, ctx.BlockHeight())
			mimirEvent2 := NewEventSetMimir(halttradeKey, strconv.FormatInt(ctx.BlockHeight(), 10))
			if err := mgr.EventMgr().EmitEvent(ctx, mimirEvent2); err != nil {
				ctx.Logger().Error("fail to emit set_mimir event", "error", err)
			}
		}
		for _, member := range membership {
			na, err := s.keeper.GetNodeAccountByPubKey(ctx, member)
			if err != nil {
				ctx.Logger().Error("fail to get node account for slash", "pk", member, "error", err)
				continue
			}
			if na.Bond.IsZero() {
				ctx.Logger().Info("validator's bond is zero, can't be slashed", "node address", na.NodeAddress.String())
				continue
			}
			runeSlashed := s.slashAndUpdateNodeAccount(ctx, na, coin, vault, totalBond, totalRuneToSlash)
			totalRuneSlashed = totalRuneSlashed.Add(runeSlashed)
		}

		//  2/3 of the total slashed RUNE value to asgard
		//  1/3 of the total slashed RUNE value to reserve
		runeToAsgard := stolenRuneValue

		// stolenRuneValue is the total value in RUNE of stolen coins, but totalRuneSlashed is
		// the total amount able to be slashed from Nodes, credit the pool only totalRuneSlashed
		if totalRuneSlashed.LT(stolenRuneValue) {
			// this should theoretically never happen
			ctx.Logger().Info("total slashed bond amount is less than RUNE value", "slashed_bond", totalRuneSlashed.String(), "rune_value", stolenRuneValue.String())
			runeToAsgard = totalRuneSlashed
		}
		runeToReserve := common.SafeSub(totalRuneSlashed, runeToAsgard)

		if !runeToReserve.IsZero() {
			if err := s.keeper.SendFromModuleToModule(ctx, BondName, ReserveName, common.NewCoins(common.NewCoin(common.RuneAsset(), runeToReserve))); err != nil {
				ctx.Logger().Error("fail to send slash funds to reserve module", "pk", vaultPK, "error", err)
			}
		}
		if !runeToAsgard.IsZero() {
			if err := s.keeper.SendFromModuleToModule(ctx, BondName, AsgardName, common.NewCoins(common.NewCoin(common.RuneAsset(), runeToAsgard))); err != nil {
				ctx.Logger().Error("fail to send slash fund to asgard module", "pk", vaultPK, "error", err)
			}
			s.updatePoolFromSlash(ctx, pool, common.NewCoin(coin.Asset, stolenAssetValue), runeToAsgard, mgr)
		}
	}

	return nil
}

// slashAndUpdateNodeAccount slashes a NodeAccount a portion of the value of coin based on their
// portion of the total bond of the offending Vault's membership. Return the amount of RUNE slashed
func (s *SlasherImpl) slashAndUpdateNodeAccount(ctx cosmos.Context, na types.NodeAccount, coin common.Coin, vault types.Vault, totalBond, totalSlashAmountInRune cosmos.Uint) cosmos.Uint {
	slashAmountRune := common.GetSafeShare(na.Bond, totalBond, totalSlashAmountInRune)
	if slashAmountRune.GT(na.Bond) {
		ctx.Logger().Info("slash amount is larger than bond", "slash amount", slashAmountRune, "bond", na.Bond)
		slashAmountRune = na.Bond
	}

	ctx.Logger().Info("slash node account", "node address", na.NodeAddress.String(), "amount", slashAmountRune.String(), "total slash amount", totalSlashAmountInRune)
	na.Bond = common.SafeSub(na.Bond, slashAmountRune)

	bondEvent := NewEventBond(slashAmountRune, BondCost, common.Tx{}, &na, nil)
	if err := s.eventMgr.EmitEvent(ctx, bondEvent); err != nil {
		ctx.Logger().Error("fail to emit bond event", "error", err)
	}

	metricLabels, _ := ctx.Context().Value(constants.CtxMetricLabels).([]metrics.Label)
	slashAmountRuneFloat, _ := new(big.Float).SetInt(slashAmountRune.BigInt()).Float32()
	telemetry.IncrCounterWithLabels(
		[]string{"thornode", "bond_slash"},
		slashAmountRuneFloat,
		append(
			metricLabels,
			telemetry.NewLabel("address", na.NodeAddress.String()),
			telemetry.NewLabel("coin_symbol", coin.Asset.Symbol.String()),
			telemetry.NewLabel("coin_chain", string(coin.Asset.Chain)),
			telemetry.NewLabel("vault_type", vault.Type.String()),
			telemetry.NewLabel("vault_status", vault.Status.String()),
		),
	)

	if err := s.keeper.SetNodeAccount(ctx, na); err != nil {
		ctx.Logger().Error("fail to save node account for slash", "error", err)
	}

	return slashAmountRune
}

// IncSlashPoints will increase the given account's slash points
func (s *SlasherImpl) IncSlashPoints(ctx cosmos.Context, point int64, addresses ...cosmos.AccAddress) {
	for _, addr := range addresses {
		if err := s.keeper.IncNodeAccountSlashPoints(ctx, addr, point); err != nil {
			ctx.Logger().Error("fail to increase node account slash point", "error", err, "address", addr.String())
		}
	}
}

// DecSlashPoints will decrease the given account's slash points
func (s *SlasherImpl) DecSlashPoints(ctx cosmos.Context, point int64, addresses ...cosmos.AccAddress) {
	for _, addr := range addresses {
		if err := s.keeper.DecNodeAccountSlashPoints(ctx, addr, point); err != nil {
			ctx.Logger().Error("fail to decrease node account slash point", "error", err, "address", addr.String())
		}
	}
}

// updatePoolFromSlash updates a pool's depths and emits appropriate events after a slash
func (s *SlasherImpl) updatePoolFromSlash(ctx cosmos.Context, pool types.Pool, stolenAsset common.Coin, runeCreditAmt cosmos.Uint, mgr Manager) {
	pool.BalanceAsset = common.SafeSub(pool.BalanceAsset, stolenAsset.Amount)
	pool.BalanceRune = pool.BalanceRune.Add(runeCreditAmt)
	if err := s.keeper.SetPool(ctx, pool); err != nil {
		ctx.Logger().Error("fail to save pool for slash", "asset", pool.Asset, "error", err)
	}
	stolenAmtInt64 := int64(math.MaxInt64)
	if stolenAsset.Amount.Uint64() <= uint64(math.MaxInt64) {
		stolenAmtInt64 = int64(stolenAsset.Amount.Uint64())
	}
	creditAmtInt64 := int64(math.MaxInt64)
	if runeCreditAmt.Uint64() <= uint64(math.MaxInt64) {
		creditAmtInt64 = int64(runeCreditAmt.Uint64())
	}
	poolSlashAmt := []PoolAmt{
		{
			Asset:  pool.Asset,
			Amount: 0 - stolenAmtInt64,
		},
		{
			Asset:  common.RuneAsset(),
			Amount: creditAmtInt64,
		},
	}
	eventSlash := NewEventSlash(pool.Asset, poolSlashAmt)
	if err := mgr.EventMgr().EmitEvent(ctx, eventSlash); err != nil {
		ctx.Logger().Error("fail to emit slash event", "error", err)
	}
}

func (s *SlasherImpl) needsNewVault(ctx cosmos.Context, mgr Manager, vault Vault, signingTransPeriod, startHeight int64, toi TxOutItem) bool {
	outhashes := mgr.Keeper().GetObservedLink(ctx, toi.InHash)
	if len(outhashes) == 0 {
		return true
	}

	for _, hash := range outhashes {
		voter, err := mgr.Keeper().GetObservedTxOutVoter(ctx, hash)
		if err != nil {
			ctx.Logger().Error("fail to get txout voter", "hash", hash, "error", err)
			continue
		}
		if voter.FinalisedHeight > 0 {
			// Finalised observed txouts should have nothing to do with unfulfilled TxOutItems.
			// This finalised txout might for instance be from an output
			// that was split into multiple outbounds from initially-different vaults.
			continue
		}
		// in the event there are multiple observed txouts for a given inhash, we
		// focus on the matching pubkey and asset
		signers := make(map[string]bool)
		for _, tx1 := range voter.Txs {
			if !txOutPubKeyMatchesObserved(toi.Chain, tx1.ObservedPubKey, toi) ||
				len(tx1.Tx.Coins) != 1 ||
				!tx1.Tx.Coins[0].Asset.Equals(toi.Coin.Asset) {
				continue
			}

			for _, signer := range tx1.Signers {
				signers[signer] = true
			}
		}
		if len(signers) > 0 {
			var count int // count the number of signers from the assigned vault
			for _, member := range vault.Membership {
				pk, err := common.NewPubKey(member)
				if err != nil {
					continue
				}
				addr, err := pk.GetAddress(common.THORChain)
				if err != nil {
					continue
				}
				if _, ok := signers[addr.String()]; ok {
					count++
				}
			}
			// if a simple majority of vault members have observed the outbound,
			// then we should not reschedule. If a vault says it sent it, it
			// sent it and shouldn't get another vault to send it (potentially
			// a second time)
			if count > 0 && HasSimpleMajority(count, len(vault.Membership)) {
				return false
			}
			maxHeight := startHeight + ((int64(len(signers)) + 1) * signingTransPeriod)
			return maxHeight < ctx.BlockHeight()
		}

	}

	return true
}
