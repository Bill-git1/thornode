//go:build !mocknet
// +build !mocknet

package wasmpermissions

var WasmPermissionsRaw = WasmPermissions{
	Permissions: map[string]WasmPermission{
		// rujira-fin (trade) v1.0.0
		"de9cac49b68ae4dfe5d6d896e0a4f6f4954e809b50dc6733a0c49b2bb0d25bd9": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/80b48eddc0f16f735855442fdbc5423ac5398ff6/contracts/rujira-fin",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},

		// rujira-bow (pools) v1.0.0
		"e302b793dbb23ccbed1878a6eda8ee1437de92d58d087a2d1a98fb346006e49c": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/80b48eddc0f16f735855442fdbc5423ac5398ff6/contracts/rujira-bow",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},

		// rujira-revenue v1.1.0
		"85affbd92e63fd6b8e77430a7290c1c37aab1c7a4580e9443e46a3190ab32b0b": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/80b48eddc0f16f735855442fdbc5423ac5398ff6/contracts/rujira-revenue",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},

		// rujira-staking v1.1.0
		"3e33eee1b1fb4f58fe23e381808a32486c462680515a94fb1103099df6501ad8": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/80b48eddc0f16f735855442fdbc5423ac5398ff6/contracts/rujira-staking",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
				// AUTO team for TCY auto-compounder
				"thor1lt2r7uwly4gwx7kdmdp86md3zzdrqlt3dgr0ag": true,
			},
		},

		// rujira-merge v1.0.1
		"ee360e8c899deb1526f56fd83d7ed482876bb3071b1a2b41645d767f4b68e15b": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/80b48eddc0f16f735855442fdbc5423ac5398ff6/contracts/rujira-merge",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},

		// rujira-merge v1.0.0
		"dab37041278fe3b13e7a401918b09e8fd232aaec7b00b5826cf9ecd9d34991ba": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/0ff0376fd8316ad6cb4e4c306a215c7cbb3e29f6/contracts/rujira-merge",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},

		// rujira-mint v1.0.0
		"eb361f43e7e2c00347f03903ba07d567fc9f47b1399dc078060bbcaefc6aafe2": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/52716f6b83af191d7c2cc261b15c6f08cf9b9836/contracts/rujira-mint",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},
	},
}
