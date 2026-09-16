package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const twoAccounts = `
[accounts.eng]
teams = ["eng", "OPS"]
key_env = "LCLI_CFG_TEST_KEY"

[accounts.acme]
teams = ["ACME"]
key_cmd = "echo '  acme-secret  '"
`

func TestResolve(t *testing.T) {
	c, err := Load(write(t, twoAccounts))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ team, override, want string }{
		{"ENG", "", "eng"},
		{"eng", "", "eng"},
		{"OPS", "", "eng"},
		{"ACME", "", "acme"},
		{"ENG", "acme", "acme"},
		{"NOPE", "eng", "eng"},
	} {
		a, err := c.Resolve(tc.team, tc.override)
		if err != nil || a.Name != tc.want {
			t.Errorf("Resolve(%q, %q) = %v, %v; want %s", tc.team, tc.override, a, err, tc.want)
		}
	}

	_, err = c.Resolve("HBX", "")
	var re *ResolveError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), `unknown team key "HBX"; known: ACME (acme); ENG, OPS (eng)`) {
		t.Errorf("unknown team error = %v", err)
	}
	_, err = c.Resolve("ENG", "other")
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "configured accounts: acme, eng") {
		t.Errorf("unknown account error = %v", err)
	}
}

func TestLoadValidation(t *testing.T) {
	for name, tc := range map[string]struct{ content, wantErr string }{
		"no accounts":  {"", "defines no"},
		"no key":       {"[accounts.a]\nteams=['A']\n", "exactly one of key_cmd or key_env"},
		"both keys":    {"[accounts.a]\nkey_cmd='x'\nkey_env='Y'\n", "exactly one of key_cmd or key_env"},
		"dup team":     {"[accounts.a]\nteams=['A']\nkey_env='X'\n[accounts.b]\nteams=['a']\nkey_env='Y'\n", "team key A is listed in both"},
		"unknown key":  {"[accounts.a]\nkey_env='X'\napi_key='oops'\n", "unknown keys"},
		"invalid toml": {"[accounts.a\n", "parse"},
		"empty team":   {"[accounts.a]\nteams=[' ']\nkey_env='X'\n", "empty team key"},
		"team twice":   {"[accounts.a]\nteams=['A','a']\nkey_env='X'\n", `account "a" lists team key A twice`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, tc.content))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestAPIKey(t *testing.T) {
	c, err := Load(write(t, twoAccounts))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	key, err := c.Accounts["acme"].APIKey(ctx)
	if err != nil || key != "acme-secret" {
		t.Errorf("key_cmd key = %q, %v", key, err)
	}

	t.Setenv("LCLI_CFG_TEST_KEY", "eng-secret\n")
	key, err = c.Accounts["eng"].APIKey(ctx)
	if err != nil || key != "eng-secret" {
		t.Errorf("key_env key = %q, %v", key, err)
	}

	t.Setenv("LCLI_CFG_TEST_KEY", "  \n")
	if _, err := c.Accounts["eng"].APIKey(ctx); err == nil || !strings.Contains(err.Error(), "LCLI_CFG_TEST_KEY is empty") {
		t.Errorf("empty env error = %v", err)
	}

	failing := &Account{Name: "x", KeyCmd: "echo nope >&2; echo second-line-secret >&2; exit 3"}
	if _, err := failing.APIKey(ctx); err == nil || !strings.Contains(err.Error(), "key_cmd failed") ||
		!strings.Contains(err.Error(), "nope") || strings.Contains(err.Error(), "second-line-secret") {
		t.Errorf("failing key_cmd error = %v", err)
	}
	empty := &Account{Name: "x", KeyCmd: "true"}
	if _, err := empty.APIKey(ctx); err == nil {
		t.Error("empty key_cmd output accepted")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("LCLI_CONFIG", "/explicit.toml")
	if p, _ := DefaultPath(); p != "/explicit.toml" {
		t.Errorf("LCLI_CONFIG path = %s", p)
	}
	t.Setenv("LCLI_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, _ := DefaultPath(); p != "/xdg/lcli/config.toml" {
		t.Errorf("XDG path = %s", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/u")
	if p, _ := DefaultPath(); p != "/home/u/.config/lcli/config.toml" {
		t.Errorf("home path = %s", p)
	}
}
