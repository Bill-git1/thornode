package mimir

import (
	"fmt"
	"strings"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
)

type MimirType uint8

const (
	UnknownMimir MimirType = iota
	EconomicMimir
	OperationalMimir
)

type Mimir interface {
	LegacyKey(_ string) string
	Tags() []string
	Description() string
	Name() string
	Reference() string
	DefaultValue() int64
	Type() MimirType
	FetchValue(_ cosmos.Context, _ keeper.Keeper) int64
	IsOn(_ cosmos.Context, _ keeper.Keeper) bool
	IsOff(_ cosmos.Context, _ keeper.Keeper) bool
}

type mimir struct {
	name           string
	defaultValue   int64
	reference      string
	id             Id
	mimirType      MimirType
	tags           []string
	description    string
	legacyMimirKey func(ref string) string // mimir v1 key/constant
}

func (m *mimir) LegacyKey(ref string) string {
	return m.legacyMimirKey(ref)
}

func (m *mimir) Tags() []string {
	return m.tags
}

func (m *mimir) Description() string {
	return m.description
}

func (m *mimir) Name() string {
	return strings.ToUpper(fmt.Sprintf("%s%s", m.name, m.Reference()))
}

func (m *mimir) DefaultValue() int64 {
	return m.defaultValue
}

func (m *mimir) Type() MimirType {
	return m.mimirType
}

func (m *mimir) Reference() string {
	if m.reference == "" {
		return "Global"
	}
	return strings.ToUpper(m.reference)
}

func (m *mimir) key() string {
	return fmt.Sprintf("%d-%s", m.id, strings.ToUpper(m.reference))
}

func (m *mimir) FetchValue(ctx cosmos.Context, keeper keeper.Keeper) (value int64) {
	var err error

	// fetch mimir v2
	if keeper.GetVersion().GTE(semver.MustParse("1.124.0")) {
		active, err := keeper.ListActiveValidators(ctx)
		if err != nil {
			ctx.Logger().Error("failed to get active validator set", "error", err)
		}

		mimirs, err := keeper.GetNodeMimirsV2(ctx, m.key())
		if err != nil {
			ctx.Logger().Error("failed to get node mimir v2", "error", err)
		}
		value := int64(-1)
		switch m.Type() {
		case EconomicMimir:
			value = mimirs.ValueOfEconomic(m.key(), active.GetNodeAddresses())
			if value < 0 {
				// no value, fallback to last economic value (if present)
				value, err := keeper.GetMimirV2(ctx, m.key())
				if err != nil {
					ctx.Logger().Error("failed to get mimir v2", "error", err)
				}
				if value >= 0 {
					return value
				}
			} else {
				// value reached, save to persist it beyond loosing 2/3rds
				keeper.SetMimirV2(ctx, m.key(), value)
			}
		case OperationalMimir:
			value = mimirs.ValueOfOperational(m.key(), constants.MinMimirV2Vote, active.GetNodeAddresses())
		}
		if value >= 0 {
			return value
		}
	}

	// fetch legacy mimir (v1)
	value, err = keeper.GetMimir(ctx, m.LegacyKey(m.reference))
	if err != nil {
		ctx.Logger().Error("failed to get mimir V1", "error", err)
		return -1
	}
	if value >= 0 {
		return value
	}

	// use default
	return m.DefaultValue()
}

func (m *mimir) IsOn(ctx cosmos.Context, keeper keeper.Keeper) bool {
	value := m.FetchValue(ctx, keeper)
	return value > 0
}

func (m *mimir) IsOff(ctx cosmos.Context, keeper keeper.Keeper) bool {
	value := m.FetchValue(ctx, keeper)
	return value <= 0
}
