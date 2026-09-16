package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rsoeldner/lcli/internal/config"
	"github.com/rsoeldner/lcli/internal/linear"
)

func (a *App) accountsCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "accounts",
		Short: "List configured accounts and the team keys routed to them (never prints keys)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.loadConfig()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Config: %s\n", c.Path)
			var failed []string
			code := ExitOK
			for _, name := range c.Names() {
				acct := c.Accounts[name]
				teams := strings.Join(acct.Teams, ", ")
				if teams == "" {
					teams = "(none; use --account)"
				}
				fmt.Fprintf(out, "\n%s\n  teams: %s\n  key:   %s\n", name, teams, acct.KeySource())
				if !check {
					continue
				}
				if err := a.checkAccount(cmd, acct); err != nil {
					fmt.Fprintf(out, "  check: FAILED: %v\n", err)
					failed = append(failed, name)
					code = max(code, exitCode(err))
				}
			}
			if len(failed) > 0 {
				return &exitError{code, fmt.Errorf("check failed for account(s): %s", strings.Join(failed, ", "))}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "verify each API key against Linear and show its workspace and teams")
	return cmd
}

func (a *App) checkAccount(cmd *cobra.Command, acct *config.Account) error {
	s, err := a.connect(cmd.Context(), acct)
	if err != nil {
		return err
	}
	resp, err := linear.Viewer(cmd.Context(), s.client)
	if err != nil {
		return apiErr(acct.Name, err)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  check: ok — %s <%s> in workspace %s (%s)\n", resp.Viewer.Name, resp.Viewer.Email, resp.Organization.Name, resp.Organization.UrlKey)
	configured := map[string]bool{}
	for _, t := range acct.Teams {
		configured[t] = true
	}
	var remote []string
	for _, t := range resp.Teams.Nodes {
		mark := ""
		if !configured[t.Key] {
			mark = " (not in config)"
		}
		remote = append(remote, t.Key+mark)
		delete(configured, t.Key)
	}
	fmt.Fprintf(out, "  workspace teams: %s\n", strings.Join(remote, ", "))
	for _, t := range acct.Teams {
		if configured[t] {
			fmt.Fprintf(out, "  warning: configured team %s is not visible to this key\n", t)
		}
	}
	return nil
}
