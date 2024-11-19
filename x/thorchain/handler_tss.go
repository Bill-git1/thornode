package thorchain

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/blang/semver"
	"github.com/btcsuite/btcd/btcec"
	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/hashicorp/go-metrics"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

type TssHandler = BaseHandler[*MsgTssPool]

// NewTssHandler create a new handler to process MsgTssPool
func NewTssHandler(mgr Manager) BaseHandler[*MsgTssPool] {
	return BaseHandler[*MsgTssPool]{
		mgr:    mgr,
		logger: MsgTssPoolLogger,
		validators: NewValidators[*MsgTssPool]().
			Register("2.137.0", MsgTssPoolValidateV137).
			Register("1.124.0", MsgTssPoolValidateV124),
		handlers: NewHandlers[*MsgTssPool]().
			Register("2.137.0", MsgTssPoolHandleV137).
			Register("1.134.0", MsgTssPoolHandleV134),
	}
}

func MsgTssPoolLogger(ctx cosmos.Context, msg *MsgTssPool) {
	ctx.Logger().Info(
		"handleMsgTssPool request",
		"ID", msg.ID,
		"signer", msg.Signer.String(),
		"pubkey", msg.PoolPubKey,
		"secp256k1_signature", base64.StdEncoding.EncodeToString(msg.Secp256K1Signature),
		"keyshares_backup", len(msg.KeysharesBackup) > 0,
	)
}

// verifySecp256K1Signature verifies the provided signature of the public key. This is
// set as a variable so tests can override verification when using random public keys.
var verifySecp256K1Signature = func(pk common.PubKey, sig []byte) error {
	// verify signature length
	if len(sig) != 64 {
		return fmt.Errorf("invalid secp256k1 signature length")
	}

	// build the signature
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	signature := &btcec.Signature{R: r, S: s}

	// verify the signature
	spk, err := pk.Secp256K1()
	if err != nil {
		return fmt.Errorf("fail to get secp256k1 pubkey: %w", err)
	}
	if !signature.Verify([]byte(pk.String()), spk) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

func MsgTssPoolValidateV137(ctx cosmos.Context, mgr Manager, msg *MsgTssPool) error {
	// ValidateBasic is also executed in message service router's handler and isn't versioned there
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	if msg.KeygenType != AsgardKeygen {
		return fmt.Errorf("only asgard vaults allowed for tss")
	}

	newMsg, err := NewMsgTssPool(msg.PubKeys, msg.PoolPubKey, nil, nil, msg.KeygenType, msg.Height, msg.Blame, msg.Chains, msg.Signer, msg.KeygenTime)
	if err != nil {
		return fmt.Errorf("fail to recreate MsgTssPool,err: %w", err)
	}
	if msg.ID != newMsg.ID {
		return cosmos.ErrUnknownRequest("invalid tss message")
	}

	churnRetryBlocks := mgr.Keeper().GetConfigInt64(ctx, constants.ChurnRetryInterval)
	if msg.Height <= ctx.BlockHeight()-churnRetryBlocks {
		return cosmos.ErrUnknownRequest("invalid keygen block")
	}

	// verify the check signatures if provided (only a subset of members in signing party)
	if len(msg.Secp256K1Signature) > 0 {
		err = verifySecp256K1Signature(msg.PoolPubKey, msg.Secp256K1Signature)
		if err != nil {
			ctx.Logger().Error(
				"invalid secp256k1 check signature",
				"err", err,
				"ID", msg.ID,
				"signer", msg.Signer.String(),
				"pubkey", msg.PoolPubKey,
				"signature", base64.StdEncoding.EncodeToString(msg.Secp256K1Signature),
			)
			return cosmos.ErrUnknownRequest("invalid secp256k1 check signature")
		}
	}

	keygenBlock, err := mgr.Keeper().GetKeygenBlock(ctx, msg.Height)
	if err != nil {
		return fmt.Errorf("fail to get keygen block from data store: %w", err)
	}

	for _, keygen := range keygenBlock.Keygens {
		keyGenMembers := keygen.GetMembers()
		if !msg.GetPubKeys().Equals(keyGenMembers) {
			continue
		}
		// Make sure the keygen type are consistent
		if msg.KeygenType != keygen.Type {
			continue
		}
		for _, member := range keygen.GetMembers() {
			addr, err := member.GetThorAddress()
			if err == nil && addr.Equals(msg.Signer) {
				return validateTssAuth(ctx, mgr.Keeper(), msg.Signer)
			}
		}
	}

	return cosmos.ErrUnauthorized("not authorized")
}

func validateTssAuth(ctx cosmos.Context, k keeper.Keeper, signer cosmos.AccAddress) error {
	nodeSigner, err := k.GetNodeAccount(ctx, signer)
	if err != nil {
		return fmt.Errorf("invalid signer")
	}
	if nodeSigner.IsEmpty() {
		return fmt.Errorf("invalid signer")
	}
	if nodeSigner.Status != NodeActive && nodeSigner.Status != NodeReady {
		return fmt.Errorf("invalid signer status(%s)", nodeSigner.Status)
	}
	// ensure we have enough rune
	minBond := k.GetConfigInt64(ctx, constants.MinimumBondInRune)
	if nodeSigner.Bond.LT(cosmos.NewUint(uint64(minBond))) {
		return fmt.Errorf("signer doesn't have enough rune")
	}
	return nil
}

func MsgTssPoolHandleV137(ctx cosmos.Context, mgr Manager, msg *MsgTssPool) (*cosmos.Result, error) {
	ctx.Logger().Info("handler tss", "current version", mgr.GetVersion())
	blames := make([]string, 0)
	if !msg.Blame.IsEmpty() {
		for i := range msg.Blame.BlameNodes {
			pk, err := common.NewPubKey(msg.Blame.BlameNodes[i].Pubkey)
			if err != nil {
				ctx.Logger().Error("fail to get tss keygen pubkey", "pubkey", msg.Blame.BlameNodes[i].Pubkey, "error", err)
				continue
			}
			acc, err := pk.GetThorAddress()
			if err != nil {
				ctx.Logger().Error("fail to get tss keygen thor address", "pubkey", msg.Blame.BlameNodes[i].Pubkey, "error", err)
				continue
			}
			blames = append(blames, acc.String())
		}
		sort.Strings(blames)
		ctx.Logger().Info(
			"tss keygen results blame",
			"height", msg.Height,
			"id", msg.ID,
			"pubkey", msg.PoolPubKey,
			"round", msg.Blame.Round,
			"blames", strings.Join(blames, ", "),
			"reason", msg.Blame.FailReason,
			"blamer", msg.Signer,
		)
	}
	// only record TSS metric when keygen is success
	if msg.IsSuccess() && !msg.PoolPubKey.IsEmpty() {
		metric, err := mgr.Keeper().GetTssKeygenMetric(ctx, msg.PoolPubKey)
		if err != nil {
			ctx.Logger().Error("fail to get keygen metric", "error", err)
		} else {
			ctx.Logger().Info("save keygen metric to db")
			metric.AddNodeTssTime(msg.Signer, msg.KeygenTime)
			mgr.Keeper().SetTssKeygenMetric(ctx, metric)
		}
	}
	voter, err := mgr.Keeper().GetTssVoter(ctx, msg.ID)
	if err != nil {
		return nil, fmt.Errorf("fail to get tss voter: %w", err)
	}

	// when PoolPubKey is empty , which means TssVoter with id(msg.ID) doesn't
	// exist before, this is the first time to create it
	// set the PoolPubKey to the one in msg, there is no reason voter.PubKeys
	// have anything in it either, thus override it with msg.PubKeys as well
	if voter.PoolPubKey.IsEmpty() {
		voter.PoolPubKey = msg.PoolPubKey
		voter.PubKeys = msg.PubKeys
	}
	// voter's pool pubkey is the same as the one in message
	if !voter.PoolPubKey.Equals(msg.PoolPubKey) {
		return nil, fmt.Errorf("invalid pool pubkey")
	}
	observeSlashPoints := mgr.GetConstants().GetInt64Value(constants.ObserveSlashPoints)
	lackOfObservationPenalty := mgr.GetConstants().GetInt64Value(constants.LackOfObservationPenalty)
	observeFlex := mgr.Keeper().GetConfigInt64(ctx, constants.ObservationDelayFlexibility)

	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
		telemetry.NewLabel("reason", "failed_observe_tss_pool"),
	}))

	if !voter.Sign(msg.Signer, msg.Chains, string(msg.Secp256K1Signature)) {
		// Slash for the network having to handle the extra message/s.
		mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
		ctx.Logger().Info("signer already signed MsgTssPool", "signer", msg.Signer.String(), "txid", msg.ID)
		return &cosmos.Result{}, nil

	}
	mgr.Keeper().SetTssVoter(ctx, voter)

	if !voter.HasConsensus() {
		// Slash until 2/3rds consensus.
		mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
		return &cosmos.Result{}, nil
	}

	if voter.BlockHeight > 0 && (voter.BlockHeight+observeFlex) >= ctx.BlockHeight() {
		// After 2/3rds consensus, only decrement slash points if within the ObservationDelayFlexibility period.
		// (This is expected to only apply for a failed keygen.)
		mgr.Slasher().DecSlashPoints(slashCtx, lackOfObservationPenalty, msg.Signer)
	}

	if voter.BlockHeight == 0 {
		// This message brings the voter to 2/3rds consensus.
		// For an IsSuccess() message, BlockHeight and MajorityConsensusBlockHeight will initially be the same.
		voter.BlockHeight = ctx.BlockHeight()
		mgr.Keeper().SetTssVoter(ctx, voter)

		// A list of keygen node accounts isn't readily available,
		// so (rather than do a KVStore-check GetNodeAccount)
		// prepare the non-signer AccAddresses manually.
		signers := voter.GetSigners()
		var keygenNodeAccAddresses []cosmos.AccAddress
		for _, member := range msg.PubKeys {
			pkey, err := common.NewPubKey(member)
			if err != nil {
				ctx.Logger().Error("fail to get pub key", "error", err)
				continue
			}
			thorAddr, err := pkey.GetThorAddress()
			if err != nil {
				ctx.Logger().Error("fail to get thor address", "error", err)
				continue
			}
			keygenNodeAccAddresses = append(keygenNodeAccAddresses, thorAddr)
		}
		var nonSigners []cosmos.AccAddress
		var signed bool
		for _, keygenNodeAccAddress := range keygenNodeAccAddresses {
			signed = false
			for _, signer := range signers {
				if keygenNodeAccAddress.Equals(signer) {
					signed = true
					break
				}
			}

			if !signed {
				nonSigners = append(nonSigners, keygenNodeAccAddress)
			}
		}

		// As this signer brings the voter to 2/3rds consensus,
		// increment the signer's slash points like the before-consensus signers,
		// then decrement all the signers' slash points and increment the non-signers' slash points.
		mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
		mgr.Slasher().DecSlashPoints(slashCtx, observeSlashPoints, signers...)
		mgr.Slasher().IncSlashPoints(slashCtx, lackOfObservationPenalty, nonSigners...)

		// Do the below only for a non-success message upon 2/3rds consensus.
		if !msg.IsSuccess() {
			// since the keygen failed, it's now safe to reset all nodes in
			// ready status back to standby status
			ready, err := mgr.Keeper().ListValidatorsByStatus(ctx, NodeReady)
			if err != nil {
				ctx.Logger().Error("fail to get list of ready node accounts", "error", err)
			}
			for _, na := range ready {
				na.UpdateStatus(NodeStandby, ctx.BlockHeight())
				if err := mgr.Keeper().SetNodeAccount(ctx, na); err != nil {
					ctx.Logger().Error("fail to set node account", "error", err)
				}
			}

			// if a node fail to join the keygen, thus hold off the network
			// from churning then it will be slashed accordingly
			slashPoints := mgr.GetConstants().GetInt64Value(constants.FailKeygenSlashPoints)
			for _, node := range msg.Blame.BlameNodes {
				nodePubKey, err := common.NewPubKey(node.Pubkey)
				if err != nil {
					return nil, ErrInternal(err, fmt.Sprintf("fail to parse pubkey(%s)", node.Pubkey))
				}

				na, err := mgr.Keeper().GetNodeAccountByPubKey(ctx, nodePubKey)
				if err != nil {
					return nil, fmt.Errorf("fail to get node from it's pub key: %w", err)
				}
				if na.Status == NodeActive {
					failedKeygenSlashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
						telemetry.NewLabel("reason", "failed_keygen"),
					}))
					if err := mgr.Keeper().IncNodeAccountSlashPoints(failedKeygenSlashCtx, na.NodeAddress, slashPoints); err != nil {
						ctx.Logger().Error("fail to inc slash points", "error", err)
					}

					if err := mgr.EventMgr().EmitEvent(ctx, NewEventSlashPoint(na.NodeAddress, slashPoints, "fail keygen")); err != nil {
						ctx.Logger().Error("fail to emit slash point event")
					}
				} else {
					// go to jail
					jailTime := mgr.GetConstants().GetInt64Value(constants.JailTimeKeygen)
					releaseHeight := ctx.BlockHeight() + jailTime
					reason := "failed to perform keygen"
					if err := mgr.Keeper().SetNodeAccountJail(ctx, na.NodeAddress, releaseHeight, reason); err != nil {
						ctx.Logger().Error("fail to set node account jail", "node address", na.NodeAddress, "reason", reason, "error", err)
					}

					network, err := mgr.Keeper().GetNetwork(ctx)
					if err != nil {
						return nil, fmt.Errorf("fail to get network: %w", err)
					}

					slashBond := network.CalcNodeRewards(cosmos.NewUint(uint64(slashPoints)))
					if slashBond.GT(na.Bond) {
						slashBond = na.Bond
					}
					ctx.Logger().Info("fail keygen , slash bond", "address", na.NodeAddress, "amount", slashBond.String())
					// take out bond from the node account and add it to the Reserve
					// thus good behaviour nodes and liquidity providers will get reward
					na.Bond = common.SafeSub(na.Bond, slashBond)
					coin := common.NewCoin(common.RuneNative, slashBond)
					if !coin.Amount.IsZero() {
						if err := mgr.Keeper().SendFromModuleToModule(ctx, BondName, ReserveName, common.NewCoins(coin)); err != nil {
							return nil, fmt.Errorf("fail to transfer funds from bond to reserve: %w", err)
						}
						slashFloat, _ := new(big.Float).SetInt(slashBond.BigInt()).Float32()
						telemetry.IncrCounterWithLabels(
							[]string{"thornode", "bond_slash"},
							slashFloat,
							[]metrics.Label{
								telemetry.NewLabel("address", na.NodeAddress.String()),
								telemetry.NewLabel("reason", "failed_keygen"),
							},
						)
					}

					tx := common.Tx{}
					tx.ID = common.BlankTxID
					tx.FromAddress = na.BondAddress
					bondEvent := NewEventBond(slashBond, BondCost, tx, &na, nil)
					if err := mgr.EventMgr().EmitEvent(ctx, bondEvent); err != nil {
						return nil, fmt.Errorf("fail to emit bond event: %w", err)
					}
				}
				if err := mgr.Keeper().SetNodeAccount(ctx, na); err != nil {
					return nil, fmt.Errorf("fail to save node account: %w", err)
				}
			}

			if err := mgr.EventMgr().EmitEvent(ctx, NewEventTssKeygenFailure(msg.Blame.FailReason, msg.Blame.Round, msg.Blame.IsUnicast, msg.Height, blames)); err != nil {
				ctx.Logger().Error("fail to emit keygen failure event")
			}
		}
	}

	// when keygen success
	if msg.IsSuccess() {
		// Separately from the usual consensus-agreement slash points,
		// those who haven't agreed with a consensus success message incur FailKeygenSlashPoints until agreement.
		judgeLateSigner(ctx, mgr, msg, voter)

		// Do the below only for a success message upon complete consensus.
		if voter.HasCompleteConsensus() {
			ctx.Logger().Info(
				"tss keygen results success",
				"height", msg.Height,
				"id", msg.ID,
				"pubkey", msg.PoolPubKey,
			)

			// we must also have quorum on the check signature
			consensusSig, ok := voter.ConsensusCheckSignature()
			if !ok {
				ctx.Logger().Error("keygen rejected due to lacking check signature quorum")
				return &cosmos.Result{}, nil
			}

			// log an error if any bad nodes submitted a mismatched check signature
			for _, sig := range voter.Secp256K1Signatures {
				if sig != consensusSig {
					ctx.Logger().Error(
						"mismatched check signature detected",
						"expected", base64.StdEncoding.EncodeToString([]byte(consensusSig)),
						"found", base64.StdEncoding.EncodeToString([]byte(sig)),
					)
				}
			}

			// Update the BlockHeight to reflect the newly reached state.
			voter.BlockHeight = ctx.BlockHeight()
			mgr.Keeper().SetTssVoter(ctx, voter)

			vaultType := AsgardVault
			chains := voter.ConsensusChains()
			vault := NewVault(ctx.BlockHeight(), InitVault, vaultType, voter.PoolPubKey, chains.Strings(), mgr.Keeper().GetChainContracts(ctx, chains))
			vault.Membership = voter.PubKeys

			if err := mgr.Keeper().SetVault(ctx, vault); err != nil {
				return nil, fmt.Errorf("fail to save vault: %w", err)
			}
			keygenBlock, err := mgr.Keeper().GetKeygenBlock(ctx, msg.Height)
			if err != nil {
				return nil, fmt.Errorf("fail to get keygen block, err: %w, height: %d", err, msg.Height)
			}
			initVaults, err := mgr.Keeper().GetAsgardVaultsByStatus(ctx, InitVault)
			if err != nil {
				return nil, fmt.Errorf("fail to get init vaults: %w", err)
			}

			metric, err := mgr.Keeper().GetTssKeygenMetric(ctx, msg.PoolPubKey)
			if err != nil {
				ctx.Logger().Error("fail to get keygen metric", "error", err)
			} else {
				var total int64
				for _, item := range metric.NodeTssTimes {
					total += item.TssTime
				}
				evt := NewEventTssKeygenMetric(metric.PubKey, metric.GetMedianTime())
				if err := mgr.EventMgr().EmitEvent(ctx, evt); err != nil {
					ctx.Logger().Error("fail to emit tss metric event", "error", err)
				}
			}

			if len(initVaults) == len(keygenBlock.Keygens) {
				ctx.Logger().Info("tss keygen results churn", "asgards", len(initVaults))
				for _, v := range initVaults {
					if err := mgr.NetworkMgr().RotateVault(ctx, v); err != nil {
						return nil, fmt.Errorf("fail to rotate vault: %w", err)
					}
				}
			} else {
				ctx.Logger().Info("not enough keygen yet", "expecting", len(keygenBlock.Keygens), "current", len(initVaults))
			}

			addrs, err := vault.GetMembership().Addresses()
			members := make([]string, len(addrs))
			if err != nil {
				ctx.Logger().Error("fail to get member addresses", "error", err)
			} else {
				for i, addr := range addrs {
					members[i] = addr.String()
				}
				if err := mgr.EventMgr().EmitEvent(ctx, NewEventTssKeygenSuccess(msg.PoolPubKey, msg.Height, members)); err != nil {
					ctx.Logger().Error("fail to emit keygen success event")
				}
			}
		}
	}

	return &cosmos.Result{}, nil
}

func judgeLateSigner(ctx cosmos.Context, mgr Manager, msg *MsgTssPool, voter TssVoter) {
	// if the voter doesn't reach 2/3 majority consensus , this method should not take any actions
	if !voter.HasConsensus() || !msg.IsSuccess() {
		return
	}
	slashPoints := mgr.GetConstants().GetInt64Value(constants.FailKeygenSlashPoints)
	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
		telemetry.NewLabel("reason", "failed_observe_tss_pool"),
	}))

	// when voter already has 2/3 majority signers , restore current message signer's slash points
	if voter.MajorityConsensusBlockHeight > 0 {
		mgr.Slasher().DecSlashPoints(slashCtx, slashPoints, msg.Signer)
		if err := mgr.Keeper().ReleaseNodeAccountFromJail(ctx, msg.Signer); err != nil {
			ctx.Logger().Error("fail to release node account from jail", "node address", msg.Signer, "error", err)
		}
		return
	}

	voter.MajorityConsensusBlockHeight = ctx.BlockHeight()
	mgr.Keeper().SetTssVoter(ctx, voter)
	for _, member := range msg.PubKeys {
		pkey, err := common.NewPubKey(member)
		if err != nil {
			ctx.Logger().Error("fail to get pub key", "error", err)
			continue
		}
		thorAddr, err := pkey.GetThorAddress()
		if err != nil {
			ctx.Logger().Error("fail to get thor address", "error", err)
			continue
		}
		// whoever is in the keygen list , but didn't broadcast MsgTssPool
		if !voter.HasSigned(thorAddr) {
			mgr.Slasher().IncSlashPoints(slashCtx, slashPoints, thorAddr)
			// go to jail
			jailTime := mgr.GetConstants().GetInt64Value(constants.JailTimeKeygen)
			releaseHeight := ctx.BlockHeight() + jailTime
			reason := "failed to vote keygen in time"
			if err := mgr.Keeper().SetNodeAccountJail(ctx, thorAddr, releaseHeight, reason); err != nil {
				ctx.Logger().Error("fail to set node account jail", "node address", thorAddr, "reason", reason, "error", err)
			}
		}
	}
}

// TssAnteHandler called by the ante handler to gate mempool entry
// and also during deliver. Store changes will persist if this function
// succeeds, regardless of the success of the transaction.
func TssAnteHandler(ctx cosmos.Context, v semver.Version, k keeper.Keeper, msg MsgTssPool) error {
	err := validateTssAuth(ctx, k, msg.Signer)
	if err != nil {
		return err
	}

	// reject messages with a check signature before network version 137
	if v.LT(semver.MustParse("2.137.0")) && len(msg.Secp256K1Signature) > 0 {
		return fmt.Errorf("check signatures not supported before version 137")
	}

	return nil
}
