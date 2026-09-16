// Package cli implements the lcli command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/rsoeldner/lcli/internal/config"
	"github.com/rsoeldner/lcli/internal/ident"
	"github.com/rsoeldner/lcli/internal/linear"
)

// Exit codes.
const (
	ExitOK     = 0
	ExitUsage  = 1 // bad arguments or input
	ExitConfig = 2 // config, key retrieval or authentication
	ExitAPI    = 3 // Linear API or network failure
)

// App holds the process environment so tests can substitute every edge.
type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// ConfigPath overrides config.DefaultPath.
	ConfigPath string
	// Endpoint is the GraphQL endpoint.
	Endpoint string
	HTTP     *http.Client
	// UploadHosts are the hosts serving Linear-uploaded files; only they
	// receive the API key when media is downloaded.
	UploadHosts []string
	// CacheDir is where issue media is downloaded by default.
	CacheDir string
}

// New returns an App wired to the real process environment.
func New() *App {
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	return &App{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Endpoint:    linear.DefaultEndpoint,
		HTTP:        &http.Client{Timeout: 10 * time.Minute},
		UploadHosts: []string{"uploads.linear.app"},
		CacheDir:    cache,
	}
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageErr(format string, args ...any) error {
	return &exitError{ExitUsage, fmt.Errorf(format, args...)}
}

func configErr(err error) error { return &exitError{ExitConfig, err} }

// apiErr classifies an error returned by a Linear API call.
func apiErr(account string, err error) error {
	code := ExitAPI
	if linear.IsAuthError(err) {
		code = ExitConfig
	}
	return &exitError{code, fmt.Errorf("linear (account %s): %s", account, linear.Message(err))}
}

// Run executes lcli with args and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetIn(a.Stdin)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	fmt.Fprintln(a.Stderr, "error:", err)
	return exitCode(err)
}

// exitCode maps err to an exit code. Errors without an exitError are the
// ones cobra produces itself: unknown commands, flags and arg counts.
func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return ExitUsage
}

func (a *App) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "lcli",
		Short: "Read and comment on Linear issues across several Linear accounts",
		Long: `lcli reads Linear issues (including uploaded images) and writes comments
with attached images or videos. The account is chosen from the issue
identifier's team key (ENG-123 -> team ENG) as mapped in the config file;
--account overrides it.

Exit codes: 0 ok, 1 usage/input, 2 config/auth, 3 Linear API/network.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("account", "", "use this configured account instead of resolving it from the team key")
	root.AddCommand(a.accountsCmd(), a.issueCmd(), a.commentCmd(), a.uploadCmd())
	return root
}

func (a *App) loadConfig() (*config.Config, error) {
	path := a.ConfigPath
	if path == "" {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			return nil, configErr(err)
		}
	}
	c, err := config.Load(path)
	if err != nil {
		return nil, configErr(err)
	}
	return c, nil
}

// session is an authenticated connection to the account owning an issue.
type session struct {
	account *config.Account
	client  graphql.Client
	key     string
}

func (a *App) connect(ctx context.Context, acct *config.Account) (*session, error) {
	key, err := acct.APIKey(ctx)
	if err != nil {
		return nil, configErr(err)
	}
	return &session{account: acct, client: linear.NewClient(a.Endpoint, key, a.HTTP), key: key}, nil
}

// open parses rawID and connects to the account that owns it.
func (a *App) open(cmd *cobra.Command, rawID string) (*session, ident.ID, error) {
	id, err := ident.Parse(rawID)
	if err != nil {
		return nil, id, usageErr("%v", err)
	}
	c, err := a.loadConfig()
	if err != nil {
		return nil, id, err
	}
	override, _ := cmd.Flags().GetString("account")
	acct, err := c.Resolve(id.Team, override)
	if err != nil {
		return nil, id, usageErr("%v", err)
	}
	s, err := a.connect(cmd.Context(), acct)
	return s, id, err
}
