//go:build !mocknet
// +build !mocknet

package wasmpermissions

var WasmPermissionsRaw = WasmPermissions{
	Permissions: map[string]WasmPermission{
		"dab37041278fe3b13e7a401918b09e8fd232aaec7b00b5826cf9ecd9d34991ba": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/0ff0376fd8316ad6cb4e4c306a215c7cbb3e29f6/contracts/rujira-merge",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},
		"eb361f43e7e2c00347f03903ba07d567fc9f47b1399dc078060bbcaefc6aafe2": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/52716f6b83af191d7c2cc261b15c6f08cf9b9836/contracts/rujira-mint",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},
	},
}
