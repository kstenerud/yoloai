// ABOUTME: Binary entry point: sets up signal handling and delegates to cli.Execute.
// ABOUTME: Thin wrapper so the main package stays free of business logic.
// Package main is the entry point for the yoloAI CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kstenerud/yoloai/internal/broker"
	"github.com/kstenerud/yoloai/internal/cli"
)

// version, commit, date are set via ldflags at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// __inject runs the binary as a credential-injector sidecar (internal
	// plumbing, not a user command). It is dispatched here, before the cobra/
	// layout bootstrap: the sidecar runs with an empty env (no HOME, so the CLI's
	// layout resolution would panic) and reserves stdout for its address
	// handshake. The entrypoint legitimately owns argv and the process streams.
	if len(os.Args) >= 2 && os.Args[1] == broker.InjectVerb { //nolint:forbidigo // entrypoint owns argv; must dispatch before cobra (§12 boundary)
		if err := broker.RunSidecar(ctx, os.Stdin, os.Stdout); err != nil { //nolint:forbidigo // sidecar transport boundary owns process stdio (§12)
			fmt.Fprintln(os.Stderr, err) //nolint:forbidigo // entrypoint boundary
			return 1
		}
		return 0
	}

	return cli.Execute(ctx, version, commit, date)
}
