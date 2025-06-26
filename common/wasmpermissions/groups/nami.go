package groups

import "gitlab.com/thorchain/thornode/v3/common/wasmpermissions/types"

var NamiPermissions = map[string]types.WasmPermission{
	// nami-index-nav v1.0.1
	"e452f0568a1d73f4fb1a61f37df4c19ddd3cf48938fca39f3fb23022d4ddc8dc": {
		Origin: "https://github.com/NAMIProtocol/nami-contracts/tree/0e1f2ea4ca9d8a7130d13b0de5c0d4914154c1a0/contracts/nami-index-nav",
		Deployers: map[string]bool{
			"thor1zjwanvezcjp6hefgt6vqfnrrdm8yj9za3s8ss0": true,
		},
	},

	// nami-index-fixed v1.0.0
	"63dd9426926704db38dc25b6c1830d202bbad7d92d8d298056cd7e0de3efd9ce": {
		Origin: "https://github.com/NAMIProtocol/nami-contracts/tree/3efb8706f2438323d5dbae29c337a11a6509de30/contracts/nami-index-fixed",
		Deployers: map[string]bool{
			"thor1zjwanvezcjp6hefgt6vqfnrrdm8yj9za3s8ss0": true,
		},
	},

	// nami-index-entry-adapter v1.0.0
	"e9927b93feeef8fd2e8dcdca4695dddd38d0a832d8e62ad2c0e9cf2826a4f61a": {
		Origin: "https://github.com/NAMIProtocol/nami-contracts/tree/3efb8706f2438323d5dbae29c337a11a6509de30/contracts/nami-index-entry-adapter",
		Deployers: map[string]bool{
			"thor1zjwanvezcjp6hefgt6vqfnrrdm8yj9za3s8ss0": true,
		},
	},

	// nami-affiliate v1.0.0
	"223ea20a4463696fe32b23f845e9f90ae5c83ef0175894a4b0cec114b7dd4b26": {
		Origin: "https://github.com/NAMIProtocol/nami-contracts/tree/3efb8706f2438323d5dbae29c337a11a6509de30/contracts/nami-affiliate",
		Deployers: map[string]bool{
			"thor1zjwanvezcjp6hefgt6vqfnrrdm8yj9za3s8ss0": true,
		},
	},
}
