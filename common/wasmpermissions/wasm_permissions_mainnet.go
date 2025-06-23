//go:build !mocknet
// +build !mocknet

package wasmpermissions

import (
	"gitlab.com/thorchain/thornode/v3/common/wasmpermissions/groups"
	"gitlab.com/thorchain/thornode/v3/common/wasmpermissions/types"
)

var WasmPermissionsRaw = WasmPermissions{
	Permissions: combinePermissions(),
}

// Collect all permission groups to merge.
var permissionGroups = []map[string]types.WasmPermission{
	groups.RujiraPermissions,
	groups.LevanaPermissions,
	groups.NamiPermissions,
	groups.DaoDaoPermissions,
	// Add permission groups above this line.
}

// combinePermissions merges all project permissions into a single map
func combinePermissions() map[string]types.WasmPermission {
	combined := make(map[string]types.WasmPermission)
	// Merge each permission group into the combined map
	for _, permGroup := range permissionGroups {
		mergePermissionsInto(combined, permGroup)
	}
	return combined
}

// mergePermissionsInto merges permissions from source into target map (modifies target in-place)
func mergePermissionsInto(target, source map[string]types.WasmPermission) {
	// We need to iterate over the permissions map to copy values, and the linter
	// complains with `found map iteration`, but there's no alternative. We are
	// not depending on map order, so this is safe.
	//
	// analyze-ignore(map-iteration)
	for hash, permission := range source {
		existing, exists := target[hash]
		if !exists {
			// Create a new permission with a copy of the deployers map
			target[hash] = types.WasmPermission{
				Origin:    permission.Origin,
				Deployers: copyDeployers(permission.Deployers),
			}
		} else {
			// Merge deployers from the new permission into the existing one
			if existing.Deployers == nil {
				existing.Deployers = make(map[string]bool)
			}
			// We need to iterate over the deployers map to copy values, and the
			// linter complains with `found map iteration`, but there's no
			// alternative. We are not depending on map order, so this is safe.
			//
			// analyze-ignore(map-iteration)
			for deployer, enabled := range permission.Deployers {
				existing.Deployers[deployer] = enabled
			}
			target[hash] = existing
		}
	}
}

// copyDeployers creates a shallow copy of the deployers map
func copyDeployers(deployers map[string]bool) map[string]bool {
	if deployers == nil {
		return make(map[string]bool)
	}

	copy := make(map[string]bool, len(deployers))
	// We need to iterate over the deployers map to copy values, and the linter
	// complains with `found map iteration`, but there's no alternative. We are
	// not depending on map order, so this is safe.
	//
	// analyze-ignore(map-iteration)
	for k, v := range deployers {
		copy[k] = v
	}
	return copy
}
