// Command owrt-tui manages OpenWrt routers over SSH. With no command it opens
// the terminal UI; with a command it runs the read-only command-line front end.
package main

import (
	"context"
	"os"

	"github.com/madushakv/owrt-tui/internal/cli"
	"github.com/madushakv/owrt-tui/internal/tui"
)

func main() {
	cli.TUI = tui.Run // used with -demo
	cli.TUIConnect = func(ctx context.Context, o cli.TUIOpts) error {
		return tui.RunConnect(ctx, tui.ConnectOpts{
			Target: o.Target, Identity: o.Identity, Port: o.Port, Write: o.Write, Timeout: o.Timeout,
		})
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
