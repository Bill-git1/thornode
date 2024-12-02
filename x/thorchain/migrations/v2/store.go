package v2

import (
	"fmt"
	"strings"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	prefixStoreVersion = "_ver/"
)

// MigrateStore performs in-place store migrations from v2.137.3 to v3.0.0
// migration includes:
//
// - Remove legacy store migration version
func MigrateStore(ctx sdk.Context, storeKey storetypes.StoreKey) error {
	key := getKey(prefixStoreVersion, "")
	store := ctx.KVStore(storeKey)

	if store.Has([]byte(key)) {
		store.Delete([]byte(key))
	}

	return nil
}

func getKey(prefix, key string) string {
	return fmt.Sprintf("%s/%s", prefix, strings.ToUpper(key))
}
