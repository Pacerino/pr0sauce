package bot

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Pacerino/pr0sauce/internal/config"
	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/odesli"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
	"github.com/Pacerino/pr0sauce/internal/recognize"
)

const defaultPersistEvery = 10 * time.Minute

type checkResult struct {
	valid bool
	err   error
}

const (
	sessionCookiesKey = "session_cookies"

	defaultRecheck    = 10 * time.Minute // re-check while the session is invalid
	defaultBackoffMin = 30 * time.Second // re-check while the session is unknown
	defaultBackoffMax = 10 * time.Minute

	loginHint = "pr0gramm session invalid — run: docker compose run --rm -it bot bot-login"
)

// session is the part of the pr0gramm client the session handling needs.
type session interface {
	SessionValid(ctx context.Context) (bool, error)
	Login(ctx context.Context, name, password, token, captcha string) error
	Captcha(ctx context.Context) (pr0gramm.Captcha, error)
	ExportCookies() ([]byte, error)
	ImportCookies([]byte) error
}

// runner owns the process: session state machine, health endpoint, main loop.
// Intervals and the clock are fields so tests can use millisecond scales.
type runner struct {
	cfg     config.Config
	log     *slog.Logger
	bot     *Bot
	store   Store
	sess    session
	health  *health
	clock   Clock
	ln      net.Listener // health listener; nil means listen on cfg.BotHealthAddr
	poll    time.Duration
	sync    time.Duration
	recheck time.Duration
	boMin   time.Duration
	boMax   time.Duration

	persistEvery time.Duration // min. interval between rotated-cookie persists

	loginUsed      bool // the one automatic login has been spent
	startupUnknown bool // startup never got a definitive session answer yet
	lastCookies    string
	lastPersist    time.Time
	checked        *checkResult // SessionValid result of the current tick

	nextCheck time.Time     // earliest next SessionValid while not ok
	backoff   time.Duration // current backoff while unknown
}

// Run is the bot process. It returns an error only when startup fails (DB
// unreachable, bad config); a cancelled ctx is a clean shutdown and returns nil.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	client := pr0gramm.New(nil, "")
	store := NewStore(db.New(pool))
	b, err := New(Deps{
		Store:      store,
		Pr0:        client,
		Recognizer: &recognize.Client{BaseURL: cfg.ShazamURL},
		Linker:     odesli.New(nil, ""),
	}, cfg, log)
	if err != nil {
		return err
	}
	clock := systemClock{}
	r := &runner{
		cfg: cfg, log: log, bot: b, store: store, sess: client, clock: clock,
		health:       &health{pinger: pool, clock: clock},
		poll:         cfg.PollInterval,
		sync:         cfg.CommentSyncInterval,
		persistEvery: defaultPersistEvery,
		recheck:      defaultRecheck, boMin: defaultBackoffMin, boMax: defaultBackoffMax,
	}
	return r.run(ctx)
}

func (r *runner) run(ctx context.Context) error {
	if err := r.bot.RecoverReplying(ctx); err != nil {
		return err
	}

	srv, err := r.startHealth()
	if err != nil {
		return err
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			r.log.Warn("health server shutdown", "err", err)
		}
	}()

	r.startup(ctx)

	pollT := time.NewTicker(r.poll)
	defer pollT.Stop()
	syncT := time.NewTicker(r.sync)
	defer syncT.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-pollT.C:
			r.tick(ctx)
		case <-syncT.C:
			r.syncTick(ctx)
		}
	}
}

func (r *runner) startHealth() (*http.Server, error) {
	ln := r.ln
	if ln == nil {
		var err error
		if ln, err = net.Listen("tcp", r.cfg.BotHealthAddr); err != nil {
			return nil, fmt.Errorf("bot: health listen: %w", err)
		}
	}
	srv := &http.Server{Handler: r.health.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.log.Error("health server", "err", err)
		}
	}()
	return srv, nil
}

// startup imports persisted cookies and settles the session. It is the only
// place the bot ever logs in by itself, and at most once.
func (r *runner) startup(ctx context.Context) {
	r.importCookies(ctx)
	r.lastPersist = r.clock.Now()
	valid, err := r.sess.SessionValid(ctx)
	switch {
	case err != nil:
		r.setUnknown(err)
		r.startupUnknown = true
	case valid:
		r.setOK()
	default:
		r.loginOnce(ctx)
	}
}

func (r *runner) loginOnce(ctx context.Context) {
	r.loginUsed = true
	r.log.Info("session invalid, logging in once")
	if err := r.sess.Login(ctx, r.cfg.Pr0User, r.cfg.Pr0Password, "", ""); err != nil {
		r.log.Error("automatic login failed", "err", err)
		r.setInvalid()
		return
	}
	if err := r.persistCookies(ctx); err != nil {
		r.log.Error("persisting session cookies failed", "err", err)
	}
	valid, err := r.checkSession(ctx)
	switch {
	case err != nil:
		r.setUnknown(err)
	case valid:
		r.setOK()
	default:
		r.setInvalid()
	}
}

func (r *runner) persistCookies(ctx context.Context) error {
	data, err := r.sess.ExportCookies()
	if err != nil {
		return err
	}
	if err := r.store.SetState(ctx, db.SetStateParams{Key: sessionCookiesKey, Value: string(data)}); err != nil {
		return err
	}
	r.lastCookies, r.lastPersist = string(data), r.clock.Now()
	return nil
}

// maybePersistCookies writes the client's cookies back when pr0gramm rotated
// them, at most every persistEvery. A concurrent bot-login write could be
// overwritten here, but only while the bot's own session is valid, which is
// acceptable.
func (r *runner) maybePersistCookies(ctx context.Context) {
	if r.clock.Now().Sub(r.lastPersist) < r.persistEvery {
		return
	}
	data, err := r.sess.ExportCookies()
	if err != nil {
		r.log.Warn("exporting session cookies", "err", err)
		return
	}
	if string(data) == r.lastCookies {
		r.lastPersist = r.clock.Now()
		return
	}
	if err := r.persistCookies(ctx); err != nil {
		r.log.Warn("persisting rotated session cookies", "err", err)
	}
}

// checkSession runs SessionValid at most once per tick; later callers in the
// same tick get the first result.
func (r *runner) checkSession(ctx context.Context) (bool, error) {
	if r.checked == nil {
		v, err := r.sess.SessionValid(ctx)
		r.checked = &checkResult{v, err}
	}
	return r.checked.valid, r.checked.err
}

// importCookies loads session_cookies from the DB into the client, if present.
func (r *runner) importCookies(ctx context.Context) {
	v, err := r.store.GetState(ctx, sessionCookiesKey)
	if errors.Is(err, ErrNotFound) {
		return
	}
	if err != nil {
		r.log.Warn("reading session cookies", "err", err)
		return
	}
	if err := r.sess.ImportCookies([]byte(v)); err != nil {
		r.log.Warn("importing session cookies", "err", err)
		return
	}
	// Remember the normalised form so an unchanged jar is not rewritten.
	if data, err := r.sess.ExportCookies(); err == nil {
		r.lastCookies = string(data)
	}
}

func (r *runner) setOK() {
	r.health.setSession(sessionOK)
	r.startupUnknown = false
	r.backoff = 0
	r.log.Info("pr0gramm session ok")
}

func (r *runner) setInvalid() {
	r.health.setSession(sessionInvalid)
	r.startupUnknown = false
	r.nextCheck = r.clock.Now().Add(r.recheck)
	r.log.Error(loginHint)
}

func (r *runner) setUnknown(cause error) {
	if r.backoff == 0 {
		r.backoff = r.boMin
	} else {
		r.backoff = min(r.backoff*2, r.boMax)
	}
	r.health.setSession(sessionUnknown)
	r.nextCheck = r.clock.Now().Add(r.backoff)
	r.log.Warn("pr0gramm session state unknown, will re-check", "err", cause, "in", r.backoff)
}

// tick runs one poll round, or a session re-check when the session is not ok.
func (r *runner) tick(ctx context.Context) {
	r.checked = nil
	if r.health.getSession() != sessionOK {
		r.recheckSession(ctx)
		if r.health.getSession() != sessionOK {
			return
		}
	}

	if err := r.bot.Ingest(ctx); err != nil {
		if r.stepFailed(ctx, "ingest", err) {
			return
		}
	} else {
		r.health.markPoll(r.clock.Now())
	}
	if err := r.bot.ProcessDue(ctx); err != nil {
		if r.stepFailed(ctx, "process", err) {
			return
		}
	}
	if err := r.bot.MarkHandledRead(ctx); err != nil {
		if r.stepFailed(ctx, "mark read", err) {
			return
		}
	}
	if r.health.getSession() == sessionOK {
		r.maybePersistCookies(ctx)
	}
}

func (r *runner) syncTick(ctx context.Context) {
	r.checked = nil
	if r.health.getSession() != sessionOK {
		return
	}
	if err := r.bot.SyncComments(ctx); err != nil {
		r.stepFailed(ctx, "comment sync", err)
	}
}

// stepFailed logs a step error. For an auth error it confirms the session and
// reports whether polling must stop (session confirmed invalid). It never logs in.
func (r *runner) stepFailed(ctx context.Context, step string, err error) (stop bool) {
	if ctx.Err() != nil {
		return true
	}
	if !errors.Is(err, ErrAuth) {
		r.log.Error("step failed", "step", step, "err", err)
		return false
	}
	r.log.Warn("pr0gramm auth error, checking session", "step", step, "err", err)
	first := r.checked == nil
	valid, cerr := r.checkSession(ctx)
	switch {
	case cerr != nil:
		r.log.Warn("session check failed, continuing", "err", cerr)
		return false
	case valid:
		return false
	case !first: // already flipped to invalid earlier in this tick
		return true
	default:
		r.setInvalid()
		return true
	}
}

// recheckSession re-checks a session that is invalid or unknown once its
// re-check time has come. New cookies written by bot-login are re-imported
// first. A session found invalid stays invalid; there is no automatic login.
func (r *runner) recheckSession(ctx context.Context) {
	if r.clock.Now().Before(r.nextCheck) {
		return
	}
	r.importCookies(ctx)
	valid, err := r.checkSession(ctx)
	prev := r.health.getSession()
	switch {
	case err != nil && prev == sessionUnknown:
		r.setUnknown(err)
	case err != nil:
		r.nextCheck = r.clock.Now().Add(r.recheck)
		r.log.Warn("session re-check failed, still invalid", "err", err)
	case valid:
		r.setOK()
	case r.startupUnknown && !r.loginUsed:
		// Startup never got a definitive answer; this is it, so the one
		// automatic login is still available. A session that was ok and died
		// mid-loop never gets here.
		r.loginOnce(ctx)
	default:
		r.setInvalid()
	}
}

// LoginInteractive logs in with a captcha the operator solves, and persists
// the session cookies so a running bot picks them up.
func LoginInteractive(ctx context.Context, cfg config.Config, in io.Reader, out io.Writer) error {
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return loginInteractive(ctx, cfg, pr0gramm.New(nil, ""), NewStore(db.New(pool)), in, out)
}

func loginInteractive(ctx context.Context, cfg config.Config, sess session, store Store, in io.Reader, out io.Writer) error {
	c, err := sess.Captcha(ctx)
	if err != nil {
		return fmt.Errorf("bot: captcha: %w", err)
	}
	img := c.ImageURL
	if strings.HasPrefix(img, "data:") {
		path, err := saveDataURI(img)
		if err != nil {
			return fmt.Errorf("bot: save captcha image: %w", err)
		}
		fmt.Fprintf(out, "Captcha image saved to: %s\nCaptcha text: ", path)
	} else {
		fmt.Fprintf(out, "Captcha image: %s\nCaptcha text: ", img)
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || strings.TrimSpace(line) == "") {
		return fmt.Errorf("bot: read captcha: %w", err)
	}
	if err := sess.Login(ctx, cfg.Pr0User, cfg.Pr0Password, c.Token, strings.TrimSpace(line)); err != nil {
		return fmt.Errorf("bot: login: %w", err)
	}
	data, err := sess.ExportCookies()
	if err != nil {
		return fmt.Errorf("bot: export cookies: %w", err)
	}
	if err := store.SetState(ctx, db.SetStateParams{Key: sessionCookiesKey, Value: string(data)}); err != nil {
		return fmt.Errorf("bot: save cookies: %w", err)
	}
	fmt.Fprintln(out, "ok")
	return nil
}

// saveDataURI decodes a data: URI into a temp file and returns its path.
func saveDataURI(uri string) (string, error) {
	meta, payload, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
	if !ok {
		return "", errors.New("malformed data URI")
	}
	mimeType, _, _ := strings.Cut(meta, ";")
	var data []byte
	if strings.HasSuffix(meta, ";base64") {
		var err error
		if data, err = base64.StdEncoding.DecodeString(payload); err != nil {
			return "", err
		}
	} else {
		p, err := url.PathUnescape(payload)
		if err != nil {
			return "", err
		}
		data = []byte(p)
	}
	ext := ".img"
	switch mimeType {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/svg+xml":
		ext = ".svg"
	}
	f, err := os.CreateTemp("", "pr0sauce-captcha-*"+ext)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return f.Name(), nil
}
