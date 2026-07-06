// Package sidecar is the HTTP boundary between Bifrost and the
// `xmr-frost-signer` Rust process. All DKG/sign/broadcast calls plus the
// wallet-scan and chain-query reads flow through the exported Client here.
package sidecar

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

// Client is the HTTP client for the xmr-frost-signer sidecar.
type Client struct {
	endpoint string

	daemonEndpoint string

	dkgRound1Path     string
	dkgRound2Path     string
	dkgRound3Path     string
	dkgFinalizePath   string
	signPreparePath   string
	signPartialPath   string
	signCombinePath   string
	clearSessionsPath string
	broadcastPath     string
	balancePath       string
	transfersPath     string
	scannerSyncPath   string
	addressesPath     string
	outputsPath       string
	registerPath      string
	feeRatePath       string
	chainHeightPath   string
	chainBlockPath    string
	chainTxPath       string

	client       *http.Client
	daemonClient *http.Client
	signClient   *http.Client
	username     string
	password     string
	bearer       string
}

// NewClient wires a sidecar Client from a Bifrost chain configuration.
func NewClient(cfg config.BifrostChainConfiguration) (*Client, error) {
	endpoint := strings.TrimSpace(cfg.SignerEndpoint)
	return NewClientWithEndpoint(cfg, endpoint)
}

// NewClientWithEndpoint overrides the endpoint read from cfg.
func NewClientWithEndpoint(cfg config.BifrostChainConfiguration, endpoint string) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("sidecar endpoint is empty")
	}

	parsedEndpoint, err := parseSidecarEndpointURL(endpoint)
	if err != nil {
		return nil, err
	}
	if parsedEndpoint.Scheme == "http" &&
		!IsLoopbackHost(parsedEndpoint.Hostname()) &&
		!AllowInsecureHTTP(cfg) {
		return nil, fmt.Errorf("sidecar endpoint %q must use https when not loopback", endpoint)
	}

	username := strings.TrimSpace(cfg.UserName)
	password := strings.TrimSpace(cfg.Password)
	bearer := strings.TrimSpace(cfg.AuthorizationBearer)
	if (username == "") != (password == "") {
		return nil, fmt.Errorf("sidecar requires both username and password when basic auth is configured")
	}
	if !IsLoopbackHost(parsedEndpoint.Hostname()) && bearer == "" && username == "" {
		return nil, fmt.Errorf("sidecar endpoint %q requires authorization_bearer or username/password", endpoint)
	}

	endpoint = strings.TrimSuffix(parsedEndpoint.String(), "/")

	daemonEndpoint := ""
	if strings.TrimSpace(cfg.RPCHost) != "" {
		parsedDaemon, err := parseDaemonEndpointURL(cfg.RPCHost)
		if err != nil {
			return nil, err
		}
		daemonEndpoint = normalizeDaemonEndpoint(parsedDaemon)
	}

	timeout := cfg.BlockScanner.HTTPRequestTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	signTimeout := timeout
	if signTimeout < 120*time.Second {
		signTimeout = 120 * time.Second
	}

	return &Client{
		endpoint:       endpoint,
		daemonEndpoint: daemonEndpoint,

		dkgRound1Path:     "/v1/monero/dkg/round1",
		dkgRound2Path:     "/v1/monero/dkg/round2",
		dkgRound3Path:     "/v1/monero/dkg/round3",
		dkgFinalizePath:   "/v1/monero/dkg/finalize",
		signPreparePath:   "/v1/monero/sign/prepare",
		signPartialPath:   "/v1/monero/sign/partial",
		signCombinePath:   "/v1/monero/sign/combine",
		clearSessionsPath: "/v1/monero/sessions/clear",
		broadcastPath:     "/v1/monero/broadcast",
		balancePath:       "/v1/monero/balance",
		transfersPath:     "/v1/monero/transfers",
		scannerSyncPath:   "/v1/monero/scanner/sync",
		addressesPath:     "/v1/monero/addresses",
		outputsPath:       "/v1/monero/outputs",
		registerPath:      "/v1/monero/vaults/register",
		feeRatePath:       "/v1/monero/fee-rate",
		chainHeightPath:   "/v1/monero/chain/height",
		chainBlockPath:    "/v1/monero/chain/block",
		chainTxPath:       "/v1/monero/chain/tx",

		client:       &http.Client{Timeout: timeout},
		daemonClient: &http.Client{Timeout: timeout},
		signClient:   &http.Client{Timeout: signTimeout},
		username:     username,
		password:     password,
		bearer:       bearer,
	}, nil
}

func parseSidecarEndpointURL(endpoint string) (*url.URL, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("sidecar endpoint is empty")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid sidecar endpoint %q: %w", endpoint, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid sidecar endpoint scheme %q", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return nil, fmt.Errorf("invalid sidecar endpoint host")
	}
	return parsed, nil
}

// IsLoopbackHost reports whether host resolves to a loopback address or the
// "localhost" alias. Exported for reuse by the server-side component.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AllowInsecureHTTP decides whether http:// endpoints are accepted. Allowed on
// the non-public networks (mocknet, chainnet, stagenet), where the sidecar and
// monerod are reached over internal cluster traffic; mainnet requires TLS.
func AllowInsecureHTTP(cfg config.BifrostChainConfiguration) bool {
	chainNetwork := strings.TrimSpace(cfg.ChainNetwork)
	if strings.EqualFold(chainNetwork, "mocknet") {
		return true
	}
	net := common.CurrentChainNetwork
	return net == common.MockNet || net == common.ChainNet || net == common.StageNet
}

// --- FROST DKG ---

func (c *Client) DKGRound1(req DKGRound1Req) (DKGRound1Resp, error) {
	var out DKGRound1Resp
	if err := c.call(http.MethodPost, c.dkgRound1Path, req, &out); err != nil {
		return DKGRound1Resp{}, fmt.Errorf("fail to call dkg round1 endpoint: %w", err)
	}
	if strings.TrimSpace(out.Commitments) == "" {
		return DKGRound1Resp{}, fmt.Errorf("dkg round1 endpoint returned empty commitments")
	}
	return out, nil
}

func (c *Client) DKGRound2(req DKGRound2Req) (DKGRound2Resp, error) {
	var out DKGRound2Resp
	if err := c.call(http.MethodPost, c.dkgRound2Path, req, &out); err != nil {
		return DKGRound2Resp{}, fmt.Errorf("fail to call dkg round2 endpoint: %w", err)
	}
	// A single-participant FROST DKG has nobody else to encrypt shares for, so
	// the sidecar legitimately returns an empty map here.
	if len(out.EncryptedShares) == 0 && len(req.AllCommitments) > 1 {
		return DKGRound2Resp{}, fmt.Errorf("dkg round2 endpoint returned empty encrypted_shares")
	}
	return out, nil
}

func (c *Client) DKGRound3(req DKGRound3Req) (DKGRound3Resp, error) {
	var out DKGRound3Resp
	if err := c.call(http.MethodPost, c.dkgRound3Path, req, &out); err != nil {
		return DKGRound3Resp{}, fmt.Errorf("fail to call dkg round3 endpoint: %w", err)
	}
	if strings.TrimSpace(out.Address) == "" {
		return DKGRound3Resp{}, fmt.Errorf("dkg round3 endpoint returned empty address")
	}
	return out, nil
}

func (c *Client) DKGFinalize(req DKGFinalizeReq) (DKGFinalizeResp, error) {
	var out DKGFinalizeResp
	if err := c.call(http.MethodPost, c.dkgFinalizePath, req, &out); err != nil {
		return DKGFinalizeResp{}, fmt.Errorf("fail to call dkg finalize endpoint: %w", err)
	}
	if !out.Success {
		return DKGFinalizeResp{}, fmt.Errorf("dkg finalize endpoint returned unsuccessful response")
	}
	return out, nil
}

// --- FROST signing ---

func (c *Client) SignPrepare(req SignPrepareReq) (SignPrepareResp, error) {
	var out SignPrepareResp
	if err := c.call(http.MethodPost, c.signPreparePath, req, &out); err != nil {
		return SignPrepareResp{}, fmt.Errorf("fail to call sign prepare endpoint: %w", err)
	}
	if strings.TrimSpace(out.SessionID) == "" {
		return SignPrepareResp{}, fmt.Errorf("sign prepare endpoint returned empty session_id")
	}
	if strings.TrimSpace(out.Preprocess) == "" {
		return SignPrepareResp{}, fmt.Errorf("sign prepare endpoint returned empty preprocess")
	}
	return out, nil
}

func (c *Client) SignPartial(req SignPartialReq) (SignPartialResp, error) {
	var out SignPartialResp
	if err := c.call(http.MethodPost, c.signPartialPath, req, &out); err != nil {
		return SignPartialResp{}, fmt.Errorf("fail to call sign partial endpoint: %w", err)
	}
	if strings.TrimSpace(out.SignatureShare) == "" {
		return SignPartialResp{}, fmt.Errorf("sign partial endpoint returned empty signature_share")
	}
	return out, nil
}

func (c *Client) SignCombine(req SignCombineReq) (SignCombineResp, error) {
	var out SignCombineResp
	if err := c.call(http.MethodPost, c.signCombinePath, req, &out); err != nil {
		return SignCombineResp{}, fmt.Errorf("fail to call sign combine endpoint: %w", err)
	}
	if strings.TrimSpace(out.TxHash) == "" {
		return SignCombineResp{}, fmt.Errorf("sign combine endpoint returned empty tx_hash")
	}
	return out, nil
}

// ClearSessions drops any cached per-session state on the sidecar. Returns the
// count of sessions cleared for logging.
func (c *Client) ClearSessions() (int, error) {
	if c == nil || strings.TrimSpace(c.clearSessionsPath) == "" {
		return 0, nil
	}
	var out struct {
		ClearedSessions int    `json:"cleared_sessions"`
		Status          string `json:"status"`
	}
	if err := c.call(http.MethodPost, c.clearSessionsPath, struct{}{}, &out); err != nil {
		return 0, fmt.Errorf("fail to call sessions clear endpoint: %w", err)
	}
	return out.ClearedSessions, nil
}

func (c *Client) Broadcast(req BroadcastReq) (BroadcastResp, error) {
	var out BroadcastResp
	if err := c.call(http.MethodPost, c.broadcastPath, req, &out); err != nil {
		return BroadcastResp{}, fmt.Errorf("fail to call broadcast endpoint: %w", err)
	}
	return out, nil
}

// --- Reads ---

func (c *Client) Balance(vaultPubKey string) (Balance, error) {
	var out Balance
	path := c.balancePath + "/" + url.PathEscape(vaultPubKey)
	if err := c.callGet(path, &out); err != nil {
		return Balance{}, fmt.Errorf("fail to call balance endpoint: %w", err)
	}
	return out, nil
}

func (c *Client) RegisterVault(req RegisterVaultReq) (RegisterVaultResp, error) {
	var out RegisterVaultResp
	if err := c.call(http.MethodPost, c.registerPath, req, &out); err != nil {
		return RegisterVaultResp{}, fmt.Errorf("fail to call register vault endpoint: %w", err)
	}
	return out, nil
}

// TransfersAt returns the sidecar's diagnostic view of transfers at a height
// (read-only; scanner sync is the canonical drive).
func (c *Client) TransfersAt(height int64) (Transfers, error) {
	var out Transfers
	path := fmt.Sprintf("%s?height=%d", c.transfersPath, height)
	if err := c.callGet(path, &out); err != nil {
		return Transfers{}, fmt.Errorf("fail to call transfers endpoint: %w", err)
	}
	return out, nil
}

// SyncAt is the canonical Bifrost-driven block scan call.
func (c *Client) SyncAt(height, tipHeight int64, blockHash, parentHash string, reorgWindow int64) (Transfers, error) {
	if height < 0 {
		return Transfers{}, fmt.Errorf("height must be >= 0")
	}
	if tipHeight < 0 {
		tipHeight = 0
	}
	if reorgWindow < 0 {
		reorgWindow = 0
	}

	var out Transfers
	req := SyncReq{
		Height:      height,
		TipHeight:   tipHeight,
		BlockHash:   strings.TrimSpace(blockHash),
		ParentHash:  strings.TrimSpace(parentHash),
		ReorgWindow: reorgWindow,
	}
	if err := c.call(http.MethodPost, c.scannerSyncPath, req, &out); err != nil {
		return Transfers{}, fmt.Errorf("fail to call scanner sync endpoint: %w", err)
	}
	return out, nil
}

func (c *Client) Mempool() (Transfers, error) {
	var out Transfers
	path := fmt.Sprintf("%s?mempool=true", c.transfersPath)
	if err := c.callGet(path, &out); err != nil {
		return Transfers{}, fmt.Errorf("fail to call mempool transfers endpoint: %w", err)
	}
	return out, nil
}

func (c *Client) Addresses(vaultPubKey string) ([]string, error) {
	var out Addresses
	path := c.addressesPath + "/" + url.PathEscape(vaultPubKey)
	if err := c.callGet(path, &out); err != nil {
		return nil, fmt.Errorf("fail to call addresses endpoint: %w", err)
	}
	return out.Addresses, nil
}

func (c *Client) Outputs(vaultPubKey string) ([]Output, error) {
	var out Outputs
	path := c.outputsPath + "/" + url.PathEscape(vaultPubKey)
	if err := c.callGet(path, &out); err != nil {
		return nil, fmt.Errorf("fail to call outputs endpoint: %w", err)
	}
	return out.Outputs, nil
}

func (c *Client) FeeRate() (uint64, error) {
	var out FeeRate
	if err := c.callGet(c.feeRatePath, &out); err != nil {
		return 0, fmt.Errorf("fail to call fee-rate endpoint: %w", err)
	}
	return out.FeeRatePerWeight, nil
}

func (c *Client) ResetScanner(vaultPubKey string, birthdayHeight int64) error {
	req := struct {
		VaultPubKey    string `json:"vault_pub_key"`
		BirthdayHeight int64  `json:"birthday_height"`
	}{vaultPubKey, birthdayHeight}
	return c.call(http.MethodPost, "/v1/monero/scanner/reset", req, nil)
}

func (c *Client) ChainHeight() (int64, error) {
	var daemonErr error
	if c.hasDaemonRPC() {
		height, err := c.daemonHeight()
		if err == nil {
			return height, nil
		}
		daemonErr = fmt.Errorf("fail to call monero daemon chain-height rpc: %w", err)
	}

	var out ChainHeightResp
	if err := c.callGet(c.chainHeightPath, &out); err != nil {
		if daemonErr != nil {
			return 0, fmt.Errorf("%v; fail to call sidecar chain-height endpoint: %w", daemonErr, err)
		}
		return 0, fmt.Errorf("fail to call sidecar chain-height endpoint: %w", err)
	}
	return out.Height, nil
}

func (c *Client) ChainBlock(height int64) (ChainBlock, error) {
	if height < 0 {
		return ChainBlock{}, fmt.Errorf("height must be >= 0")
	}

	var daemonErr error
	if c.hasDaemonRPC() {
		block, err := c.daemonBlock(height)
		if err == nil {
			return block, nil
		}
		if isUnavailableBlock(err) {
			return ChainBlock{}, err
		}
		daemonErr = fmt.Errorf("fail to call monero daemon chain-block rpc: %w", err)
	}

	var out ChainBlockResp
	path := fmt.Sprintf("%s/%d", c.chainBlockPath, height)
	if err := c.callGet(path, &out); err != nil {
		if IsHTTPStatus(err, http.StatusConflict) {
			return ChainBlock{}, fmt.Errorf("%w: %d", ErrUnavailableBlock, height)
		}
		if daemonErr != nil {
			return ChainBlock{}, fmt.Errorf("%v; fail to call sidecar chain-block endpoint: %w", daemonErr, err)
		}
		return ChainBlock{}, fmt.Errorf("fail to call sidecar chain-block endpoint: %w", err)
	}
	return ChainBlock(out), nil
}

func (c *Client) TxExists(txHash string) (bool, error) {
	normalized := strings.TrimSpace(txHash)
	if normalized == "" {
		return false, fmt.Errorf("tx hash is empty")
	}

	var out TxExistsResp
	path := fmt.Sprintf("%s/%s/exists", c.chainTxPath, url.PathEscape(normalized))
	if err := c.callGet(path, &out); err != nil {
		return false, fmt.Errorf("fail to call chain tx-exists endpoint: %w", err)
	}
	return out.Exists, nil
}

// MarkSpent records that outputs were consumed by a particular spend tx.
func (c *Client) MarkSpent(vaultPubKey, txHash string, refs []SpentRef, skipVerification bool, outbound *OutboundMeta) (MarkSpentResp, error) {
	var out MarkSpentResp
	req := MarkSpentReq{
		VaultPubKey:      vaultPubKey,
		TxHash:           txHash,
		SpentRefs:        refs,
		SkipVerification: skipVerification,
	}
	if outbound != nil {
		req.FromAddress = outbound.FromAddress
		req.ToAddress = outbound.ToAddress
		req.Amount = outbound.Amount
		req.Fee = outbound.Fee
		req.Memo = outbound.Memo
	}
	if err := c.call(http.MethodPost, "/v1/monero/outputs/mark-spent", req, &out); err != nil {
		return out, fmt.Errorf("fail to call mark-spent endpoint: %w", err)
	}
	return out, nil
}

// Vaults lists every vault the sidecar knows about and whether it holds a
// threshold share for it.
func (c *Client) Vaults() ([]VaultKey, error) {
	var out struct {
		Vaults []VaultKey `json:"vaults"`
	}
	if err := c.callGet("/v1/monero/keys", &out); err != nil {
		return nil, fmt.Errorf("fail to list vault keys: %w", err)
	}
	return out.Vaults, nil
}

// ExportKeys returns a recovery blob for the given vault's threshold share.
func (c *Client) ExportKeys(vaultPubKey string) (string, error) {
	var out struct {
		KeyBytesHex string `json:"key_bytes_hex"`
	}
	path := fmt.Sprintf("/v1/monero/keys/%s/export", url.PathEscape(vaultPubKey))
	if err := c.callGet(path, &out); err != nil {
		return "", fmt.Errorf("fail to export keys: %w", err)
	}
	return out.KeyBytesHex, nil
}

// RestoreKeys imports a recovery blob back into the sidecar. The blob is the
// raw on-disk recovery file, which may be at-rest encrypted (binary). It is
// base64-encoded for transport because raw bytes are not valid UTF-8 and would
// be mangled by JSON string encoding.
func (c *Client) RestoreKeys(vaultPubKey string, keyBlob []byte) (bool, error) {
	var out struct {
		Restored bool `json:"restored"`
	}
	req := struct {
		VaultPubKey string `json:"vault_pub_key"`
		KeyBlobB64  string `json:"key_blob_b64"`
	}{
		VaultPubKey: vaultPubKey,
		KeyBlobB64:  base64.StdEncoding.EncodeToString(keyBlob),
	}
	if err := c.call(http.MethodPost, "/v1/monero/keys/restore", req, &out); err != nil {
		return false, fmt.Errorf("fail to restore keys: %w", err)
	}
	return out.Restored, nil
}

// --- HTTP helpers ---

func (c *Client) callGet(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return fmt.Errorf("fail to create sidecar request: %w", err)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.bearer))
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("fail to call sidecar endpoint %s: %w", path, err)
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	respBody, err := readBoundedResponseBody(resp.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("fail to read sidecar response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return &HTTPError{
			Path:   path,
			Status: resp.StatusCode,
			Body:   strings.TrimSpace(string(respBody)),
		}
	}
	if out == nil {
		return nil
	}
	if len(respBody) == 0 {
		return fmt.Errorf("sidecar endpoint %s returned empty response body", path)
	}

	var env envelope
	if err = json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("fail to unmarshal sidecar response: %w", err)
	}
	if env.Error != "" || env.Message != "" {
		msg := strings.TrimSpace(env.Error)
		if msg == "" {
			msg = strings.TrimSpace(env.Message)
		}
		return fmt.Errorf("sidecar endpoint %s error: %s", path, msg)
	}
	if len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return json.Unmarshal(respBody, out)
}

func (c *Client) call(method, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("fail to marshal sidecar request payload: %w", err)
	}

	req, err := http.NewRequest(method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fail to create sidecar request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.bearer != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.bearer))
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	httpClient := c.client
	if path == c.signPreparePath && c.signClient != nil {
		httpClient = c.signClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fail to call sidecar endpoint %s: %w", path, err)
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	respBody, err := readBoundedResponseBody(resp.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("fail to read sidecar response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return &HTTPError{
			Path:   path,
			Status: resp.StatusCode,
			Body:   strings.TrimSpace(string(respBody)),
		}
	}
	if out == nil {
		return nil
	}
	if len(respBody) == 0 {
		return fmt.Errorf("sidecar endpoint %s returned empty response body", path)
	}

	// Try envelope first: json.Unmarshal succeeds on any valid JSON regardless
	// of field matches, so we cannot rely on direct-unmarshal failure to detect
	// envelope-style responses.
	var env envelope
	if err = json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("fail to unmarshal sidecar response: %w", err)
	}
	if env.Error != "" || env.Message != "" {
		msg := strings.TrimSpace(env.Error)
		if msg == "" {
			msg = strings.TrimSpace(env.Message)
		}
		return fmt.Errorf("sidecar endpoint %s error: %s", path, msg)
	}
	if len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return json.Unmarshal(respBody, out)
}
