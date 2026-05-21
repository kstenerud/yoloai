// ABOUTME: Binary entry point: sets up signal handling and delegates to cli.Execute.
// ABOUTME: Thin wrapper so the main package stays free of business logic.
// Package main is the entry point for the yoloAI CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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

	return cli.Execute(ctx, version, commit, date)
}
