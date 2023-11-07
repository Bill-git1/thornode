package constants

import (
	"fmt"

	"github.com/blang/semver"
)

// ConstantName the name we used to get constant values
type ConstantName int

const (
	EmissionCurve ConstantName = iota
	IncentiveCurve
	MaxRuneSupply
	BlocksPerYear
	OutboundTransactionFee
	NativeTransactionFee
	KillSwitchStart    // TODO remove on hard fork
	KillSwitchDuration // TODO remove on hard fork
	PoolCycle
	MinRunePoolDepth
	MaxAvailablePools
	StagedPoolCost
	PendingLiquidityAgeLimit
	MinimumNodesForYggdrasil // TODO remove on hard fork
	MinimumNodesForBFT
	DesiredValidatorSet
	AsgardSize
	DerivedDepthBasisPts
	DerivedMinDepth
	MaxAnchorSlip
	MaxAnchorBlocks
	DynamicMaxAnchorSlipBlocks
	DynamicMaxAnchorTarget
	DynamicMaxAnchorCalcInterval
	ChurnInterval
	ChurnRetryInterval
	ValidatorsChangeWindow
	LeaveProcessPerBlockHeight
	BadValidatorRedline
	LackOfObservationPenalty
	SigningTransactionPeriod
	DoubleSignMaxAge
	PauseBond
	PauseUnbond
	MinimumBondInRune
	FundMigrationInterval
	ArtificialRagnarokBlockHeight
	MaximumLiquidityRune
	StrictBondLiquidityRatio
	DefaultPoolStatus
	MaxOutboundAttempts
	SlashPenalty
	PauseOnSlashThreshold
	FailKeygenSlashPoints
	FailKeysignSlashPoints
	LiquidityLockUpBlocks
	ObserveSlashPoints
	MissBlockSignSlashPoints
	ObservationDelayFlexibility
	StopFundYggdrasil // TODO remove on hard fork
	YggFundLimit      // TODO remove on hard fork
	YggFundRetry      // TODO remove on hard fork
	JailTimeKeygen
	JailTimeKeysign
	NodePauseChainBlocks
	EnableDerivedAssets
	MinSwapsPerBlock
	MaxSwapsPerBlock
	EnableOrderBooks
	MintSynths
	BurnSynths
	MaxSynthPerAssetDepth // TODO: remove me on hard fork
	MaxSynthPerPoolDepth
	MaxSynthsForSaversYield
	VirtualMultSynths
	VirtualMultSynthsBasisPoints
	MinSlashPointsForBadValidator
	FullImpLossProtectionBlocks
	BondLockupPeriod
	MaxBondProviders
	NumberOfNewNodesPerChurn
	MinTxOutVolumeThreshold
	TxOutDelayRate
	TxOutDelayMax
	MaxTxOutOffset
	TNSRegisterFee
	TNSFeeOnSale
	TNSFeePerBlock
	StreamingSwapPause
	StreamingSwapMinBPFee
	StreamingSwapMaxLength
	StreamingSwapMaxLengthNative
	MinCR
	MaxCR
	LoanStreamingSwapsInterval
	PauseLoans
	LoanRepaymentMaturity
	LendingLever
	PermittedSolvencyGap
	NodeOperatorFee
	ValidatorMaxRewardRatio
	PoolDepthForYggFundingMin // TODO remove on hard fork
	MaxNodeToChurnOutForLowVersion
	ChurnOutForLowVersionBlocks
	POLMaxNetworkDeposit
	POLMaxPoolMovement
	POLSynthUtilization // TODO: remove me on hard fork
	POLTargetSynthPerPoolDepth
	POLBuffer
	RagnarokProcessNumOfLPPerIteration
	SwapOutDexAggregationDisabled
	SynthYieldBasisPoints
	SynthYieldCycle
	MinimumL1OutboundFeeUSD
	MinimumPoolLiquidityFee
	ILPCutoff
	ChurnMigrateRounds
	AllowWideBlame
	MaxAffiliateFeeBasisPoints
	TargetOutboundFeeSurplusRune
	MaxOutboundFeeMultiplierBasisPoints
	MinOutboundFeeMultiplierBasisPoints
	NativeOutboundFeeUSD
	NativeTransactionFeeUSD
	TNSRegisterFeeUSD
	TNSFeePerBlockUSD
	EnableUSDFees
	PreferredAssetOutboundFeeMultiplier
	FeeUSDRoundSignificantDigits
	MigrationVaultSecurityBps
	KeygenRetryInterval
	SaversStreamingSwapsInterval
)

var nameToString = map[ConstantName]string{
	EmissionCurve:                       "EmissionCurve",
	IncentiveCurve:                      "IncentiveCurve",
	MaxRuneSupply:                       "MaxRuneSupply",
	BlocksPerYear:                       "BlocksPerYear",
	OutboundTransactionFee:              "OutboundTransactionFee",
	NativeOutboundFeeUSD:                "NativeOutboundFeeUSD",
	NativeTransactionFee:                "NativeTransactionFee",
	NativeTransactionFeeUSD:             "NativeTransactionFeeUSD",
	PoolCycle:                           "PoolCycle",
	MinRunePoolDepth:                    "MinRunePoolDepth",
	MaxAvailablePools:                   "MaxAvailablePools",
	StagedPoolCost:                      "StagedPoolCost",
	PendingLiquidityAgeLimit:            "PendingLiquidityAgeLimit",
	KillSwitchStart:                     "KillSwitchStart",          // TODO remove on hard fork
	KillSwitchDuration:                  "KillSwitchDuration",       // TODO remove on hard fork
	MinimumNodesForYggdrasil:            "MinimumNodesForYggdrasil", // TODO remove on hard fork
	MinimumNodesForBFT:                  "MinimumNodesForBFT",
	DesiredValidatorSet:                 "DesiredValidatorSet",
	AsgardSize:                          "AsgardSize",
	DerivedDepthBasisPts:                "DerivedDepthBasisPts",
	DerivedMinDepth:                     "DerivedMinDepth",
	MaxAnchorSlip:                       "MaxAnchorSlip",
	MaxAnchorBlocks:                     "MaxAnchorBlocks",
	DynamicMaxAnchorSlipBlocks:          "DynamicMaxAnchorSlipBlocks",
	DynamicMaxAnchorTarget:              "DynamicMaxAnchorTarget",
	DynamicMaxAnchorCalcInterval:        "DynamicMaxAnchorCalcInterval",
	ChurnInterval:                       "ChurnInterval",
	ChurnRetryInterval:                  "ChurnRetryInterval",
	ValidatorsChangeWindow:              "ValidatorsChangeWindow",
	LeaveProcessPerBlockHeight:          "LeaveProcessPerBlockHeight",
	BadValidatorRedline:                 "BadValidatorRedline",
	LackOfObservationPenalty:            "LackOfObservationPenalty",
	SigningTransactionPeriod:            "SigningTransactionPeriod",
	DoubleSignMaxAge:                    "DoubleSignMaxAge",
	PauseBond:                           "PauseBond",
	PauseUnbond:                         "PauseUnbond",
	MinimumBondInRune:                   "MinimumBondInRune",
	MaxBondProviders:                    "MaxBondProviders",
	FundMigrationInterval:               "FundMigrationInterval",
	ArtificialRagnarokBlockHeight:       "ArtificialRagnarokBlockHeight",
	MaximumLiquidityRune:                "MaximumLiquidityRune",
	StrictBondLiquidityRatio:            "StrictBondLiquidityRatio",
	DefaultPoolStatus:                   "DefaultPoolStatus",
	MaxOutboundAttempts:                 "MaxOutboundAttempts",
	SlashPenalty:                        "SlashPenalty",
	PauseOnSlashThreshold:               "PauseOnSlashThreshold",
	FailKeygenSlashPoints:               "FailKeygenSlashPoints",
	FailKeysignSlashPoints:              "FailKeysignSlashPoints",
	LiquidityLockUpBlocks:               "LiquidityLockUpBlocks",
	ObserveSlashPoints:                  "ObserveSlashPoints",
	MissBlockSignSlashPoints:            "MissBlockSignSlashPoints",
	ObservationDelayFlexibility:         "ObservationDelayFlexibility",
	StopFundYggdrasil:                   "StopFundYggdrasil", // TODO remove on hard fork
	YggFundLimit:                        "YggFundLimit",      // TODO remove on hard fork
	YggFundRetry:                        "YggFundRetry",      // TODO remove on hard fork
	JailTimeKeygen:                      "JailTimeKeygen",
	JailTimeKeysign:                     "JailTimeKeysign",
	NodePauseChainBlocks:                "NodePauseChainBlocks",
	EnableDerivedAssets:                 "EnableDerivedAssets",
	MinSwapsPerBlock:                    "MinSwapsPerBlock",
	MaxSwapsPerBlock:                    "MaxSwapsPerBlock",
	EnableOrderBooks:                    "EnableOrderBooks",
	MintSynths:                          "MintSynths",
	BurnSynths:                          "BurnSynths",
	VirtualMultSynths:                   "VirtualMultSynths",
	VirtualMultSynthsBasisPoints:        "VirtualMultSynthsBasisPoints",
	MaxSynthPerAssetDepth:               "MaxSynthPerAssetDepth", // TODO: remove me on hard fork
	MaxSynthPerPoolDepth:                "MaxSynthPerPoolDepth",
	MaxSynthsForSaversYield:             "MaxSynthsForSaversYield",
	MinSlashPointsForBadValidator:       "MinSlashPointsForBadValidator",
	FullImpLossProtectionBlocks:         "FullImpLossProtectionBlocks",
	BondLockupPeriod:                    "BondLockupPeriod",
	NumberOfNewNodesPerChurn:            "NumberOfNewNodesPerChurn",
	MinTxOutVolumeThreshold:             "MinTxOutVolumeThreshold",
	TxOutDelayRate:                      "TxOutDelayRate",
	TxOutDelayMax:                       "TxOutDelayMax",
	MaxTxOutOffset:                      "MaxTxOutOffset",
	TNSRegisterFee:                      "TNSRegisterFee",
	TNSRegisterFeeUSD:                   "TNSRegisterFeeUSD",
	TNSFeeOnSale:                        "TNSFeeOnSale",
	TNSFeePerBlock:                      "TNSFeePerBlock",
	TNSFeePerBlockUSD:                   "TNSFeePerBlockUSD",
	PermittedSolvencyGap:                "PermittedSolvencyGap",
	ValidatorMaxRewardRatio:             "ValidatorMaxRewardRatio",
	NodeOperatorFee:                     "NodeOperatorFee",
	PoolDepthForYggFundingMin:           "PoolDepthForYggFundingMin", // TODO remove on hard fork
	MaxNodeToChurnOutForLowVersion:      "MaxNodeToChurnOutForLowVersion",
	ChurnOutForLowVersionBlocks:         "ChurnOutForLowVersionBlocks",
	SwapOutDexAggregationDisabled:       "SwapOutDexAggregationDisabled",
	POLMaxNetworkDeposit:                "POLMaxNetworkDeposit",
	POLMaxPoolMovement:                  "POLMaxPoolMovement",
	POLSynthUtilization:                 "POLSynthUtilization", // TODO: remove me on hard fork
	POLTargetSynthPerPoolDepth:          "POLTargetSynthPerPoolDepth",
	POLBuffer:                           "POLBuffer",
	RagnarokProcessNumOfLPPerIteration:  "RagnarokProcessNumOfLPPerIteration",
	SynthYieldBasisPoints:               "SynthYieldBasisPoints",
	SynthYieldCycle:                     "SynthYieldCycle",
	MinimumL1OutboundFeeUSD:             "MinimumL1OutboundFeeUSD",
	MinimumPoolLiquidityFee:             "MinimumPoolLiquidityFee",
	ILPCutoff:                           "ILPCutoff",
	ChurnMigrateRounds:                  "ChurnMigrateRounds",
	MaxAffiliateFeeBasisPoints:          "MaxAffiliateFeeBasisPoints",
	StreamingSwapPause:                  "StreamingSwapPause",
	StreamingSwapMinBPFee:               "StreamingSwapMinBPFee",
	StreamingSwapMaxLength:              "StreamingSwapMaxLength",
	StreamingSwapMaxLengthNative:        "StreamingSwapMaxLengthNative",
	MinCR:                               "MinCR",
	MaxCR:                               "MaxCR",
	LoanStreamingSwapsInterval:          "LoanStreamingSwapsInterval",
	PauseLoans:                          "PauseLoans",
	LoanRepaymentMaturity:               "LoanRepaymentMaturity",
	LendingLever:                        "LendingLever",
	AllowWideBlame:                      "AllowWideBlame",
	TargetOutboundFeeSurplusRune:        "TargetOutboundFeeSurplusRune",
	MaxOutboundFeeMultiplierBasisPoints: "MaxOutboundFeeMultiplierBasisPoints",
	MinOutboundFeeMultiplierBasisPoints: "MinOutboundFeeMultiplierBasisPoints",
	EnableUSDFees:                       "EnableUSDFees",
	PreferredAssetOutboundFeeMultiplier: "PreferredAssetOutboundFeeMultiplier",
	FeeUSDRoundSignificantDigits:        "FeeUSDRoundSignificantDigits",
	MigrationVaultSecurityBps:           "MigrationVaultSecurityBps",
	KeygenRetryInterval:                 "KeygenRetryInterval",
	SaversStreamingSwapsInterval:        "SaversStreamingSwapsInterval",
}

// String implement fmt.stringer
func (cn ConstantName) String() string {
	val, ok := nameToString[cn]
	if !ok {
		return "NA"
	}
	return val
}

// ConstantValues define methods used to get constant values
type ConstantValues interface {
	fmt.Stringer
	GetInt64Value(name ConstantName) int64
	GetBoolValue(name ConstantName) bool
	GetStringValue(name ConstantName) string
}

// GetConstantValues will return an  implementation of ConstantValues which provide ways to get constant values
// TODO hard fork remove unused version parameter
func GetConstantValues(_ semver.Version) ConstantValues {
	return NewConstantValue()
}
