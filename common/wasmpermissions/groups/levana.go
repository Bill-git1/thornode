package groups

import "gitlab.com/thorchain/thornode/v3/common/wasmpermissions/types"

var LevanaPermissions = map[string]types.WasmPermission{
	// levana-perpswap-cosmos-position-token v0.1.1
	"c654a041bb05201afa7a973a1cfc5a1dc8bfc6f9af1f0f614ac8478a47f61ea5": {
		Origin: "https://github.com/Levana-Protocol/levana-perps/tree/02a47aff84645d37210bdbfe9d9c15501fb8a37d/contracts/position_token",
		Deployers: map[string]bool{
			"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
		},
	},

	// levana-perpswap-cosmos-market v0.1.2
	"fe632b2fde3771d2774ab4df619920ea14df3a99a05e4b09420229cb56c33701": {
		Origin: "https://github.com/Levana-Protocol/levana-perps/tree/02a47aff84645d37210bdbfe9d9c15501fb8a37d/contracts/market",
		Deployers: map[string]bool{
			"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
		},
	},

	// levana-perpswap-cosmos-liquidity-token v0.1.1
	"f48d1c4c4bd4c129f421b7026f82614f3ed30759066185f678da7854f61e820a": {
		Origin: "https://github.com/Levana-Protocol/levana-perps/tree/02a47aff84645d37210bdbfe9d9c15501fb8a37d/contracts/liquidity_token",
		Deployers: map[string]bool{
			"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
		},
	},

	// levana-perpswap-cosmos-factory v0.1.1
	"67db51fd0f33477090239930d3e6e4dc29a4175abc59cd2569f515e573083d83": {
		Origin: "https://github.com/Levana-Protocol/levana-perps/tree/02a47aff84645d37210bdbfe9d9c15501fb8a37d/contracts/factory",
		Deployers: map[string]bool{
			"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
		},
	},

	// levana-perpswap-cosmos-countertrade v0.1.0
	"7b2a303549b6e96cdeecaaabb40f862faae7d6f7c079fe28e12da2576caae856": {
		Origin: "https://github.com/Levana-Protocol/levana-perps/tree/02a47aff84645d37210bdbfe9d9c15501fb8a37d/contracts/countertrade",
		Deployers: map[string]bool{
			"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
		},
	},

	// levana-perpswap-cosmos-copy-trading v0.1.0
	"490edc0f489111fe3c99ae783b2f5c9c1b5e414f84c93e30cadce74fad014342": {
		Origin: "https://github.com/Levana-Protocol/levana-perps/tree/02a47aff84645d37210bdbfe9d9c15501fb8a37d/contracts/copy_trading",
		Deployers: map[string]bool{
			"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
		},
	},

	// cw3_flex_multsig v1.1.2
	"c7f3bcc7e4c86194af17de73ea7de34fbe46263ce088b05cdbcf95fbba647df0": {
		Origin: "https://github.com/CosmWasm/cw-plus/tree/bf3dd9656f2910c7ac4ff6e1dfc2d223741199a1/contracts/cw3-flex-multisig",
		Deployers: map[string]bool{
			"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
		},
	},

	// cw4_group v1.1.2
	"dd2216f1114fc68bc4c043701b02e55ce3e5598cdeb616985388215a400db277": {
		Origin: "https://github.com/CosmWasm/cw-plus/tree/bf3dd9656f2910c7ac4ff6e1dfc2d223741199a1/contracts/cw4-group",
		Deployers: map[string]bool{
			"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
		},
	},
}
