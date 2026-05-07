package sidecar

import (
	"encoding/json"
	"testing"
)

func TestBroadcastReqJSONIncludesRequiredFields(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(BroadcastReq{})
	if err != nil {
		t.Fatalf("marshal broadcast request: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal broadcast request map: %v", err)
	}
	if _, ok := decoded["vault_pub_key"]; !ok {
		t.Fatalf("expected vault_pub_key field in broadcast request JSON: %s", string(raw))
	}
	if _, ok := decoded["msg_id"]; !ok {
		t.Fatalf("expected msg_id field in broadcast request JSON: %s", string(raw))
	}
}
