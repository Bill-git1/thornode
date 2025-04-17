package ebifrost

import (
	"fmt"

	"github.com/spf13/cast"
	"github.com/spf13/cobra"

	servertypes "github.com/cosmos/cosmos-sdk/server/types"
)

const (
	flagEnabled = "ebifrost.enable"
	flagAddress = "ebifrost.address"
)

type EBifrostConfig struct {
	Enable  bool   `json:"enable"`
	Address string `json:"address"`
}

func DefaultEBifrostConfig() EBifrostConfig {
	return EBifrostConfig{
		Enable:  true,
		Address: "localhost:50051",
	}
}

// ConfigTemplate toml snippet for app.toml
func ConfigTemplate(c EBifrostConfig) string {
	return fmt.Sprintf(`
[ebifrost]
# Whether the local enshrined bifrost GRPC listener is enabled
enabled = %t

# Address of the enshrined bifrost GRPC listener
address = "%s"
`, c.Enable, c.Address)
}

func DefaultConfigTemplate() string {
	return ConfigTemplate(DefaultEBifrostConfig())
}

// ____________________________________________________________________________

// AddModuleInitFlags implements servertypes.ModuleInitFlags interface.
func AddModuleInitFlags(startCmd *cobra.Command) {
	defaults := DefaultEBifrostConfig()
	startCmd.Flags().Bool(flagEnabled, defaults.Enable, "Enable the local enshrined bifrost GRPC listener")
	startCmd.Flags().String(flagAddress, defaults.Address, "Address of the enshrined bifrost GRPC listener")
}

// ReadEBifrostConfig reads the ebifrost specific configuration
func ReadEBifrostConfig(opts servertypes.AppOptions) (EBifrostConfig, error) {
	cfg := DefaultEBifrostConfig()

	if v := opts.Get(flagEnabled); v != nil {
		var err error
		if cfg.Enable, err = cast.ToBoolE(v); err != nil {
			return cfg, err
		}
	}

	if v := opts.Get(flagAddress); v != nil {
		var ok bool
		if cfg.Address, ok = v.(string); !ok {
			return cfg, fmt.Errorf("expected string for %s, got %T", flagAddress, v)
		}
	}

	return cfg, nil
}
