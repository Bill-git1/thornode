package groups

import "gitlab.com/thorchain/thornode/v3/common/wasmpermissions/types"

var RujiraPermissions = map[string]types.WasmPermission{
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
			// AUTO x for TCY auto-compounder
			"thor1lt2r7uwly4gwx7kdmdp86md3zzdrqlt3dgr0ag": true,
		},
	},

	// rujira-staking v1.2.0
	"9f3a872c75ab4413dd37936f720b81a051062b1b96554c9cb46c7ccdb4fd017e": {
		Origin: "https://gitlab.com/thorchain/rujira/-/tree/3b2942ba9921a700fcd58d19f06f762d9a1131ff/contracts/rujira-staking",
		Deployers: map[string]bool{
			"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
			// AUTO x for TCY auto-compounder
			"thor1lt2r7uwly4gwx7kdmdp86md3zzdrqlt3dgr0ag": true,
		},
	},

	// rujira-merge v1.0.2
	"26876d1d5cb038ff957d875fe79fb739283b74c6a390c8cf6b96b22735aa3e7e": {
		Origin: "https://gitlab.com/thorchain/rujira/-/tree/ad78b65b2913f8bf6e9f2c3c9f67ade10991ff11/contracts/rujira-merge",
		Deployers: map[string]bool{
			"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
		},
	},

	// rujira-merge v1.0.1
	"46f98e6ac1be26c3108ecb684cedd846ffda220dde5bb6b86644dbe0b0acfd05": {
		Origin: "https://gitlab.com/thorchain/rujira/-/tree/d74d3dc4e2d384aef36af39bc200b59ed8206331/contracts/rujira-merge",
		Deployers: map[string]bool{
			"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
		},
	},
}
