package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Pacerino/pr0sauce/internal/config"
	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/legacy"
)

const usage = "usage: pr0sauce <migrate|import-legacy|bot|bot-login|api>\n"

var commands = map[string]func(context.Context, []string) error{
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
	if err := cmd(ctx, args[1:]); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", args[0], err)
		return 1
	}
	return 0
}

var errNotImplemented = errors.New("not implemented")

func runMigrate(ctx context.Context, _ []string) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	return db.Migrate(ctx, cfg.DatabaseURL)
}

func runImportLegacy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("import-legacy", flag.ContinueOnError)
	force := fs.Bool("force", false, "import even if songs is not empty")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if err := cfg.RequireLegacy(); err != nil {
		return err
	}
	dst, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer dst.Close()
	src, err := db.Open(ctx, cfg.LegacyDatabaseURL)
	if err != nil {
		return err
	}
	defer src.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rep, err := legacy.Import(ctx, src, dst, log, *force)
	if err != nil {
		return err
	}
	fmt.Printf("import-legacy: Songs=%d Hits=%d DuplicatesDropped=%d Comments=%d DatesFromCreated=%d DatesFromComment=%d DatesFromUpdated=%d DatesEstimated=%d\n",
		rep.Songs, rep.Hits, rep.DuplicatesDropped, rep.Comments,
		rep.DatesFromCreated, rep.DatesFromComment, rep.DatesFromUpdated, rep.DatesEstimated)
	return nil
}

func runBot(context.Context, []string) error      { return errNotImplemented }
func runBotLogin(context.Context, []string) error { return errNotImplemented }
func runAPI(context.Context, []string) error      { return errNotImplemented }
