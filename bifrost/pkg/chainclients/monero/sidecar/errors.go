package sidecar

import (
	"errors"
	"fmt"
	"io"
)

// ErrUnavailableBlock indicates the requested block height is above the canonical chain tip.
var ErrUnavailableBlock = errors.New("sidecar chain block unavailable")

// maxResponseBytes caps the size of any response body the sidecar (or the
// monerod fallback) is allowed to return, to keep a misbehaving peer from
// exhausting memory.
const maxResponseBytes = 10 * 1024 * 1024

// HTTPError wraps a non-2xx response from the sidecar.
type HTTPError struct {
	Path   string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("sidecar endpoint %s returned status %d: %s", e.Path, e.Status, e.Body)
}

// IsHTTPStatus reports whether err wraps an HTTPError with the given status code.
func IsHTTPStatus(err error, status int) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.Status == status
}

func readBoundedResponseBody(body io.Reader, maxBytes int) ([]byte, error) {
	respBody, err := io.ReadAll(io.LimitReader(body, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(respBody) > maxBytes {
		return nil, fmt.Errorf("sidecar response exceeded %d byte limit", maxBytes)
	}
	return respBody, nil
}
