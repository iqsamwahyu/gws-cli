package config

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds only non-secret fields. The credential itself lives in the OS
// keyring, never in this file.
type Config struct {
	// Account is the Google account the credential belongs to; it is the
	// keyring entry name.
	Account string `mapstructure:"account"`
	// AuthMode is "service_account" or "oauth".
	AuthMode string `mapstructure:"auth_mode"`
}

func dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "gws-cli"), nil
}

func Load() (*Config, error) {
	d, err := dir()
	if err != nil {
		return nil, err
	}
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(d)
	// missing file is fine: the user is simply not logged in yet
	_ = v.ReadInConfig()

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func Save(c *Config) error {
	d, err := dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	v := viper.New()
	v.Set("account", c.Account)
	v.Set("auth_mode", c.AuthMode)
	return v.WriteConfigAs(filepath.Join(d, "config.yaml"))
}
