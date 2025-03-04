package wasmpermissions

import (
	"encoding/hex"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type WasmPermissions struct {
	Permissions map[string]WasmPermission
}

type WasmPermission struct {
	Origin    string          `json:"origin"`
	Deployers map[string]bool `json:"deployers"`
}

func (w WasmPermissions) Permit(actor sdk.AccAddress, checksum []byte) error {
	hexChecksum := hex.EncodeToString(checksum)
	if permission, exists := w.Permissions[hexChecksum]; exists && permission.Deployers[actor.String()] {
		return nil
	}
	return errors.New("unauthorized")
}

func GetWasmPermissions() WasmPermissions {
	return WasmPermissionsRaw
}
