package keeperv1

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/runtime"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper/types"
)

func (k KVStore) setLoan(ctx cosmos.Context, key []byte, record Loan) {
	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	buf := k.cdc.MustMarshal(&record)
	if buf == nil {
		store.Delete(key)
	} else {
		store.Set(key, buf)
	}
}

func (k KVStore) getLoan(ctx cosmos.Context, key []byte, record *Loan) (bool, error) {
	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	if !store.Has(key) {
		return false, nil
	}

	bz := store.Get(key)
	if err := k.cdc.Unmarshal(bz, record); err != nil {
		return true, dbError(ctx, fmt.Sprintf("Unmarshal kvstore: (%T) %s", record, key), err)
	}
	return true, nil
}

// GetLoanIterator iterate loans
func (k KVStore) GetLoanIterator(ctx cosmos.Context, asset common.Asset) cosmos.Iterator {
	key := k.GetKey(prefixLoan, asset.String())
	return k.getIterator(ctx, types.DbPrefix(key))
}

// GetLoan retrieve loan from the data store
func (k KVStore) GetLoan(ctx cosmos.Context, asset common.Asset, addr common.Address) (Loan, error) {
	record := NewLoan(addr, asset, 0)
	_, err := k.getLoan(ctx, k.GetKey(prefixLoan, record.Key()), &record)
	return record, err
}

// SetLoan save the loan to kv store
func (k KVStore) SetLoan(ctx cosmos.Context, lp Loan) {
	k.setLoan(ctx, k.GetKey(prefixLoan, lp.Key()), lp)
}

// RemoveLoan remove the loan to kv store
func (k KVStore) RemoveLoan(ctx cosmos.Context, lp Loan) {
	k.del(ctx, k.GetKey(prefixLoan, lp.Key()))
}

func (k KVStore) SetTotalCollateral(ctx cosmos.Context, asset common.Asset, amt cosmos.Uint) {
	key := k.GetKey(prefixLoanTotalCollateral, asset.String())
	k.setUint64(ctx, key, amt.Uint64())
}

func (k KVStore) GetTotalCollateral(ctx cosmos.Context, asset common.Asset) (cosmos.Uint, error) {
	var record uint64
	key := k.GetKey(prefixLoanTotalCollateral, asset.String())
	_, err := k.getUint64(ctx, key, &record)
	return cosmos.NewUint(record), err
}
