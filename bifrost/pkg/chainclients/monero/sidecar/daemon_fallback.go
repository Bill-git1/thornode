package sidecar

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// daemon RPC types — used only by the optional fallback path. They are
// intentionally kept unexported; outside callers should use Client.ChainHeight
// / Client.ChainBlock which hide the transport choice.

type daemonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type daemonRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *daemonRPCError `json:"error"`
}

type daemonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *daemonRPCError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("daemon rpc error %d", e.Code)
	}
	return fmt.Sprintf("daemon rpc error %d: %s", e.Code, strings.TrimSpace(e.Message))
}

type daemonGetInfoResp struct {
	Height int64 `json:"height"`
}

type daemonGetBlockParams struct {
	Height int64 `json:"height"`
}

type daemonBlockHeader struct {
	Height   int64  `json:"height"`
	Reward   uint64 `json:"reward"`
	Hash     string `json:"hash"`
	PrevHash string `json:"prev_hash"`
}

type daemonGetBlockResp struct {
	BlockHeader daemonBlockHeader `json:"block_header"`
	TxHashes    []string          `json:"tx_hashes"`
	JSON        string            `json:"json"`
}

type daemonBlockJSON struct {
	TxHashes []string `json:"tx_hashes"`
}

func parseDaemonEndpointURL(endpoint string) (*url.URL, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("monero rpc host is empty")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid monero rpc host %q: %w", endpoint, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid monero rpc host scheme %q", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return nil, fmt.Errorf("invalid monero rpc host")
	}
	return parsed, nil
}

func normalizeDaemonEndpoint(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	copied := *parsed
	copied.Path = strings.TrimSuffix(strings.TrimSuffix(copied.Path, "/"), "/json_rpc")
	return strings.TrimSuffix(copied.String(), "/")
}

func (c *Client) hasDaemonRPC() bool {
	return c != nil && c.daemonClient != nil && strings.TrimSpace(c.daemonEndpoint) != ""
}

func (c *Client) daemonHeight() (int64, error) {
	var out daemonGetInfoResp
	if err := c.daemonRPC("get_info", struct{}{}, &out); err != nil {
		return 0, err
	}
	if out.Height <= 0 {
		return 0, fmt.Errorf("daemon returned invalid height %d", out.Height)
	}
	return out.Height - 1, nil
}

func (c *Client) daemonBlock(height int64) (ChainBlock, error) {
	tipHeight, err := c.daemonHeight()
	if err != nil {
		return ChainBlock{}, err
	}
	if height > tipHeight {
		return ChainBlock{}, fmt.Errorf("%w: %d", ErrUnavailableBlock, height)
	}

	var out daemonGetBlockResp
	if err := c.daemonRPC("get_block", daemonGetBlockParams{Height: height}, &out); err != nil {
		return ChainBlock{}, err
	}

	txHashes := out.TxHashes
	if len(txHashes) == 0 && strings.TrimSpace(out.JSON) != "" {
		var parsed daemonBlockJSON
		if err := json.Unmarshal([]byte(out.JSON), &parsed); err == nil && len(parsed.TxHashes) > 0 {
			txHashes = parsed.TxHashes
		}
	}

	return ChainBlock{
		Height:       out.BlockHeader.Height,
		Reward:       out.BlockHeader.Reward,
		Hash:         out.BlockHeader.Hash,
		PreviousHash: out.BlockHeader.PrevHash,
		TxHashes:     txHashes,
	}, nil
}

func (c *Client) daemonRPC(method string, params any, out any) error {
	if !c.hasDaemonRPC() {
		return fmt.Errorf("monero daemon rpc is not configured")
	}

	body, err := json.Marshal(daemonRPCRequest{
		JSONRPC: "2.0",
		ID:      "0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("fail to marshal monero daemon request payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.daemonEndpoint+"/json_rpc", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fail to create monero daemon request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.daemonClient.Do(req)
	if err != nil {
		return fmt.Errorf("fail to call monero daemon rpc %s: %w", method, err)
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	respBody, err := readBoundedResponseBody(resp.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("fail to read monero daemon response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("monero daemon rpc %s returned status %d: %s", method, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out == nil {
		return nil
	}
	if len(respBody) == 0 {
		return fmt.Errorf("monero daemon rpc %s returned empty response body", method)
	}

	var env daemonRPCResponse
	if err = json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("fail to unmarshal monero daemon rpc response: %w", err)
	}
	if env.Error != nil {
		return env.Error
	}
	if len(env.Result) == 0 {
		return fmt.Errorf("monero daemon rpc %s returned empty result", method)
	}
	if err = json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("fail to unmarshal monero daemon rpc result: %w", err)
	}
	return nil
}

func isUnavailableBlock(err error) bool {
	return errors.Is(err, ErrUnavailableBlock)
}
