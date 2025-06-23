package groups

import "gitlab.com/thorchain/thornode/v3/common/wasmpermissions/types"

var DaoDaoPermissions = map[string]types.WasmPermission{
	// cw1-whitelist v1.1.2
	"97e402a61e01722cd7f5bb9b2044ca242d18a9e5b5af41cbf2116b421f1eeb02": {
		Origin: "https://github.com/CosmWasm/cw-plus/tree/d33824679d5b91ca0b4615a8dede7e0028947486/contracts/cw1-whitelist",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// cw4_group v1.1.2
	"dd2216f1114fc68bc4c043701b02e55ce3e5598cdeb616985388215a400db277": {
		Origin: "https://github.com/CosmWasm/cw-plus/tree/d33824679d5b91ca0b4615a8dede7e0028947486/contracts/cw4-group",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// cw721-base v0.18.0
	"ba81e10d053814f1dfbb92f20f77ce1cccf64b27b639db9e2afa8ab5d6ea3cf7": {
		Origin: "https://github.com/public-awesome/cw-nfts/tree/177a993dfb5a1a3164be1baf274f43b1ca53da53/contracts/cw721-base",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// cw-admin-factory v2.7.1
	"cc6914b5dd7cd05d02b7c1700edb32a34b5f6650c967d1929cac24e78f914724": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/external/cw-admin-factory",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// cw-payroll-factory v2.7.1
	"a52870c468a9722384323a2796596db5b9c59cc1ef21eecf3086c843f94242f7": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/external/cw-payroll-factory",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// cw-token-swap v2.7.1
	"08ecf8043234104e21ee1cf76d5eeca249eb801b79a67f93eba42d6f094ef4b3": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/external/cw-token-swap",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// cw-tokenfactory-issuer v2.7.1
	"8f4243c2223699147f22ab3166355ca45f8009b3e3c70d814a73b1aa0554d0fe": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/external/cw-tokenfactory-issuer",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// cw-vesting v2.7.1
	"e8e60f4ffa0521b29db26223397dd322d1b3fc6c5168647fb4572af08227b529": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/external/cw-vesting",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-dao-core v2.7.1
	"976ccfb808d40ffa5ea6a9497ba2c38e5d76a959b30171dc30108c8484d1fa42": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/dao-dao-core",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-pre-propose-approval-multiple v2.7.1
	"2a5125f9e3120c6084875e2d7df6cde4e004903ce66c6405cb1dde24325bc86b": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/pre-propose/dao-pre-propose-approval-multiple",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-pre-propose-approval-single v2.7.1
	"55be8dcac8ef4271d5bf676bb3a77ba957ca7256b0d4d09b16c33d7ae989c9f6": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/pre-propose/dao-pre-propose-approval-single",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-pre-propose-approver v2.7.1
	"b723dbccac803c4d6221dda2941f926f2c54a76fd8bbacc71838f39573d69fdc": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/pre-propose/dao-pre-propose-approver",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-pre-propose-multiple v2.7.1
	"db9f7b69cabb628d83fc0031eaf6fa63da73f32ef20a2b33508e876689646136": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/pre-propose/dao-pre-propose-multiple",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-pre-propose-single v2.7.1
	"f99ba59f1147d590386eee3d9e2f24ab3a4d2b8f682b7132e30313583caa8e5f": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/pre-propose/dao-pre-propose-single",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-proposal-multiple v2.7.1
	"caa16d288e297d981e7d9cf2d1be98ac19ea5fa2857c559833d08cb54897d70a": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/proposal/dao-proposal-multiple",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-proposal-single v2.7.1
	"149599fb6f97e8c60e267015f2ffda55fba98973f69d2d6a584c93049c5a2c20": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/proposal/dao-proposal-single",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-rewards-distributor v2.7.1
	"2110461bc9a99c9fe950f2f2cc88ef2904956957ade1bb5831bd0d90d3ad5139": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/distribution/dao-rewards-distributor",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-vote-delegation v2.7.1
	"b5bf184cae05509fd3e62eb9a00aabdd49a5d3589d2d2e0402c82bd80275e721": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/delegation/dao-vote-delegation",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-voting-cw4 v2.7.1
	"52afbd7f95a7d6cfdd847348d04f5fcdaeb77232d4d54c1d6ab85d637c09aae2": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/voting/dao-voting-cw4",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-voting-cw721-staked v2.7.1
	"2a9b9a2e6c12f58a4930a1af2f0a3e776dba94f78bbcae6df243d601b671d611": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/voting/dao-voting-cw721-staked",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},

	// dao-voting-token-staked v2.7.1
	"3b5c203458bd412f04d368c1e321e0b1a829ee60ae0b34b450d44d607971c513": {
		Origin: "https://github.com/DA0-DA0/dao-contracts/tree/92c44e593e6a0677a437e028514ce207efbd4d66/contracts/voting/dao-voting-token-staked",
		Deployers: map[string]bool{
			"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		},
	},
}
