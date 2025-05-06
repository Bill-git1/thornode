package ebifrost

import (
	"context"
	"errors"
	fmt "fmt"
	"net"
	"sync"
	"time"

	"cosmossdk.io/log"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	cmttypes "github.com/cometbft/cometbft/types"
	common "gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
	"google.golang.org/grpc"
)

const (
	// This is a dummy address used for injected transactions.
	// It is not used otherwise, as quorum msg attestations are verified within the handlers.
	ebifrostSigner        = "thor1zxhfu0qmmq6gmgq4sgz0xgq69h0nhqx5yrseu5"
	cachedBlocks          = 10
	EventQuorumTxCommited = "quorum_tx_committed"
)

var ErrAlreadyStarted = errors.New("ebifrost already started")

var notifyEvents = []string{EventQuorumTxCommited}

var _, ebifrostSignerAcc, _ = bech32.DecodeAndConvert(ebifrostSigner)

// EnshrinedBifrost is a thornode service that is used to communicate with bifrost
// for the purpose of observing transactions and processing quorum attestations.
type EnshrinedBifrost struct {
	cdc    codec.Codec
	s      *grpc.Server
	logger log.Logger

	cfg EBifrostConfig

	quorumTxCache   *InjectCache[*common.QuorumTx]
	networkFeeCache *InjectCache[*common.QuorumNetworkFee]
	solvencyCache   *InjectCache[*common.QuorumSolvency]
	errataCache     *InjectCache[*common.QuorumErrataTx]

	subscribers   map[string][]chan *EventNotification
	subscribersMu sync.Mutex

	started bool
	mu      sync.Mutex
}

func NewEnshrinedBifrost(cdc codec.Codec, logger log.Logger, config EBifrostConfig) *EnshrinedBifrost {
	if !config.Enable {
		return nil
	}

	s := grpc.NewServer()
	ebs := &EnshrinedBifrost{
		cdc:             cdc,
		logger:          logger.With("module", "ebifrost"),
		s:               s,
		quorumTxCache:   NewInjectCache[*common.QuorumTx](),
		networkFeeCache: NewInjectCache[*common.QuorumNetworkFee](),
		solvencyCache:   NewInjectCache[*common.QuorumSolvency](),
		errataCache:     NewInjectCache[*common.QuorumErrataTx](),
		subscribers:     make(map[string][]chan *EventNotification),
		cfg:             config,
	}

	RegisterLocalhostBifrostServer(s, ebs)
	return ebs
}

func (b *EnshrinedBifrost) Start() error {
	if b == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return ErrAlreadyStarted
	}
	b.started = true

	lis, err := net.Listen("tcp", b.cfg.Address)
	if err != nil {
		return err
	}
	go func() {
		if err := b.s.Serve(lis); err != nil {
			panic(fmt.Errorf("failed to start enshrined bifrost grpc server: %w", err))
		}
	}()

	return nil
}

func (b *EnshrinedBifrost) Stop() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started {
		return
	}
	b.started = false

	b.s.Stop()
}

func (b *EnshrinedBifrost) SendQuorumTx(ctx context.Context, tx *common.QuorumTx) (*SendQuorumTxResult, error) {
	if err := b.quorumTxCache.AddItem(
		tx,
		(*common.QuorumTx).GetAttestations,
		(*common.QuorumTx).SetAttestations,
		(*common.QuorumTx).Equals,
	); err != nil {
		return nil, err
	}

	return &SendQuorumTxResult{}, nil
}

func (b *EnshrinedBifrost) SendQuorumNetworkFee(ctx context.Context, nf *common.QuorumNetworkFee) (*SendQuorumNetworkFeeResult, error) {
	if err := b.networkFeeCache.AddItem(
		nf,
		(*common.QuorumNetworkFee).GetAttestations,
		(*common.QuorumNetworkFee).SetAttestations,
		(*common.QuorumNetworkFee).Equals,
	); err != nil {
		return nil, err
	}

	return &SendQuorumNetworkFeeResult{}, nil
}

func (b *EnshrinedBifrost) SendQuorumSolvency(ctx context.Context, s *common.QuorumSolvency) (*SendQuorumSolvencyResult, error) {
	if err := b.solvencyCache.AddItem(
		s,
		(*common.QuorumSolvency).GetAttestations,
		(*common.QuorumSolvency).SetAttestations,
		(*common.QuorumSolvency).Equals,
	); err != nil {
		return nil, err
	}

	return &SendQuorumSolvencyResult{}, nil
}

func (b *EnshrinedBifrost) SendQuorumErrataTx(ctx context.Context, e *common.QuorumErrataTx) (*SendQuorumErrataTxResult, error) {
	b.quorumTxCache.Lock()
	for i, tx := range b.quorumTxCache.items {
		if tx.ObsTx.Tx.Chain == e.ErrataTx.Chain && tx.ObsTx.Tx.ID == e.ErrataTx.Id {
			// remove the tx from the cache because we observed an error for it
			b.quorumTxCache.items = append(b.quorumTxCache.items[:i], b.quorumTxCache.items[i+1:]...)
			break
		}
	}
	b.quorumTxCache.Unlock()

	if err := b.errataCache.AddItem(
		e,
		(*common.QuorumErrataTx).GetAttestations,
		(*common.QuorumErrataTx).SetAttestations,
		(*common.QuorumErrataTx).Equals,
	); err != nil {
		return nil, err
	}

	return &SendQuorumErrataTxResult{}, nil
}

func (b *EnshrinedBifrost) SubscribeToEvents(req *SubscribeRequest, stream LocalhostBifrost_SubscribeToEventsServer) error {
	for _, eventType := range req.EventTypes {
		found := false
		for _, notifyEvent := range notifyEvents {
			if eventType == notifyEvent {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown event type: %s", eventType)
		}
	}

	eventCh := make(chan *EventNotification)

	// Register this client
	b.subscribersMu.Lock()
	for _, eventType := range req.EventTypes {
		b.subscribers[eventType] = append(b.subscribers[eventType], eventCh)
	}
	b.subscribersMu.Unlock()

	b.logger.Info("Client subscribed to events", "event_types", req.EventTypes)

	// Keep the connection open and forward events to client
	for {
		select {
		case event := <-eventCh:
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-stream.Context().Done():
			// Client disconnected, clean up
			b.removeSubscriber(eventCh)
			return nil
		}
	}
}

// removeSubscriber removes the given channel from all event type subscriptions
func (b *EnshrinedBifrost) removeSubscriber(ch chan *EventNotification) {
	b.subscribersMu.Lock()
	defer b.subscribersMu.Unlock()

	// Iterate through all event types
	// analyze-ignore(map-iteration)
	for eventType, subscribers := range b.subscribers {
		// Create a new slice without the channel we're removing
		newSubscribers := make([]chan *EventNotification, 0, len(subscribers))

		for _, subCh := range subscribers {
			if subCh != ch {
				newSubscribers = append(newSubscribers, subCh)
			}
		}

		// Update the subscribers list for this event type
		if len(newSubscribers) == 0 {
			// No subscribers left for this event type, remove the key
			delete(b.subscribers, eventType)
		} else {
			b.subscribers[eventType] = newSubscribers
		}
	}

	// Close the channel to signal subscribers they won't receive more events
	close(ch)
}

func (b *EnshrinedBifrost) broadcastEvent(eventType string, payload []byte) {
	event := &EventNotification{
		EventType: eventType,
		Payload:   payload,
		Timestamp: time.Now().Unix(),
	}

	b.subscribersMu.Lock()
	subscribers := b.subscribers[eventType]
	b.subscribersMu.Unlock()

	for _, ch := range subscribers {
		select {
		case ch <- event:
			b.logger.Debug("Event sent to subscriber", "event", eventType)
			// Event sent successfully
		default:
			b.logger.Error("Failed to send event to subscriber", "event", eventType)
			// Channel is full or closed, could implement cleanup here
		}
	}
}

func (b *EnshrinedBifrost) broadcastQuorumTxEvent(tx *common.QuorumTx) {
	b.quorumTxCache.BroadcastEvent(
		tx,
		func(item *common.QuorumTx) ([]byte, error) {
			return item.Marshal()
		},
		b.broadcastEvent,
		EventQuorumTxCommited,
		b.logger,
	)
}

// MarkQuorumTxAttestationsConfirmed is intended to be called by the bifrost post handler after a tx has been processed.
// It will look for any matching quorum txs, remove the confirmed attestations from them, and remove the quorum txs if all attestations have been confirmed.
func (b *EnshrinedBifrost) MarkQuorumTxAttestationsConfirmed(ctx context.Context, qtx *common.QuorumTx) {
	if b == nil {
		return
	}

	go b.broadcastQuorumTxEvent(qtx)

	found := b.quorumTxCache.MarkAttestationsConfirmed(
		qtx,
		b.logger,
		(*common.QuorumTx).Equals,
		(*common.QuorumTx).GetAttestations,
		(*common.QuorumTx).RemoveAttestations,
		func(qtx *common.QuorumTx, logger log.Logger) {
			obsTx := qtx.ObsTx
			logger.Debug("Marking quorum tx attestations confirmed",
				"chain", obsTx.Tx.Chain,
				"hash", obsTx.Tx.ID,
				"attestations", len(qtx.Attestations))
		},
	)

	if !found {
		cmpObsTx := qtx.ObsTx
		b.logger.Debug("Failed to find quorum tx to mark attestations confirmed",
			"chain", cmpObsTx.Tx.Chain,
			"hash", cmpObsTx.Tx.ID,
		)
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()

	b.quorumTxCache.AddToBlock(height, qtx)
	b.quorumTxCache.CleanOldBlocks(height, cachedBlocks)
}

// MarkQuorumNetworkFeeAttestationsConfirmed is intended to be called by the bifrost post handler after a tx has been processed.
// It will look for any matching quorum txs, remove the confirmed attestations from them, and remove the quorum txs if all attestations have been confirmed.
func (b *EnshrinedBifrost) MarkQuorumNetworkFeeAttestationsConfirmed(ctx context.Context, qnf *common.QuorumNetworkFee) {
	if b == nil {
		return
	}

	found := b.networkFeeCache.MarkAttestationsConfirmed(
		qnf,
		b.logger,
		(*common.QuorumNetworkFee).Equals,
		(*common.QuorumNetworkFee).GetAttestations,
		(*common.QuorumNetworkFee).RemoveAttestations,
		func(qnf *common.QuorumNetworkFee, logger log.Logger) {
			nf := qnf.NetworkFee
			logger.Debug("Marking quorum network fee attestations confirmed",
				"chain", nf.Chain,
				"height", nf.Height,
				"tx_size", nf.TransactionSize,
				"tx_rate", nf.TransactionRate,
				"attestations", len(qnf.Attestations))
		},
	)

	if !found {
		cmpNf := qnf.NetworkFee
		b.logger.Debug("Failed to find quorum network fee to mark attestations confirmed",
			"chain", cmpNf.Chain,
			"height", cmpNf.Height,
			"tx_size", cmpNf.TransactionSize,
			"tx_rate", cmpNf.TransactionRate,
		)
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()

	b.networkFeeCache.AddToBlock(height, qnf)
	b.networkFeeCache.CleanOldBlocks(height, cachedBlocks)
}

// MarkQuorumSolvencyAttestationsConfirmed is intended to be called by the bifrost post handler after a tx has been processed.
// It will look for any matching quorum txs, remove the confirmed attestations from them, and remove the quorum txs if all attestations have been confirmed.
func (b *EnshrinedBifrost) MarkQuorumSolvencyAttestationsConfirmed(ctx context.Context, qs *common.QuorumSolvency) {
	if b == nil {
		return
	}

	found := b.solvencyCache.MarkAttestationsConfirmed(
		qs,
		b.logger,
		(*common.QuorumSolvency).Equals,
		(*common.QuorumSolvency).GetAttestations,
		(*common.QuorumSolvency).RemoveAttestations,
		func(qs *common.QuorumSolvency, logger log.Logger) {
			s := qs.Solvency
			logger.Debug("Marking quorum solvency attestations confirmed",
				"chain", s.Chain,
				"height", s.Height,
				"coins", s.Coins,
				"pub_key", s.PubKey,
				"attestations", len(qs.Attestations))
		},
	)

	if !found {
		cmpS := qs.Solvency
		b.logger.Debug("Failed to find quorum solvency to mark attestations confirmed",
			"chain", cmpS.Chain,
			"height", cmpS.Height,
			"coins", cmpS.Coins,
			"pub_key", cmpS.PubKey,
		)
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()

	b.solvencyCache.AddToBlock(height, qs)
	b.solvencyCache.CleanOldBlocks(height, cachedBlocks)
}

// MarkQuorumErrataTxAttestationsConfirmed is intended to be called by the bifrost post handler after a tx has been processed.
// It will look for any matching quorum txs, remove the confirmed attestations from them, and remove the quorum txs if all attestations have been confirmed.
func (b *EnshrinedBifrost) MarkQuorumErrataTxAttestationsConfirmed(ctx context.Context, qe *common.QuorumErrataTx) {
	if b == nil {
		return
	}

	found := b.errataCache.MarkAttestationsConfirmed(
		qe,
		b.logger,
		(*common.QuorumErrataTx).Equals,
		(*common.QuorumErrataTx).GetAttestations,
		(*common.QuorumErrataTx).RemoveAttestations,
		func(qe *common.QuorumErrataTx, logger log.Logger) {
			er := qe.ErrataTx
			logger.Debug("Marking quorum errata attestations confirmed",
				"chain", er.Chain,
				"id", er.Id,
				"attestations", len(qe.Attestations))
		},
	)

	if !found {
		cmpEr := qe.ErrataTx
		b.logger.Debug("Failed to find quorum errata to mark attestations confirmed",
			"chain", cmpEr.Chain,
			"id", cmpEr.Id,
		)
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()

	b.errataCache.AddToBlock(height, qe)
	b.errataCache.CleanOldBlocks(height, cachedBlocks)
}

func (b *EnshrinedBifrost) MarshalTx(msg sdk.Msg) ([]byte, error) {
	itx := NewInjectTx(b.cdc, []sdk.Msg{msg})
	return itx.Tx.Marshal()
}

// ProposalInjectTxs is intended to be called by the current proposing validator during PrepareProposal
// and will return a list of in-quorum transactions to be included in the next block along with the total byte length of the transactions.
func (b *EnshrinedBifrost) ProposalInjectTxs(ctx sdk.Context, maxTxBytes int64) ([][]byte, int64) {
	if b == nil {
		return nil, 0
	}

	var injectTxs [][]byte
	var txBzLen int64

	// Process observed txs
	txBzs := b.quorumTxCache.ProcessForProposal(
		func(tx *common.QuorumTx) (sdk.Msg, error) {
			return types.NewMsgObservedTxQuorum(tx, ebifrostSignerAcc), nil
		},
		b.MarshalTx,
		func(tx *common.QuorumTx, logger log.Logger) {
			obsTx := tx.ObsTx
			logger.Info("Injecting quorum tx",
				"chain", obsTx.Tx.Chain,
				"hash", obsTx.Tx.ID,
				"finalized", obsTx.IsFinal(),
				"inbound", tx.Inbound,
				"attestations", len(tx.Attestations))
		},
		b.logger,
	)
	for _, bz := range txBzs {
		addLen := cmttypes.ComputeProtoSizeForTxs([]cmttypes.Tx{bz})
		if txBzLen+addLen > maxTxBytes {
			continue
		}
		txBzLen += addLen
		injectTxs = append(injectTxs, bz)
	}

	// Process network fees
	nfBzs := b.networkFeeCache.ProcessForProposal(
		func(qnf *common.QuorumNetworkFee) (sdk.Msg, error) {
			return types.NewMsgNetworkFeeQuorum(qnf, ebifrostSignerAcc), nil
		},
		b.MarshalTx,
		func(qnf *common.QuorumNetworkFee, logger log.Logger) {
			nf := qnf.NetworkFee
			logger.Info("Injecting quorum network fee",
				"chain", nf.Chain,
				"height", nf.Height,
				"attestations", len(qnf.Attestations))
		},
		b.logger,
	)
	for _, bz := range nfBzs {
		addLen := cmttypes.ComputeProtoSizeForTxs([]cmttypes.Tx{bz})
		if txBzLen+addLen > maxTxBytes {
			continue
		}
		txBzLen += addLen
		injectTxs = append(injectTxs, bz)
	}

	// Process solvency
	sBzs := b.solvencyCache.ProcessForProposal(
		func(qs *common.QuorumSolvency) (sdk.Msg, error) {
			return types.NewMsgSolvencyQuorum(qs, ebifrostSignerAcc)
		},
		b.MarshalTx,
		func(qs *common.QuorumSolvency, logger log.Logger) {
			s := qs.Solvency
			logger.Info("Injecting quorum solvency",
				"chain", s.Chain,
				"pubkey", s.PubKey,
				"height", s.Height,
				"coins", s.Coins,
				"attestations", len(qs.Attestations))
		},
		b.logger,
	)
	for _, bz := range sBzs {
		addLen := cmttypes.ComputeProtoSizeForTxs([]cmttypes.Tx{bz})
		if txBzLen+addLen > maxTxBytes {
			continue
		}
		txBzLen += addLen
		injectTxs = append(injectTxs, bz)
	}

	// Process errata
	eBzs := b.errataCache.ProcessForProposal(
		func(qe *common.QuorumErrataTx) (sdk.Msg, error) {
			return types.NewMsgErrataTxQuorum(qe, ebifrostSignerAcc), nil
		},
		b.MarshalTx,
		func(qe *common.QuorumErrataTx, logger log.Logger) {
			e := qe.ErrataTx
			logger.Info("Injecting quorum errata",
				"chain", e.Chain,
				"id", e.Id,
				"attestations", len(qe.Attestations))
		},
		b.logger,
	)
	for _, bz := range eBzs {
		addLen := cmttypes.ComputeProtoSizeForTxs([]cmttypes.Tx{bz})
		if txBzLen+addLen > maxTxBytes {
			continue
		}
		txBzLen += addLen
		injectTxs = append(injectTxs, bz)
	}

	return injectTxs, txBzLen
}

// Test use only
func (b *EnshrinedBifrost) GetInjectedMsgs(ctx sdk.Context, txs [][]byte) ([]sdk.Msg, error) {
	var msgs []sdk.Msg

	for _, txbz := range txs {
		tx, err := TxDecoder(b.cdc, nil)(txbz)
		if err != nil {
			return nil, err
		}

		msgs = append(msgs, tx.GetMsgs()...)
	}

	return msgs, nil
}
