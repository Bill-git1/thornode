package types

// WasmPermission represents permission data for a WASM contract
type WasmPermission struct {
	Origin    string          `json:"origin"`
	Deployers map[string]bool `json:"deployers"`
}
