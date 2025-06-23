package tron

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"cosmossdk.io/math"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/tron/api"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/tokenlist"
	"gitlab.com/thorchain/thornode/v3/config"
)

var (
	refBlocksMax            = 10
	refBlockInterval  int64 = 25
	updateGasInterval int64 = 30
)

type ReportSolvency func(int64) error

type RefBlock struct {
	Timestamp int64
	Height    int64
	Id        string
}

type TronBlockScanner struct {
	config                config.BifrostBlockScannerConfiguration
	logger                zerolog.Logger
	bridge                thorclient.ThorchainBridge
	api                   *api.TronApi
	whitelist             map[string]tokenlist.ERC20Token
	abi                   abi.ABI
	refBlocks             []RefBlock
	refAddress            string // needed for energy estimation via api
	currentFee            uint64
	reportSolvency        ReportSolvency
	globalNetworkFeeQueue chan common.NetworkFee
}

func NewTronBlockScanner(
	cfg config.BifrostChainConfiguration,
	bridge thorclient.ThorchainBridge,
	reportSolvency ReportSolvency,
) (*TronBlockScanner, error) {
	logger := log.Logger.With().
		Str("module", "blockscanner").
		Str("chain", cfg.ChainID.String()).
		Logger()

	// The energy estimation API expects an existing address for the api call,
	// which is only needed when at least one TRC-20 token is whitelisted.
	// Before being able to handle tokens, there needs to be a TRX pool, which
	// can be used for the request.
	if refAddress == "" {
		vaults, _ := bridge.GetAsgards()
		for _, vault := range vaults {
			if vault.HasAsset(cfg.ChainID.GetGasAsset()) {
				address, err := vault.PubKey.GetAddress(cfg.ChainID)
				if err != nil {
					continue
				}
				refAddress = address.String()
				break
			}
		}
	}

	// load whitelisted tokens
	tokens := tokenlist.GetEVMTokenList(cfg.ChainID).Tokens

	whitelist := map[string]tokenlist.ERC20Token{}
	for _, token := range tokens {
		for _, address := range cfg.BlockScanner.WhitelistTokens {
			if strings.EqualFold(address, token.Address) {
				whitelist[address] = token
			}
		}
	}

	scanner := TronBlockScanner{
		config:         cfg.BlockScanner,
		logger:         logger,
		whitelist:      whitelist,
		api:            api.NewTronApi(cfg.APIHost, cfg.BlockScanner.HTTPRequestTimeout),
		bridge:         bridge,
		refBlocks:      []RefBlock{},
		refAddress:     refAddress,
		reportSolvency: reportSolvency,
	}

	var err error

	scanner.abi, err = abi.JSON(bytes.NewReader(trc20ContractABI))
	if err != nil {
		logger.Err(err).Msg("failed to parse ABI")
		return nil, err
	}

	return &scanner, nil
}

func (s *TronBlockScanner) GetHeight() (int64, error) {
	block, err := s.api.GetLatestBlock()
	if err != nil {
		s.logger.Err(err).Msg("failed to get latest block")
		return 0, err
	}

	height := block.Header.RawData.Number - ConfirmationBlocks
	if height < 0 {
		height = 0
	}

	return height, nil
}

func (s *TronBlockScanner) FetchMemPool(_ int64) (types.TxIn, error) {
	return types.TxIn{Chain: common.TRONChain}, nil
}

func (s *TronBlockScanner) FetchTxs(
	fetchHeight, _ int64,
) (types.TxIn, error) {
	block, err := s.api.GetBlock(fetchHeight)
	if err != nil {
		s.logger.Err(err).Msg("")
		return types.TxIn{}, err
	}

	txs, err := s.processTxs(block)
	if err != nil {
		s.logger.Err(err).Msg("")
		return types.TxIn{}, err
	}

	txIn := types.TxIn{
		Chain:    s.config.ChainID,
		TxArray:  txs,
		Filtered: false,
		MemPool:  false,
	}

	if fetchHeight%refBlockInterval != 0 {
		return txIn, nil
	}

	// update block history
	if len(s.refBlocks) >= refBlocksMax {
		s.refBlocks = s.refBlocks[len(s.refBlocks)-(refBlocksMax-1):]
	}
	s.refBlocks = append(s.refBlocks, RefBlock{
		Timestamp: block.Header.RawData.Timestamp,
		Height:    block.Header.RawData.Number,
		Id:        block.BlockId,
	})
	sort.Slice(s.refBlocks, func(i, j int) bool {
		return s.refBlocks[i].Height < s.refBlocks[j].Height
	})

	return txIn, nil
}

func (s *TronBlockScanner) GetNetworkFee() (uint64, uint64) {
	return 1, s.currentFee
}

// private
// ----------------------------------------------------------------------------

func (s *TronBlockScanner) processTxs(
	block api.Block,
) ([]*types.TxInItem, error) {
	var txInItems []*types.TxInItem

	height := block.Header.RawData.Number

	contracts := map[string]struct{}{}

	for _, rawTx := range block.Transactions {
		logger := s.logger.With().Str("hash", rawTx.TxId).Logger()

		// only accept direct 1:1 transfers
		if len(rawTx.RawData.Contract) != 1 {
			logger.Warn().
				Int("len", len(rawTx.RawData.Contract)).
				Msg("amount != 1")
			continue
		}

		// we need tx return code
		if len(rawTx.Ret) == 0 {
			logger.Warn().Msg("no return code found")
			continue
		}

		// discard unsuccessful txs
		if rawTx.Ret[0].ContractRet != "SUCCESS" {
			continue
		}

		raw := rawTx.RawData.Contract[0]

		var coins common.Coins
		var dest string
		var err error

		switch raw.Type {
		case "TransferContract":
			dest, err = api.ConvertAddress(raw.Parameter.Value.ToAddress)
			if err != nil {
				logger.Err(err).Msg("failed to convert destination address")
			}

			// 1e6 -> 1e8
			amount := math.NewUint(uint64(raw.Parameter.Value.Amount)).Mul(math.NewUint(100))
			coins = common.Coins{{
				Asset:    common.TRXAsset,
				Amount:   amount,
				Decimals: 6,
			}}

		case "TriggerSmartContract":
			address, err := api.ConvertAddress(raw.Parameter.Value.ContractAddress)
			if err != nil {
				logger.Err(err).Msg("failed to convert contract address")
				continue
			}

			// skip unknown contracts
			token, ok := s.whitelist[address]
			if !ok {
				continue
			}

			// check contract penalty factor later
			contracts[address] = struct{}{}

			method, inputs, err := s.decodeTRC20Input(raw.Parameter.Value.Data)
			if err != nil {
				logger.Err(err).Msg("failed to get inputs")
				continue
			}

			if method != "transfer" {
				continue
			}

			to, ok := inputs["_to"]
			if !ok {
				logger.Error().Msg("no destination address found")
				continue
			}

			dest, err = api.ConvertAddress(fmt.Sprintf("%v", to))
			if err != nil {
				logger.Err(err).Msg("failed to convert address")
				continue
			}

			value, ok := inputs["_value"]
			if !ok {
				logger.Error().Msg("no amount found")
				continue
			}

			amount := new(big.Int)
			amount, ok = amount.SetString(fmt.Sprintf("%v", value), 10)
			if !ok {
				logger.Error().Msg("failed to convert amount")
				continue
			}

			amount, err = common.ConvertDecimals(
				amount, token.Decimals, common.THORChainDecimals,
			)
			if err != nil {
				logger.Err(err).Msg("failed to convert amount to decimals")
				continue
			}

			coins = common.Coins{{
				Asset:    token.Asset(common.TRONChain),
				Amount:   math.NewUintFromBigInt(amount),
				Decimals: int64(token.Decimals),
			}}

		default:
			continue
		}

		memo, err := hex.DecodeString(rawTx.RawData.Data)
		if err != nil {
			logger.Warn().Msg("Decode data failed")
			continue
		}

		if len(memo) == 0 {
			continue
		}

		// get fee
		info, err := s.api.GetTransactionInfo(rawTx.TxId)
		if err != nil {
			logger.Err(err).Msg("failed to get transaction info")
			continue
		}

		gasAmount := math.NewUint(info.Fee).Mul(math.NewUint(100))
		if gasAmount.IsZero() {
			gasAmount = math.NewUint(1)
		}
		gas := common.Gas{{
			Amount:   gasAmount,
			Asset:    common.TRXAsset,
			Decimals: 6,
		}}

		sender, err := api.ConvertAddress(raw.Parameter.Value.OwnerAddress)
		if err != nil {
			logger.Err(err).Msg("failed to convert sender address")
			continue
		}

		txInItems = append(txInItems, &types.TxInItem{
			Tx:          rawTx.TxId,
			BlockHeight: height,
			Memo:        strings.TrimSpace(string(memo)),
			Sender:      sender,
			To:          dest,
			Coins:       coins,
			Gas:         gas,
		})
	}

	if height%updateGasInterval == 0 {
		s.updateFee(height)
	}

	err := s.reportSolvency(height)
	if err != nil {
		s.logger.Err(err).Msg("fail to send solvency to THORChain")
	}

	return txInItems, nil
}

func (s *TronBlockScanner) decodeTRC20Input(
	data string,
) (string, map[string]interface{}, error) {
	bz, err := hex.DecodeString(data)
	if err != nil {
		s.logger.Err(err).Msg("failed to decode raw input data")
		return "", nil, err
	}

	methodSigData := bz[:4]
	inputsHexData := bz[4:]

	method, err := s.abi.MethodById(methodSigData)
	if err != nil {
		s.logger.Err(err).Msg("failed to lookup method")
		return "", nil, err
	}

	inputs := make(map[string]interface{})
	err = method.Inputs.UnpackIntoMap(inputs, inputsHexData)
	if err != nil {
		s.logger.Err(err).Msg("failed to unpack inputs")
		return "", nil, err
	}

	return method.Name, inputs, nil
}

func (s *TronBlockScanner) updateFee(height int64) {
	params, err := s.api.GetChainParameters()
	if err != nil {
		s.logger.Err(err).Msg("failed get chain parameters")
	}

	var fee, bandwidth, energy int64

	// bandwidth calculation:
	// len(raw_data) + protobuf overhead + max_result_size + signature length
	// len(raw_data) + 3 bytes + 64 bytes + 67 bytes
	if len(s.whitelist) == 0 {
		// only TRX transfers
		// => 150 + 3 + 64 + 67 = 284
		bandwidth = 284 * params.BandwidthFee
	} else {
		// hex data is longer and penalty factor is applied
		// => 211 + 3 + 64 + 67 = 361
		bandwidth = 345 * params.BandwidthFee

		maxEnergy, err := s.getMaxEnergy()
		if err != nil || maxEnergy <= 0 {
			s.logger.Err(err).Msg("failed to get max energy")
			return
		}

		energy = maxEnergy * params.EnergyFee
	}

	// add 1.1 TRX in case the new account needs to be activated:
	// https://developers.tron.network/docs/account#account-activation
	fee = energy + bandwidth + params.MemoFee + 1_100_000

	if fee <= 0 {
		s.logger.Error().Msg("fee is zero")
		return
	}

	s.currentFee = uint64(fee * 100)

	s.globalNetworkFeeQueue <- common.NetworkFee{
		Chain:           s.config.ChainID,
		Height:          height,
		TransactionSize: 1,
		TransactionRate: s.currentFee,
	}

	s.logger.Info().
		Int64("height", height).
		Int64("fee", fee).
		Msg("updated network fee")
}

func (s *TronBlockScanner) getMaxEnergy() (int64, error) {
	s.logger.Info().Msg("updating fee for block")

	// get max energy usage of all whitelisted tokens
	maxEnergy := int64(0)

	hexAddress, err := api.ConvertAddress(s.refAddress)
	if err != nil {
		s.logger.Err(err).Msg("failed to convert address")
		return 0, err
	}

	input := fmt.Sprintf("%024x%s%064x", 0, hexAddress[2:], 1)

	for _, token := range s.whitelist {
		energy, err := s.api.EstimateEnergy(
			s.refAddress,
			token.Address,
			"transfer(address,uint256)",
			input,
		)
		if err != nil {
			s.logger.Err(err).Msg("failed to estimate energy")
			return 0, err
		}

		// it takes almost twice the energy to send a token to a wallet
		// for the first time (x2 for safety)
		maxEnergy = max(maxEnergy, energy*2)
	}

	return maxEnergy, nil
}
