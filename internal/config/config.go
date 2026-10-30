// Package config handles on-disk state under ~/.alcampo: the user-authored
// config.toml (defaults + limits) and the machine-managed session cache
// (cookies + CSRF token). ALCAMPO_CONFIG_DIR overrides the directory.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const configFile = "config.toml"

// Dir is ~/.alcampo (or $ALCAMPO_CONFIG_DIR).
func Dir() string {
	if d := os.Getenv("ALCAMPO_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".alcampo"
	}
	return filepath.Join(home, ".alcampo")
}

// Config mirrors ~/.alcampo/config.toml.
type Config struct {
	Defaults struct {
		PostalCode string `toml:"postal_code"` // informational; the session cookie carries the real region
	} `toml:"defaults"`
	Session struct {
		// Browser = "firefox" re-reads the session from the Firefox profile on
		// every run (see `alcampo import-firefox --auto`).
		Browser        string `toml:"browser"`
		FirefoxProfile string `toml:"firefox_profile"` // empty = Firefox's default profile
	} `toml:"session"`
	Limits struct {
		MaxEUR float64 `toml:"max_eur"` // refuse carts/checkouts over this total (0 = no limit)
	} `toml:"limits"`
}

// LoadConfig reads config.toml. A missing file is not an error (empty config).
func LoadConfig() (Config, error) {
	var c Config
	p := filepath.Join(Dir(), configFile)
	if _, err := os.Stat(p); err != nil {
		return c, nil
	}
	_, err := toml.DecodeFile(p, &c)
	return c, err
}

// SaveConfig writes config.toml at 0600.
func SaveConfig(c Config) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(Dir(), configFile), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// Load reads a JSON state file (e.g. the session cache) from the config dir.
func Load(name string, v any) error {
	b, err := os.ReadFile(filepath.Join(Dir(), name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Save writes v as pretty JSON to name in the config dir, 0600 (secrets).
func Save(name string, v any) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(Dir(), name), b, 0o600)
}
