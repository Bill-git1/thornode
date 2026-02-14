package zec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Client struct {
	logger  zerolog.Logger
	http    *http.Client
	url     string
	timeout time.Duration
	header  http.Header
	regexp  *regexp.Regexp
}

func NewClient(url string, timeout time.Duration, header http.Header) *Client {
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")

	return &Client{
		logger:  log.Logger.With().Str("module", "zec_rpc").Logger(),
		url:     url,
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
		header:  header,
		regexp:  regexp.MustCompile(`"nonce":"[0-9a-fA-F]{64}"`),
	}
}

func (c *Client) Call(result interface{}, method string, args ...interface{}) error {
	data, err := c.post(method, args...)
	if err != nil {
		return err
	}

	// Replace nonce on getblock response to avoid parsing error
	// Zcash returns a hex string, due to its different mining algorithm
	// Thornode doesn't use the nonce information at all

	if method == "getblock" {
		data = []byte(c.regexp.ReplaceAllString(string(data), `"nonce":0`))
	}

	var resp Response

	err = json.Unmarshal(data, &resp)
	if err != nil {
		return err
	}

	if result != nil && reflect.TypeOf(result).Kind() != reflect.Ptr {
		return fmt.Errorf("call result parameter must be pointer or nil interface: %v", result)
	}

	switch {
	case resp.Error != nil:
		return resp.Error
	case len(resp.Result) == 0:
		return fmt.Errorf("JSON-RPC response has no result")
	default:
		if result == nil {
			return nil
		}
		return json.Unmarshal(resp.Result, result)
	}
}

func (c *Client) BatchCall(batch []rpc.BatchElem) error {
	for _, elem := range batch {
		err := c.Call(elem.Result, elem.Method, elem.Args...)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) getContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), c.timeout)
}

func (c *Client) post(
	method string,
	params ...any,
) ([]byte, error) {
	ctx, cancel := c.getContext()
	defer cancel()

	msg := map[string]any{
		"method": method,
		"id":     1,
		"params": []any{},
	}

	if params != nil {
		for i := range params {
			if b, ok := params[i].(bool); ok {
				if b {
					params[i] = 1
				} else {
					params[i] = 0
				}
			}
		}
		msg["params"] = params
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal params: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, "POST", c.url, bytes.NewBuffer(body),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header = c.header

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("response status: %d", resp.StatusCode)
		if data != nil {
			err = fmt.Errorf("%w: response body: %s", err, string(data))
		}
		return data, err
	}

	return data, nil
}
