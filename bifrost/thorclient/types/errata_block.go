package types

import "gitlab.com/thorchain/thornode/v3/common"

type ErrataBlock struct {
	Height     int64
	Txs        []ErrataTx
	DeliveryID string
	// Persisted is an optional in-process durability acknowledgment. The XMR
	// reorg journal waits until ObserverStorage has persisted both the errata
	// outbox record and its local on-deck cleanup, and the local errata
	// attestation has been accepted, before advancing delivery.
	Persisted chan error
}

type ErrataTx struct {
	TxID  common.TxID
	Chain common.Chain
}
