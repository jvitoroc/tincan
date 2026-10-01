// Package config stores the client's settings: the servers it knows and the
// certificate fingerprint pinned for each.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const fileName = "config.json"

type Config struct {
	LastServer string             `json:"last_server,omitempty"`
	Servers    map[string]*Server `json:"servers,omitempty"` // keyed by host:port

	path string
}

type Server struct {
	Fingerprint string `json:"fingerprint,omitempty"`
}

// DefaultDir is $TINCAN_CONFIG_DIR if set, otherwise "tincan" inside the OS's
// config directory (~/Library/Application Support on macOS, ~/.config on Linux).
// Setting TINCAN_CONFIG_DIR lets several clients run side by side on one machine.
func DefaultDir() (string, error) {
	if dir := os.Getenv("TINCAN_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tincan"), nil
}

// Load reads the config in dir. A missing file is an empty config.
func Load(dir string) (*Config, error) {
	c := &Config{path: filepath.Join(dir, fileName)}
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("reading %s: %w", c.path, err)
	}
	return c, nil
}

// Save writes the config atomically. The file is private to the user because it
// will also hold session tokens.
func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, fileName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.path)
}

func (c *Config) Path() string { return c.path }

// Fingerprint returns the certificate fingerprint pinned for addr, or "".
func (c *Config) Fingerprint(addr string) string {
	if s := c.Servers[addr]; s != nil {
		return s.Fingerprint
	}
	return ""
}

func (c *Config) PinFingerprint(addr, fingerprint string) {
	if c.Servers == nil {
		c.Servers = make(map[string]*Server)
	}
	if c.Servers[addr] == nil {
		c.Servers[addr] = &Server{}
	}
	c.Servers[addr].Fingerprint = fingerprint
}

// Forget drops everything known about addr and reports whether there was anything.
func (c *Config) Forget(addr string) bool {
	_, ok := c.Servers[addr]
	delete(c.Servers, addr)
	if c.LastServer == addr {
		c.LastServer = ""
	}
	return ok
}
