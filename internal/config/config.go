// Package config persists the awsso TUI configuration
// (SSO start URL, regions, session name) under ~/.config/awsso/config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config holds the user-level settings for the picker.
type Config struct {
	StartURL    string `toml:"start_url"`
	SSORegion   string `toml:"sso_region"`
	EKSRegion   string `toml:"eks_region"`
	SessionName string `toml:"session_name"`
}

// Path returns the location of the config file. The directory is created
// on demand by Save; Path itself does not touch the filesystem.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "awsso", "config.toml"), nil
}

// Load reads the config file. Returns an error wrapped around os.ErrNotExist
// when the file is missing so callers can branch on it.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", os.ErrNotExist, p)
		}
		return nil, err
	}
	defer f.Close()

	var c Config
	if _, err := toml.NewDecoder(f).Decode(&c); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", p, err)
	}
	return &c, nil
}

// Save writes the config file, creating the parent directory if needed.
func Save(c *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}
