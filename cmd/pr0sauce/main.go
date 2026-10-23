package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Pacerino/pr0sauce/internal/api"
	"github.com/Pacerino/pr0sauce/internal/bot"
	"github.com/Pacerino/pr0sauce/internal/config"
	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/deezer"
	"github.com/Pacerino/pr0sauce/internal/legacy"
	"github.com/Pacerino/pr0sauce/internal/obs"
	"github.com/getsentry/sentry-go"
)

const usage = "usage: pr0sauce <migrate|import-legacy|bot|bot-login|api|healthcheck>\n"

var commands = map[string]func(context.Context, []string) error{
	"migrate":       runMigrate,
	"import-legacy": runImportLegacy,
	"bot":           runBot,
	"bot-login":     runBotLogin,
	"api":           runAPI,
	"healthcheck":   runHealthcheck,
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

func runBot(ctx context.Context, _ []string) error {
	cfg, err := loadBotConfig()
	if err != nil {
		return err
	}
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, nil)
	enabled, serr := obs.Init(cfg.SentryDSN, cfg.SentryEnvironment, cfg.SentryRelease)
	if enabled {
		defer sentry.Flush(5 * time.Second)
		h = slog.NewMultiHandler(h, obs.NewEventHandler(sentry.CurrentHub()))
	}
	log := slog.New(h)
	if serr != nil {
		log.Warn("sentry disabled: init failed", "err", serr)
	}
	return bot.Run(ctx, cfg, log)
}

func runBotLogin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bot-login", flag.ContinueOnError)
	captchaOut := fs.String("captcha-out", "", "directory for the captcha image file (default: the OS temp dir)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadBotConfig()
	if err != nil {
		return err
	}
	return bot.LoginInteractive(ctx, cfg, os.Stdin, os.Stdout, *captchaOut)
}

func loadBotConfig() (config.Config, error) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return config.Config{}, err
	}
	if err := cfg.RequireBot(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func runAPI(ctx context.Context, _ []string) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if err := cfg.RequireAPI(); err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := db.New(pool)
	srv := &http.Server{
		Addr: cfg.APIAddr,
		Handler: api.New(q, pool, log, api.Options{
			InternalToken: cfg.InternalAPIToken,
			AdminName:     cfg.AdminName,
			BotHealthURL:  cfg.BotHealthURL,
			Pr0:           api.DBPr0Lookup{Q: q, BaseURL: cfg.Pr0BaseURL},
			Deezer:        deezer.New(nil, ""),
		}),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("api listening", "addr", cfg.APIAddr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return err
	}
	log.Info("api stopped")
	return nil
}

// runHealthcheck GETs args[0] with a 3 s timeout and succeeds on any 2xx. It
// exists because the distroless image has no curl for container healthchecks.
func runHealthcheck(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: pr0sauce healthcheck <url>")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args[0], nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unhealthy: %s", resp.Status)
	}
	return nil
}
