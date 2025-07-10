//go:build !mocknet
// +build !mocknet

package wasmpermissions

var WasmPermissionsRaw = WasmPermissions{
	Store: map[string]bool{
		// Rujira
		"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
	},
	Instantiate: map[string]bool{
		// DAODAO
		"thor1gg2hk8nnap6u6axlkv0rjfghd2vjlwkyshhe8s": true,
		// Ruji Perps
		"thor1440jp0ukj8ew3z2fd4zmdqgxhn5ghd7ghg2kmr": true,
		// Nami
		"thor1zjwanvezcjp6hefgt6vqfnrrdm8yj9za3s8ss0": true,
		// Rujira
		"thor1e0lmk5juawc46jwjwd0xfz587njej7ay5fh6cd": true,
		// Auto 1
		"thor1lt2r7uwly4gwx7kdmdp86md3zzdrqlt3dgr0ag": true,
	},
}
