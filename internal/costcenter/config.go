package costcenter

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is ~/.config/meta-cost-center/config.toml. Every field is optional.
type Config struct {
	Sources         *[]string              `toml:"sources"`
	Prices          *string                `toml:"prices"`
	DB              *string                `toml:"db"`
	KeyguardCommand *string                `toml:"keyguard_command"`
	Sessions        map[string]SessionMeta `toml:"sessions"`
}

func dataHome() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

func defaultConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "meta-cost-center", "config.toml")
}

// LoadConfig reads the config file; a missing file is an empty config.
func LoadConfig(path string) (Config, error) {
	var config Config
	data, err := os.ReadFile(expandHome(path))
	if os.IsNotExist(err) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	if _, err := toml.Decode(string(data), &config); err != nil {
		return config, fmt.Errorf("%s: %v", path, err)
	}
	for _, meta := range config.Sessions {
		if meta.BillingMode != nil && *meta.BillingMode != "payg" && *meta.BillingMode != "subscription" && *meta.BillingMode != "unknown" {
			return config, fmt.Errorf("Config billing_mode must be payg, subscription, or unknown")
		}
	}
	return config, nil
}
