package cli

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func viewerOK(teams ...string) func(map[string]any) gqlResult {
	return func(map[string]any) gqlResult {
		nodes := []map[string]any{}
		for _, t := range teams {
			nodes = append(nodes, map[string]any{"key": t, "name": t + " team"})
		}
		return gqlResult{Data: map[string]any{
			"viewer":       map[string]any{"name": "Robert", "email": "r@example.com"},
			"organization": map[string]any{"name": "Example Org", "urlKey": "eng"},
			"teams":        map[string]any{"nodes": nodes},
		}}
	}
}

func TestAccountsListsWithoutSecrets(t *testing.T) {
	app := newTestApp(t, nil)
	r := run(t, app, "accounts")
	wantCode(t, r, ExitOK)
	wantContains(t, r.Stdout, "acme\n  teams: ACME\n  key:   command", "eng\n  teams: ENG, OPS\n  key:   env LCLI_TEST_ENG_KEY")
	if strings.Contains(r.Stdout, "eng-key") || strings.Contains(r.Stdout, "acme-key") {
		t.Fatalf("accounts printed a key:\n%s", r.Stdout)
	}
}

func TestAccountsCheckUsesEachAccountsKey(t *testing.T) {
	f := newFakeLinear(t)
	f.on("Viewer", viewerOK("ENG", "NEW"))
	app := newTestApp(t, f)
	r := run(t, app, "accounts", "--check")
	wantCode(t, r, ExitOK)
	wantContains(t, r.Stdout, "check: ok — Robert <r@example.com> in workspace Example Org (eng)",
		"workspace teams: ENG, NEW (not in config)",
		"warning: configured team OPS is not visible to this key")
	calls := f.callsTo("Viewer")
	if len(calls) != 2 || calls[0].Auth != "acme-key" || calls[1].Auth != "eng-key" {
		t.Fatalf("Viewer calls = %+v, want acme-key then eng-key", calls)
	}
}

func TestAccountsCheckAuthFailureExitsConfig(t *testing.T) {
	f := newFakeLinear(t)
	f.on("Viewer", func(map[string]any) gqlResult {
		return gqlResult{Status: http.StatusBadRequest, Errors: []map[string]any{{
			"message":    "Authentication required, not authenticated",
			"extensions": map[string]any{"code": "AUTHENTICATION_ERROR"},
		}}}
	})
	r := run(t, newTestApp(t, f), "accounts", "--check")
	wantCode(t, r, ExitConfig)
	wantContains(t, r.Stdout, "check: FAILED: linear (account acme): Authentication required")
	wantContains(t, r.Stderr, "check failed for account(s): acme, eng")
}

func TestMissingConfigExitsConfig(t *testing.T) {
	app := newTestApp(t, nil)
	app.ConfigPath = filepath.Join(t.TempDir(), "nope.toml")
	r := run(t, app, "accounts")
	wantCode(t, r, ExitConfig)
	wantContains(t, r.Stderr, "not found")
}

func TestUnknownCommandExitsUsage(t *testing.T) {
	r := run(t, newTestApp(t, nil), "frobnicate")
	wantCode(t, r, ExitUsage)
}
