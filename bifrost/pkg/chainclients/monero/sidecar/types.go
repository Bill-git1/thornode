package sidecar

import "encoding/json"

// envelope is the optional wrapper some sidecar endpoints return. Callers try
// envelope-shape first and fall back to direct unmarshal.
type envelope struct {
	Result  json.RawMessage `json:"result"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

// --- FROST DKG ---

type DKGRound1Req struct {
	VaultPubKey      string `json:"vault_pub_key"`
	Threshold        uint32 `json:"threshold"`
	N                uint32 `json:"n"`
	ParticipantIndex uint32 `json:"participant_index"`
	Context          string `json:"context,omitempty"`
	SessionID        string `json:"session_id"`
}

type DKGRound1Resp struct {
	Commitments string `json:"commitments"`
}

type DKGRound2Req struct {
	SessionID      string            `json:"session_id"`
	AllCommitments map[string]string `json:"all_commitments"`
}

type DKGRound2Resp struct {
	EncryptedShares map[string]string `json:"encrypted_shares"`
}

type DKGRound3Req struct {
	SessionID string            `json:"session_id"`
	AllShares map[string]string `json:"all_shares"`
}

type DKGRound3Resp struct {
	Address  string `json:"address"`
	GroupKey string `json:"group_key"`
}

type DKGFinalizeReq struct {
	SessionID string `json:"session_id"`
	GroupKey  string `json:"group_key"`
	Address   string `json:"address"`
}

type DKGFinalizeResp struct {
	Success     bool   `json:"success"`
	KeyBytesHex string `json:"key_bytes_hex"`
}

// --- FROST signing ---

type SignPrepareReq struct {
	MsgID                       string     `json:"msg_id"`
	VaultPubKey                 string     `json:"vault_pub_key"`
	ToAddress                   string     `json:"to_address"`
	AmountAtomic                uint64     `json:"amount_atomic"`
	Memo                        string     `json:"memo,omitempty"`
	FeeRate                     uint64     `json:"fee_rate,omitempty"`
	DecoyContext                string     `json:"decoy_context"`
	RequiredInputs              []SpentRef `json:"required_inputs,omitempty"`
	MaxInputs                   int64      `json:"max_inputs,omitempty"`
	DustThreshold               uint64     `json:"dust_threshold,omitempty"`
	MaxSelectedInputValueAtomic uint64     `json:"max_selected_input_value_atomic,omitempty"`
}

type SignPrepareResp struct {
	SessionID  string `json:"session_id"`
	Preprocess string `json:"preprocess"`
}

type SignPartialReq struct {
	MsgID           string            `json:"msg_id"`
	SessionID       string            `json:"session_id"`
	AllPreprocesses map[string]string `json:"all_preprocesses"`
}

type SignPartialResp struct {
	SignatureShare string `json:"signature_share"`
}

type SignCombineReq struct {
	MsgID     string            `json:"msg_id"`
	SessionID string            `json:"session_id"`
	AllShares map[string]string `json:"all_shares"`
}

type SignCombineResp struct {
	TxHash          string     `json:"tx_hash"`
	TxHex           string     `json:"tx_hex"`
	FeeAtomic       uint64     `json:"fee_atomic"`
	SpentOutputRefs []SpentRef `json:"spent_output_refs,omitempty"`
}

// SpentRef identifies one (input tx, output index) tuple consumed by a sign.
type SpentRef struct {
	TxHash      string `json:"tx_hash"`
	OutputIndex uint32 `json:"output_index"`
	KeyImage    string `json:"key_image,omitempty"`
	SpendTxHash string `json:"spend_tx_hash,omitempty"`
}

type MarkSpentReq struct {
	VaultPubKey      string     `json:"vault_pub_key"`
	TxHash           string     `json:"tx_hash"`
	SpentRefs        []SpentRef `json:"spent_refs"`
	SkipVerification bool       `json:"skip_verification,omitempty"`
	FromAddress      string     `json:"from_address,omitempty"`
	ToAddress        string     `json:"to_address,omitempty"`
	Amount           uint64     `json:"amount,omitempty"`
	Fee              uint64     `json:"fee,omitempty"`
	Memo             string     `json:"memo,omitempty"`
}

type MarkSpentResp struct {
	Marked   int `json:"marked"`
	NotFound int `json:"not_found"`
}

// OutboundMeta carries outbound attribution for MarkSpent calls.
type OutboundMeta struct {
	FromAddress string
	ToAddress   string
	Amount      uint64
	Fee         uint64
	Memo        string
}

// --- Broadcast ---

type BroadcastReq struct {
	TxHex       string `json:"tx_hex"`
	TxHash      string `json:"tx_hash,omitempty"`
	VaultPubKey string `json:"vault_pub_key"`
	MsgID       string `json:"msg_id"`
}

type BroadcastResp struct {
	TxHash string `json:"tx_hash"`
}

// --- Scanner / balance / reads ---

type Balance struct {
	Balance          uint64  `json:"balance"`
	UnlockedBalance  uint64  `json:"unlocked_balance"`
	SpendableBalance *uint64 `json:"spendable_balance,omitempty"`
}

type RegisterVaultReq struct {
	VaultPubKey         string `json:"vault_pub_key"`
	GroupKey            string `json:"group_key"`
	Address             string `json:"address"`
	SpendKeyHex         string `json:"spend_key_hex,omitempty"`
	ViewKeyHex          string `json:"view_key_hex,omitempty"`
	Source              string `json:"source,omitempty"`
	VaultCreationHeight int64  `json:"birthday,omitempty"`
}

type RegisterVaultResp struct {
	Registered bool `json:"registered"`
}

type Transfer struct {
	TxID          string `json:"txid"`
	Address       string `json:"address"`
	FromAddress   string `json:"from_address,omitempty"`
	VaultPubKey   string `json:"vault_pub_key,omitempty"`
	Amount        uint64 `json:"amount"`
	Fee           uint64 `json:"fee"`
	Height        uint64 `json:"height"`
	Confirmations uint64 `json:"confirmations"`
	Note          string `json:"note"`
	Locked        bool   `json:"locked"`
}

type Transfers struct {
	Incoming []Transfer `json:"incoming"`
	Outgoing []Transfer `json:"outgoing"`
}

type SyncReq struct {
	Height      int64  `json:"height"`
	TipHeight   int64  `json:"tip_height"`
	BlockHash   string `json:"block_hash"`
	ParentHash  string `json:"parent_hash"`
	ReorgWindow int64  `json:"reorg_window"`
}

type Addresses struct {
	Addresses []string `json:"addresses"`
}

type Output struct {
	TxHash          string `json:"tx_hash"`
	OutputIndex     uint32 `json:"output_index"`
	Amount          uint64 `json:"amount"`
	Height          uint64 `json:"height"`
	KeyImage        string `json:"key_image,omitempty"`
	Spent           bool   `json:"spent"`
	State           string `json:"state,omitempty"`
	SubaddressMajor uint32 `json:"subaddress_major"`
	SubaddressMinor uint32 `json:"subaddress_minor"`
}

type Outputs struct {
	Outputs []Output `json:"outputs"`
}

type FeeRate struct {
	FeeRatePerWeight uint64 `json:"fee_rate_per_weight"`
}

type ChainHeightResp struct {
	Height int64 `json:"height"`
}

type ChainBlockResp struct {
	Height       int64    `json:"height"`
	Reward       uint64   `json:"reward"`
	Hash         string   `json:"hash"`
	PreviousHash string   `json:"previous_hash"`
	TxHashes     []string `json:"tx_hashes"`
}

type TxExistsResp struct {
	Exists bool `json:"exists"`
}

// ChainBlock is the transport-independent block descriptor (used across both
// the sidecar and monerod-fallback paths).
type ChainBlock struct {
	Height       int64
	Reward       uint64
	Hash         string
	PreviousHash string
	TxHashes     []string
}

// VaultKey describes the sidecar's knowledge of a single vault's threshold
// share.
type VaultKey struct {
	VaultPubKey  string `json:"vault_pub_key"`
	HasFrostKeys bool   `json:"has_frost_keys"`
}
