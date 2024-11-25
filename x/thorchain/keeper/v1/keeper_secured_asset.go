package keeperv1

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

func (k KVStore) setSecuredAsset(ctx cosmos.Context, key string, record SecuredAsset) {
	store := ctx.KVStore(k.storeKey)
	buf := k.cdc.MustMarshal(&record)
	if buf == nil {
		store.Delete([]byte(key))
	} else {
		store.Set([]byte(key), buf)
	}
}

func (k KVStore) getSecuredAsset(ctx cosmos.Context, key string, record *SecuredAsset) (bool, error) {
	store := ctx.KVStore(k.storeKey)
	if !store.Has([]byte(key)) {
		return false, nil
	}

	bz := store.Get([]byte(key))
	if err := k.cdc.Unmarshal(bz, record); err != nil {
		return true, dbError(ctx, fmt.Sprintf("Unmarshal kvstore: (%T) %s", record, key), err)
	}
	return true, nil
}

func (k KVStore) GetSecuredAssetIterator(ctx cosmos.Context) cosmos.Iterator {
	return k.getIterator(ctx, prefixSecuredAsset)
}

func (k KVStore) GetSecuredAsset(ctx cosmos.Context, asset common.Asset) (SecuredAsset, error) {
	record := NewSecuredAsset(asset)
	_, err := k.getSecuredAsset(ctx, k.GetKey(prefixSecuredAsset, record.Key()), &record)
	return record, err
}

func (k KVStore) SetSecuredAsset(ctx cosmos.Context, ba SecuredAsset) {
	k.setSecuredAsset(ctx, k.GetKey(prefixSecuredAsset, ba.Key()), ba)
}
