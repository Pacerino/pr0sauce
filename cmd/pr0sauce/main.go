package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Pacerino/pr0sauce/internal/config"
	"github.com/Pacerino/pr0sauce/internal/db"
)

const usage = "usage: pr0sauce <migrate|import-legacy|bot|bot-login|api>\n"

var commands = map[string]func(context.Context) error{
	"migrate":       runMigrate,
	"import-legacy": runImportLegacy,
	"bot":           runBot,
	"bot-login":     runBotLogin,
	"api":           runAPI,
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches args to a subcommand and returns the process exit code.
func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
		return 2
	}
	if err := cmd(ctx); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", args[0], err)
		return 1
	}
	return 0
}

var errNotImplemented = errors.New("not implemented")

func runMigrate(ctx context.Context) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	return db.Migrate(ctx, cfg.DatabaseURL)
}

func runImportLegacy(ctx context.Context) error { return errNotImplemented }
func runBot(ctx context.Context) error          { return errNotImplemented }
func runBotLogin(ctx context.Context) error     { return errNotImplemented }
func runAPI(ctx context.Context) error          { return errNotImplemented }
