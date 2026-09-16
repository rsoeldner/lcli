// Package config loads lcli's account configuration and resolves which
// Linear account serves a given team key.
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Account is one Linear workspace login.
type Account struct {
	Name string `toml:"-"`
	// Teams lists the team keys (e.g. "ENG") whose issues live in this account.
	Teams []string `toml:"teams"`
	// KeyCmd is a shell command that prints the API key.
	KeyCmd string `toml:"key_cmd"`
	// KeyEnv names an environment variable holding the API key.
	KeyEnv string `toml:"key_env"`
}

// Config is the parsed config file.
type Config struct {
	Accounts map[string]*Account `toml:"accounts"`
	Path     string              `toml:"-"`
}

// DefaultPath returns $LCLI_CONFIG, or $XDG_CONFIG_HOME/lcli/config.toml,
// or ~/.config/lcli/config.toml.
func DefaultPath() (string, error) {
	if p := os.Getenv("LCLI_CONFIG"); p != "" {
		return p, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "lcli", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "lcli", "config.toml"), nil
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("config file %s not found; create it with an [accounts.<name>] section (see README)", path)
	}
	if err != nil {
		return nil, err
	}
	var c Config
	md, err := toml.Decode(string(data), &c)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("parse %s: unknown keys %v", path, undecoded)
	}
	c.Path = path
	if len(c.Accounts) == 0 {
		return nil, fmt.Errorf("%s defines no [accounts.<name>] sections", path)
	}
	owner := map[string]string{}
	for _, name := range c.Names() {
		a := c.Accounts[name]
		a.Name = name
		if (a.KeyCmd == "") == (a.KeyEnv == "") {
			return nil, fmt.Errorf("%s: account %q must set exactly one of key_cmd or key_env", path, name)
		}
		for i, t := range a.Teams {
			t = strings.ToUpper(strings.TrimSpace(t))
			a.Teams[i] = t
			if t == "" {
				return nil, fmt.Errorf("%s: account %q has an empty team key", path, name)
			}
			if prev, ok := owner[t]; ok {
				if prev == name {
					return nil, fmt.Errorf("%s: account %q lists team key %s twice", path, name, t)
				}
				return nil, fmt.Errorf("%s: team key %s is listed in both accounts %q and %q", path, t, prev, name)
			}
			owner[t] = name
		}
	}
	return &c, nil
}

// Names returns the account names, sorted.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Accounts))
	for n := range c.Accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ResolveError reports that no account could be chosen for a request.
type ResolveError struct{ msg string }

func (e *ResolveError) Error() string { return e.msg }

// Resolve picks the account for teamKey, or the account named override when
// it is non-empty.
func (c *Config) Resolve(teamKey, override string) (*Account, error) {
	if override != "" {
		if a, ok := c.Accounts[override]; ok {
			return a, nil
		}
		return nil, &ResolveError{fmt.Sprintf("unknown account %q; configured accounts: %s", override, strings.Join(c.Names(), ", "))}
	}
	teamKey = strings.ToUpper(teamKey)
	for _, name := range c.Names() {
		for _, t := range c.Accounts[name].Teams {
			if t == teamKey {
				return c.Accounts[name], nil
			}
		}
	}
	return nil, &ResolveError{fmt.Sprintf("unknown team key %q; known: %s (or pass --account)", teamKey, c.knownTeams())}
}

func (c *Config) knownTeams() string {
	var parts []string
	for _, name := range c.Names() {
		if teams := c.Accounts[name].Teams; len(teams) > 0 {
			parts = append(parts, fmt.Sprintf("%s (%s)", strings.Join(teams, ", "), name))
		}
	}
	if len(parts) == 0 {
		return "none configured"
	}
	return strings.Join(parts, "; ")
}

// KeySource describes where the key comes from without revealing it.
func (a *Account) KeySource() string {
	if a.KeyEnv != "" {
		return "env " + a.KeyEnv
	}
	return "command"
}

// APIKey retrieves the account's API key.
func (a *Account) APIKey(ctx context.Context) (string, error) {
	if a.KeyEnv != "" {
		key := strings.TrimSpace(os.Getenv(a.KeyEnv))
		if key == "" {
			return "", fmt.Errorf("account %q: environment variable %s is empty", a.Name, a.KeyEnv)
		}
		return key, nil
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", a.KeyCmd)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// Only the first stderr line: enough to diagnose, without echoing
		// whatever else a password manager may print.
		first, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		return "", fmt.Errorf("account %q: key_cmd failed: %v: %s", a.Name, err, truncate(first, 200))
	}
	key := strings.TrimSpace(stdout.String())
	if key == "" {
		return "", fmt.Errorf("account %q: key_cmd printed an empty key", a.Name)
	}
	return key, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
