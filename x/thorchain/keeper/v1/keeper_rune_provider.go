package keeperv1

import (
	"fmt"

	"gitlab.com/thorchain/thornode/common/cosmos"
)

func (k KVStore) setRUNEProvider(ctx cosmos.Context, key string, record RUNEProvider) {
	store := ctx.KVStore(k.storeKey)
	buf := k.cdc.MustMarshal(&record)
	if buf == nil {
		store.Delete([]byte(key))
	} else {
		store.Set([]byte(key), buf)
	}
}

func (k KVStore) getRUNEProvider(ctx cosmos.Context, key string, record *RUNEProvider) (bool, error) {
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

// GetRUNEProviderIterator iterate RUNE providers
func (k KVStore) GetRUNEProviderIterator(ctx cosmos.Context) cosmos.Iterator {
	return k.getIterator(ctx, prefixRUNEProvider)
}

func (k KVStore) getRUNEProviders(ctx cosmos.Context) ([]RUNEProvider, error) {
	rps := make([]RUNEProvider, 0)
	iterator := k.GetRUNEProviderIterator(ctx)
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var rp RUNEProvider
		k.Cdc().MustUnmarshal(iterator.Value(), &rp)
		if rp.RuneAddress.Empty() {
			continue
		}
		rps = append(rps, rp)
	}
	return rps, nil
}

func (k KVStore) GetRUNEProviderUnitsTotal(ctx cosmos.Context) (cosmos.Uint, error) {
	rps, err := k.getRUNEProviders(ctx)
	if err != nil {
		return cosmos.ZeroUint(), fmt.Errorf("unable to getRUNEProviders: %s", err)
	}
	units := cosmos.ZeroUint()
	for _, rp := range rps {
		units = units.Add(rp.Units)
	}
	return units, nil
}

// GetRUNEProvider retrieve RUNE provider from the data store
func (k KVStore) GetRUNEProvider(ctx cosmos.Context, addr cosmos.AccAddress) (RUNEProvider, error) {
	record := RUNEProvider{
		RuneAddress:    addr,
		DepositAmount:  cosmos.ZeroUint(),
		WithdrawAmount: cosmos.ZeroUint(),
		Units:          cosmos.ZeroUint(),
	}

	_, err := k.getRUNEProvider(ctx, k.GetKey(ctx, prefixRUNEProvider, record.Key()), &record)
	if err != nil {
		return record, err
	}

	return record, nil
}

// SetRUNEProvider save the RUNE provider to kv store
func (k KVStore) SetRUNEProvider(ctx cosmos.Context, rp RUNEProvider) {
	k.setRUNEProvider(ctx, k.GetKey(ctx, prefixRUNEProvider, rp.Key()), rp)
}

// RemoveRUNEProvider remove the RUNE provider from the kv store
func (k KVStore) RemoveRUNEProvider(ctx cosmos.Context, rp RUNEProvider) {
	k.del(ctx, k.GetKey(ctx, prefixRUNEProvider, rp.Key()))
}
