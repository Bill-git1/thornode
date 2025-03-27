package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/thorchain/thornode/v3/x/denom/types"
)

func TestDecomposeDenoms(t *testing.T) {
	for _, tc := range []struct {
		desc  string
		denom string
		valid bool
	}{
		{
			desc:  "empty is invalid",
			denom: "",
			valid: false,
		},
		{
			desc:  "normal",
			denom: "x/bitcoin",
			valid: true,
		},
		{
			desc:  "multiple slashes in id",
			denom: "x/bitcoin/1",
			valid: true,
		},
		{
			desc:  "no id",
			denom: "x/",
			valid: false,
		},
		{
			desc:  "incorrect prefix",
			denom: "ibc/bitcoin",
			valid: false,
		},
		{
			desc:  "id of only slashes",
			denom: "x/////",
			valid: true,
		},
		{
			desc:  "too long name",
			denom: "x/adsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsfadsf",
			valid: false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := types.DeconstructDenom(tc.denom)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
