//go:build !mocknet
// +build !mocknet

package wasmpermissions

var WasmPermissionsRaw = WasmPermissions{
	Permissions: map[string]WasmPermission{
		// levana-perpswap-cosmos-position-token v0.1.1
		"c654a041bb05201afa7a973a1cfc5a1dc8bfc6f9af1f0f614ac8478a47f61ea5": {
			Origin: "https://github.com/Levana-Protocol/levana-perps/tree/ff8a96960f5a22835d1c4262ba01e7cb4bb7e7f8/contracts/position_token",
			Deployers: map[string]bool{
				"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
			},
		},

		// levana-perpswap-cosmos-market v0.1.2
		"fe632b2fde3771d2774ab4df619920ea14df3a99a05e4b09420229cb56c33701": {
			Origin: "https://github.com/Levana-Protocol/levana-perps/tree/ff8a96960f5a22835d1c4262ba01e7cb4bb7e7f8/contracts/market",
			Deployers: map[string]bool{
				"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
			},
		},

		// levana-perpswap-cosmos-liquidity-token v0.1.1
		"f48d1c4c4bd4c129f421b7026f82614f3ed30759066185f678da7854f61e820a": {
			Origin: "https://github.com/Levana-Protocol/levana-perps/tree/ff8a96960f5a22835d1c4262ba01e7cb4bb7e7f8/contracts/liquidity_token",
			Deployers: map[string]bool{
				"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
			},
		},

		// levana-perpswap-cosmos-factory v0.1.1
		"67db51fd0f33477090239930d3e6e4dc29a4175abc59cd2569f515e573083d83": {
			Origin: "https://github.com/Levana-Protocol/levana-perps/tree/ff8a96960f5a22835d1c4262ba01e7cb4bb7e7f8/contracts/factory",
			Deployers: map[string]bool{
				"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
			},
		},

		// levana-perpswap-cosmos-countertrade v0.1.0
		"7b2a303549b6e96cdeecaaabb40f862faae7d6f7c079fe28e12da2576caae856": {
			Origin: "https://github.com/Levana-Protocol/levana-perps/tree/ff8a96960f5a22835d1c4262ba01e7cb4bb7e7f8/contracts/countertrade",
			Deployers: map[string]bool{
				"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
			},
		},

		// levana-perpswap-cosmos-copy-trading v0.1.0
		"490edc0f489111fe3c99ae783b2f5c9c1b5e414f84c93e30cadce74fad014342": {
			Origin: "https://github.com/Levana-Protocol/levana-perps/tree/ff8a96960f5a22835d1c4262ba01e7cb4bb7e7f8/contracts/copy_trading",
			Deployers: map[string]bool{
				"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
			},
		},

		// cw3_flex_multsig v1.1.2
		"c7f3bcc7e4c86194af17de73ea7de34fbe46263ce088b05cdbcf95fbba647df0": {
			Origin: "https://github.com/CosmWasm/cw-plus/releases/download/v1.1.2/cw3_flex_multisig.wasm",
			Deployers: map[string]bool{
				"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
			},
		},

		// cw4_group v1.1.2
		"dd2216f1114fc68bc4c043701b02e55ce3e5598cdeb616985388215a400db277": {
			Origin: "https://github.com/CosmWasm/cw-plus/releases/download/v1.1.2/cw4_group.wasm",
			Deployers: map[string]bool{
				"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
			},
		},

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
		"46f98e6ac1be26c3108ecb684cedd846ffda220dde5bb6b86644dbe0b0acfd05": {
			Origin: "https://gitlab.com/thorchain/rujira/-/tree/d74d3dc4e2d384aef36af39bc200b59ed8206331/contracts/rujira-merge",
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
