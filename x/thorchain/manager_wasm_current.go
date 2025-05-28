package thorchain

import (
	"encoding/base32"
	"strings"

	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/errors"
	"gitlab.com/thorchain/thornode/v3/common/wasmpermissions"
)

var _ WasmManager = &WasmMgrVCUR{}

// WasmMgrVCUR is VCUR implementation of slasher
type WasmMgrVCUR struct {
	keeper      keeper.Keeper
	wasmKeeper  wasmkeeper.Keeper
	eventMgr    EventManager
	permissions wasmpermissions.WasmPermissions
}

// newWasmMgrVCUR create a new instance of Slasher
func newWasmMgrVCUR(
	keeper keeper.Keeper,
	wasmKeeper wasmkeeper.Keeper,
	permissions wasmpermissions.WasmPermissions,
	eventMgr EventManager,
) (*WasmMgrVCUR, error) {
	return &WasmMgrVCUR{
		keeper:      keeper,
		wasmKeeper:  wasmKeeper,
		permissions: permissions,
		eventMgr:    eventMgr,
	}, nil
}

// StoreCode stores a new wasm code on chain
func (m WasmMgrVCUR) StoreCode(
	ctx cosmos.Context,
	creator sdk.AccAddress,
	wasmCode []byte,
) (codeID uint64, checksum []byte, err error) {
	if err := m.checkHalt(ctx); err != nil {
		return 0, nil, err
	}

	codeID, checksum, err = m.permissionedKeeper().Create(
		ctx,
		creator,
		wasmCode,
		nil,
	)
	if err != nil {
		return 0, nil, err
	}

	if err := m.checkChecksumHalt(ctx, checksum); err != nil {
		return 0, nil, err
	}

	if err := m.checkAuthorization(ctx, creator, checksum); err != nil {
		return 0, nil, err
	}

	if err := m.permissionedKeeper().PinCode(ctx, codeID); err != nil {
		return 0, nil, err
	}

	return codeID, checksum, nil
}

// InstantiateContract instantiate a new contract with classic sequence based address generation
func (m WasmMgrVCUR) InstantiateContract(
	ctx cosmos.Context,
	codeID uint64,
	creator, admin sdk.AccAddress,
	initMsg []byte,
	label string,
	deposit sdk.Coins,
) (sdk.AccAddress, []byte, error) {
	if err := m.checkHalt(ctx); err != nil {
		return nil, nil, err
	}

	codeInfo, err := m.getCodeInfo(ctx, codeID)
	if err != nil {
		return nil, nil, err
	}

	err = m.checkInstantiateAuthorization(ctx, creator, codeInfo.CodeHash)
	if err != nil {
		return nil, nil, err
	}

	return m.permissionedKeeper().Instantiate(
		ctx,
		codeID,
		creator,
		admin,
		initMsg,
		label,
		deposit,
	)
}

// InstantiateContract2 instantiate a new contract with predicatable address generated
func (m WasmMgrVCUR) InstantiateContract2(
	ctx cosmos.Context,
	codeID uint64,
	creator, admin sdk.AccAddress,
	initMsg []byte,
	label string,
	deposit sdk.Coins,
	salt []byte,
	fixMsg bool,
) (sdk.AccAddress, []byte, error) {
	if err := m.checkHalt(ctx); err != nil {
		return nil, nil, err
	}

	codeInfo, err := m.getCodeInfo(ctx, codeID)
	if err != nil {
		return nil, nil, err
	}

	err = m.checkInstantiateAuthorization(ctx, creator, codeInfo.CodeHash)
	if err != nil {
		return nil, nil, err
	}

	return m.permissionedKeeper().Instantiate2(
		ctx,
		codeID,
		creator,
		admin,
		initMsg,
		label,
		deposit,
		salt,
		fixMsg,
	)
}

func (m WasmMgrVCUR) ExecuteContract(
	ctx cosmos.Context,
	contractAddress, caller sdk.AccAddress,
	msg []byte,
	coins sdk.Coins,
) ([]byte, error) {
	if err := m.checkHalt(ctx); err != nil {
		return nil, err
	}

	// The default `Messenger` configured in wasm keeper, used for routing sub-messages, uses
	// the app's `Router`. Therefore, any SDK submessages that call a contract will route
	// back through this code path and will be halted where necessary
	contractInfo, err := m.getContractInfo(ctx, contractAddress)
	if err != nil {
		return nil, err
	}

	codeInfo, err := m.getCodeInfo(ctx, contractInfo.CodeID)
	if err != nil {
		return nil, err
	}

	if err := m.checkContractHalt(ctx, contractAddress); err != nil {
		return nil, err
	}

	if err := m.checkChecksumHalt(ctx, codeInfo.CodeHash); err != nil {
		return nil, err
	}

	return m.permissionedKeeper().Execute(
		ctx,
		contractAddress,
		caller,
		msg, coins,
	)
}

func (m WasmMgrVCUR) MigrateContract(
	ctx cosmos.Context,
	contractAddress, caller sdk.AccAddress,
	newCodeID uint64,
	msg []byte,
) ([]byte, error) {
	if err := m.checkHalt(ctx); err != nil {
		return nil, err
	}

	codeInfo, err := m.getCodeInfo(ctx, newCodeID)
	if err != nil {
		return nil, err
	}

	contractInfo, err := m.getContractInfo(ctx, contractAddress)
	if err != nil {
		return nil, err
	}

	err = m.checkContractAuthorization(ctx, contractInfo, caller, codeInfo.CodeHash)
	if err != nil {
		return nil, err
	}

	if contractInfo.Admin == "" || contractInfo.Admin != caller.String() {
		return nil, errNotAuthorized
	}

	data, err := m.permissionedKeeper().Migrate(
		ctx,
		contractAddress,
		caller,
		newCodeID,
		msg,
	)
	if err != nil {
		return nil, err
	}

	err = m.maybeUnpin(ctx, contractInfo.CodeID)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// SudoContract calls sudo on a contract.
func (m WasmMgrVCUR) SudoContract(
	ctx cosmos.Context,
	contractAddress, caller sdk.AccAddress,
	msg []byte,
) ([]byte, error) {
	if err := m.checkHalt(ctx); err != nil {
		return nil, err
	}

	contractInfo, err := m.getContractInfo(ctx, contractAddress)
	if err != nil {
		return nil, err
	}

	codeInfo, err := m.getCodeInfo(ctx, contractInfo.CodeID)
	if err != nil {
		return nil, err
	}

	err = m.checkContractAuthorization(ctx, contractInfo, caller, codeInfo.CodeHash)
	if err != nil {
		return nil, err
	}

	return m.permissionedKeeper().Sudo(ctx, contractAddress, msg)
}

func (m WasmMgrVCUR) checkHalt(ctx cosmos.Context) error {
	v, err := m.keeper.GetMimir(ctx, constants.MimirKeyWasmHaltGlobal)
	if err != nil {
		return err
	}
	if v > 0 && ctx.BlockHeight() > v {
		return errorsmod.Wrap(errors.ErrUnauthorized, "wasm halted")
	}
	return nil
}

func (m WasmMgrVCUR) checkContractHalt(ctx cosmos.Context, address sdk.AccAddress) error {
	// Use checksum for brevity and to fit inside mimir's 64 char length
	addrStr := address.String()
	contractKey := addrStr[len(addrStr)-6:]
	v, err := m.keeper.GetMimirWithRef(ctx, constants.MimirTemplateWasmHaltContract, contractKey)
	if err != nil {
		return err
	}
	if v > 0 && ctx.BlockHeight() > v {
		return errorsmod.Wrap(errors.ErrUnauthorized, "contract halted")
	}
	return nil
}

func (m WasmMgrVCUR) checkChecksumHalt(ctx cosmos.Context, checksum []byte) error {
	encoder := base32.StdEncoding
	encoded := encoder.EncodeToString(checksum)
	key := strings.TrimRight(encoded, "=")
	v, err := m.keeper.GetMimirWithRef(ctx, constants.MimirTemplateWasmHaltChecksum, key)
	if err != nil {
		return err
	}
	if v > 0 && ctx.BlockHeight() > v {
		return errorsmod.Wrap(errors.ErrUnauthorized, "checksum halted")
	}
	return nil
}

func (m WasmMgrVCUR) permissionedKeeper() *wasmkeeper.PermissionedKeeper {
	return wasmkeeper.NewGovPermissionKeeper(m.wasmKeeper)
}

func (m WasmMgrVCUR) checkAuthorization(ctx cosmos.Context, actor cosmos.AccAddress, checksum []byte) error {
	v, err := m.keeper.GetMimir(ctx, constants.MimirKeyWasmPermissionless)
	if err != nil {
		return err
	}
	if v > 0 && ctx.BlockHeight() > v {
		return nil
	}

	return m.permissions.Permit(actor, checksum)
}

func (m WasmMgrVCUR) checkInstantiateAuthorization(ctx cosmos.Context, actor cosmos.AccAddress, checksum []byte) error {
	// If the actor is a contract, it can instantiate new contracts without explicit permission
	// wasmKeeper.QueryContractInfo panics if the contract does not exist, so query for non zero length history instead
	result := m.wasmKeeper.GetContractHistory(ctx, actor)
	if len(result) > 0 {
		return nil
	}

	return m.checkAuthorization(ctx, actor, checksum)
}

func (m WasmMgrVCUR) checkContractAuthorization(ctx cosmos.Context, contractInfo *wasmtypes.ContractInfo, actor cosmos.AccAddress, checksum []byte) error {
	// If MimirKeyWasmPermissionless is enabled, we still need to restrict who can call Sudo on and Migrate contracts
	// Check this against the admin value that is stored on instantiation, as is default x/wasm behaviour
	// Current limitations of x/wasm mean we can't access the storage for ContractInfo to support updating of this value
	policy := wasmkeeper.DefaultAuthorizationPolicy{}
	if !policy.CanModifyContract(contractInfo.AdminAddr(), actor) {
		return errors.ErrUnauthorized
	}

	return m.checkAuthorization(ctx, actor, checksum)
}

func (m WasmMgrVCUR) getCodeInfo(ctx cosmos.Context, id uint64) (*wasmtypes.CodeInfo, error) {
	codeInfo := m.wasmKeeper.GetCodeInfo(ctx, id)
	if codeInfo == nil {
		return nil, wasmtypes.ErrNotFound
	}
	return codeInfo, nil
}

func (m WasmMgrVCUR) getContractInfo(ctx cosmos.Context, contractAddress sdk.AccAddress) (*wasmtypes.ContractInfo, error) {
	contractInfo := m.wasmKeeper.GetContractInfo(ctx, contractAddress)
	if contractInfo == nil {
		return nil, wasmtypes.ErrNotFound
	}
	return contractInfo, nil
}

func (m WasmMgrVCUR) maybeUnpin(ctx cosmos.Context, codeId uint64) error {
	var instanceCount int

	m.wasmKeeper.IterateContractsByCode(ctx, codeId, func(address sdk.AccAddress) bool {
		instanceCount++
		return true
	})
	if instanceCount == 0 {
		err := m.permissionedKeeper().UnpinCode(ctx, codeId)
		if err != nil {
			return err
		}
	}
	return nil
}
