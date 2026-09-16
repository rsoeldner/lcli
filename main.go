// Command lcli reads and comments on Linear issues across several accounts.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/rsoeldner/lcli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.New().Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
