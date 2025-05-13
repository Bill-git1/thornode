//go:build !mocknet
// +build !mocknet

package wasmpermissions

var WasmPermissionsRaw = WasmPermissions{
	Permissions: map[string]WasmPermission{
		// rujira-mint v1.0.1
		"86dbc41f7c31bde07e426351cb96c2f73d9584a34e46913119225f178d19e8de": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/25252ec557320d3fb507ad906e08ffa4fa4f5494/contracts/rujira-mint",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},
		// rujira-fin (trade) v1.0.0
		"11ddc91557ec8ea845b74ceb6b9f5502672e8a856b0c1752eb0ce19e3ad81dac": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/8cc96cf59037a005051aff2fd16e46ff509a9241/contracts/rujira-fin",
			Deployers: map[string]bool{
				"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			},
		},

		// rujira-bow (pools) v1.0.0
		"49868d92a81ed5613b26772b6e02a43d1ebdb3d61fa13f337ef9b45b9fefb6ff": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/bde18fdb02b9b0213e43308c7ebf5b865886ac97/contracts/rujira-bow",
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
	},
}
