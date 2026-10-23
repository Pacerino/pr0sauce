# pr0sauce Monorepo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the four pr0sauce repos with one monorepo. It contains a Go bot and JSON API (one module, two processes), a Next.js site, and a Python Shazam sidecar. Pr0gramm users see the same behavior, the stack recovers from failures without manual help, and it ships with a finished website.

**Architecture:** One Go binary `pr0sauce` has the subcommands `migrate`, `import-legacy`, `bot`, `bot-login` and `api`. It is built into one image and runs as separate containers. Postgres is accessed through pgx/sqlc, with goose migrations embedded in the binary. The Next.js server components call the internal Go API. The Python sidecar downloads a video URL and runs shazamio on it. It is the only image with ffmpeg in it.

**Tech Stack:** Go 1.27, pgx/v5, sqlc, goose/v3, testcontainers-go, log/slog, net/http, Python 3.13 with FastAPI and shazamio (current release, pinned), pytest, Next.js 16.4.x with the App Router, Tailwind v4 and vitest, Postgres 18, Docker Compose, GitHub Actions, GHCR.

**Spec:** `docs/superpowers/specs/2026-10-23-pr0sauce-monorepo-design.md`

## Global Constraints

- **Git identity:** every commit is authored and committed as `Pacerino <public@pa0x43.me>` — never with the owner's work identity (work email or real name), in an author, committer, trailer, or file. Never add `Co-Authored-By` lines. Before the first commit in any new clone or worktree, run `git var GIT_AUTHOR_IDENT` and check it starts with `Pacerino <public@pa0x43.me>`.
- Go module path is `github.com/Pacerino/pr0sauce`, with `go 1.27` in go.mod.
- Recognition uses **Shazam only**. No ACRCloud code, library, or env var anywhere.
- Bot replies use the **German texts verbatim** as listed in Task 8. Song links always have the form `https://pr0sauce.info/<itemId>`, and that URL must keep resolving on the site.
- Times shown to users use `Europe/Berlin`. The comment timestamp format is Go layout `02.01.2006 um 15:04`.
- **Production images are pinned to exact tags.** Never use `:latest` or `:main`. Images: `ghcr.io/pacerino/pr0sauce-go`, `ghcr.io/pacerino/pr0sauce-web`, `ghcr.io/pacerino/pr0sauce-shazam`, tagged `sha-<7 char sha>`.
- Logging uses `log/slog` with JSON output and no logrus. Every non-2xx response from pr0gramm is logged at ERROR with path and status.
- The API is internal only. The web container is the only one attached to the external `cloudflaretunnel` network.
- Secrets live only in the host `.env`. The repo ships `.env.example` with empty values.
- **Production writes and pr0gramm posts need the owner's OK in chat before they run.** That covers anything on $PROD_HOST beyond reads and anything that posts, comments, tags, PMs, or marks read on pr0gramm. Each such step below is marked **[OWNER OK]**.

## Review Focus

1. **A pr0gramm call fails halfway through a request** (Cloudflare HTML, 5xx, `commentTooSoon`). The request stays retryable, is never marked read on pr0gramm before it's answered, and is never answered twice. Covered by the Task 8 test `TestProcess_PostFailsStaysPending`.
2. **A crash between posting the reply and recording it.** After a restart, a request left in `replying` is marked `failed` and is **not** posted again (at-most-once). Covered by the Task 8 test `TestRecoverReplying`.
3. **Two mentions of the same post.** The first gets the comment and tags. The second gets the PM copy and no second comment. Covered by the Task 8 test `TestProcess_SecondMentionGetsPM`.
4. **Search input with `%`, `_`, quotes, or Cyrillic** is matched literally and never errors. Covered by the Task 11 test `TestSearch_LiteralSpecialChars`.
5. **Odd item paths** (`/0123`, `/99999999999999999999`, `/-5`, `/favicon.ico`, `/abc`) return a 404 page and never a 500 or an API call with `NaN`. Covered by the Task 12 test `ids.test.ts` (`parseItemId`) and the Task 11 test `TestGetSong_BadIDs`.

---

## Phase 0: Find out what is broken today

### Task 0: Investigate the silence since 2026-05-05 and verify the inbox API (read-only)

**Files:**
- Create: `docs/phase0-findings.md`. Commit it in Task 1. Until then, write it in `~/Projects/pr0sauce/docs/`.

**Interfaces:**
- Produces findings that later tasks rely on:
  - (a) whether `GET /api/inbox/comments?markAsRead=false` leaves messages unread
  - (b) which cheap authenticated endpoint tells a valid session from an invalid one (candidate: `GET /api/user/identifier`)
  - (c) the `kind` values seen in inbox comment messages
  - (d) whether `https://vid.pr0gramm.com/<thumb-without-ext>.mp4` still resolves for a recent video post
  - (e) the cause of the silence since May, if one is found

All steps are read-only. No `[OWNER OK]` is needed. Do not post, comment, or mark anything read.

- [ ] **Step 1: Look at the bot's own history.** On the host, run `docker logs pr0sauce-bot-1 2>&1 | grep -vE "check Pr0gramm comments" | grep -E "level=(error|warning)|Comment written|Private message" | tail -100`. Also run `select date_trunc('week', created_at)::date, count(*) from pr0music.items where created_at > '2025-11-01' group by 1 order by 1`. Write down when the "Comment written" lines stop and which errors show up around that time.
- [ ] **Step 2: Check whether people still tag the bot.** Run `GET https://pr0gramm.com/api/profile/comments?name=Sauce&flags=31` from a logged-out curl. Note the date of the newest bot comment. A bot that stopped commenting while mentions kept coming means a bot failure. No mentions at all means usage dropped.
- [ ] **Step 3: Verify `markAsRead=false`, the session check, the kinds, and the video URL.** Use a throwaway Go or curl script that logs in with the bot credentials from `/opt/pr0sauce/.env`. **Stop the old bot first, and only with [OWNER OK].** Its 5-second poll marks everything read and would hide the result. Start it again afterwards. Then:
  - call `/api/inbox/comments?markAsRead=false` twice and confirm `read` stays 0 on the second call, using a mention from the owner's test account
  - call `/api/user/identifier` with and without cookies
  - record the `kind` values
  - send a `HEAD` request to the video URL of the test post
- [ ] **Step 4: Write `docs/phase0-findings.md`** with answers (a)–(e) and the exact commands used. If (a) is false, stop and raise it with the owner before Task 8. The no-loss design depends on it.

---

## Phase 1: Monorepo and data layer

### Task 1: Bootstrap the monorepo

**Files:**
- Move: `~/Projects/pr0sauce/{pr0music,pr0sauce_graphql,pr0sauce_frontend,pr0sauce_shazam}` → `~/Projects/pr0sauce/legacy/`
- Create: `.gitignore` (`/legacy/`, `/.superpowers/`, `node_modules/`, `.next/`, `.env`, `__pycache__/`, `.venv/`), `go.mod`, `cmd/pr0sauce/main.go`, `Makefile`, `README.md`, `.env.example`
- Commit: `docs/superpowers/specs/…`, `docs/superpowers/plans/…`, `docs/phase0-findings.md`

**Interfaces:**
- Produces: `cmd/pr0sauce/main.go` dispatches `os.Args[1]` to `run<Subcommand>(ctx, cfg) error` for `migrate | import-legacy | bot | bot-login | api`. An unknown subcommand prints usage and exits 2. A `signal.NotifyContext` on SIGINT/SIGTERM is passed to every subcommand.
- Makefile targets: `test` (go + python + web), `generate` (sqlc), `lint`.

- [ ] **Step 1:** Run `git init -b main` in `~/Projects/pr0sauce`. Run `git var GIT_AUTHOR_IDENT` and confirm the output starts with `Pacerino <public@pa0x43.me>`.
- [ ] **Step 2:** Write the skeleton, with stubbed subcommands that return `errors.New("not implemented")`. Run `go build ./... && go run ./cmd/pr0sauce nope; echo $?`. Expected output: usage, then `2`.
- [ ] **Step 3: Commit** with `git commit -m "chore: bootstrap pr0sauce monorepo with design, plan and phase-0 findings"`.
- [ ] **Step 4: [OWNER OK] Create the GitHub repo and push.** Run `gh repo create Pacerino/pr0sauce --public --source . --push`, after confirming `gh auth status` shows **Pacerino** as active.

### Task 2: Config

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Load(getenv func(string) string) (Config, error)`
- `Config` fields:
  - `DatabaseURL string` (env `DATABASE_URL`, required)
  - `LegacyDatabaseURL string` (`LEGACY_DATABASE_URL`, needed only for import-legacy)
  - `Pr0User`, `Pr0Password string` (`PR0_USER`, `PR0_PASSWORD`, required for bot)
  - `BannedUsers []string` (`BANNED_USERS`, comma-separated, trimmed, empty entries dropped, compared case-insensitively)
  - `ShazamURL string` (`SHAZAM_URL`, default `http://shazam:8000`)
  - `PollInterval time.Duration` (`POLL_INTERVAL`, default `5s`)
  - `CommentSyncInterval time.Duration` (`COMMENT_SYNC_INTERVAL`, default `10m`)
  - `MaxAttempts int` (`MAX_ATTEMPTS`, default `5`)
  - `BotHealthAddr string` (default `:8081`)
  - `APIAddr string` (default `:8080`)
  - `PublicBaseURL string` (`PUBLIC_BASE_URL`, default `https://pr0sauce.info`)
- Validation per subcommand: `cfg.RequireBot() error` and `cfg.RequireLegacy() error` list every missing variable in one error.

- [ ] **Step 1: Write failing tests.**
  - `TestLoad_Defaults`: only `DATABASE_URL` is set. Assert the defaults above.
  - `TestLoad_BannedUsers`: `" natr0n, ,Foo "` → `[]string{"natr0n","Foo"}`.
  - `TestRequireBot_ListsAllMissing`: the error contains both `PR0_USER` and `PR0_PASSWORD`.
- [ ] **Step 2:** Run `go test ./internal/config/` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/config/` and expect PASS.
- [ ] **Step 5:** Commit with `feat(config): env config with per-command validation`.

### Task 3: Schema, migrations, sqlc and test DB

**Files:**
- Create: `migrations/00001_init.sql`, `migrations/embed.go` (`package migrations; //go:embed *.sql; var FS embed.FS`)
- Create: `internal/db/db.go`, `internal/db/queries/*.sql`, `sqlc.yaml`, and the generated `internal/db/*.go`
- Create: `internal/testdb/testdb.go`, `internal/db/db_test.go`
- Modify: `cmd/pr0sauce/main.go`, so `migrate` calls `db.Migrate`

**Schema** (`00001_init.sql`, goose up/down):

```sql
create extension if not exists pg_trgm;
create table songs (
  item_id       bigint primary key,
  title         text,              -- NULL = looked up, nothing found
  artist        text,
  album         text,
  provider      text not null,     -- 'shazam' | 'acrcloud' (legacy) | 'audd' (legacy) | '' (legacy miss)
  spotify_url   text, spotify_id text,
  youtube_url   text, youtube_id text,
  cover_url     text,
  recognized_at timestamptz not null
);
create index songs_recognized_at on songs (recognized_at desc);
create index songs_search_trgm on songs using gin ((coalesce(title,'') || ' ' || coalesce(artist,'')) gin_trgm_ops);
create table requests (
  id             bigserial primary key,
  message_id     bigint not null unique,
  item_id        bigint not null,
  requester      text not null,
  mentioned_at   timestamptz not null,
  status         text not null check (status in ('pending','replying','done','failed','skipped')),
  reply_kind     text check (reply_kind in ('comment','pm')),
  attempts       int not null default 0,
  next_attempt_at timestamptz not null default now(),
  last_error     text,
  marked_read    boolean not null default false,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now()
);
create index requests_due on requests (next_attempt_at) where status = 'pending';
create table bot_comments (
  comment_id bigint primary key,
  item_id    bigint not null,
  content    text not null,
  up int not null, down int not null,
  thumb      text,
  created_at timestamptz not null
);
create index bot_comments_item on bot_comments (item_id);
create table bot_state (key text primary key, value text not null);   -- 'live_since', 'session_cookies'
```

**Interfaces:**
- Produces:
  - `db.Open(ctx, url string) (*pgxpool.Pool, error)`, which pings before returning
  - `db.Migrate(ctx, url string) error`, which runs goose up on `migrations.FS` through `pgx/v5/stdlib`
  - `db.New(pool) *db.Queries`, the sqlc-generated query set
  - `testdb.New(t *testing.T) *pgxpool.Pool`, which starts a `postgres:18` testcontainer once per package, migrates it, and truncates all tables per test
- sqlc queries needed later. Names are fixed because later tasks use them.
  - songs: `GetSong(item_id)`, `UpsertSong(...)`, `ListRecentSongs(before_recognized_at, before_item_id, limit)` (keyset), `SearchSongs(q, limit, offset)`
  - stats: `CountSongs`, `CountHits`, `CountSongsSince(ts)`, `ProviderShare` (hits grouped by provider), `SongsPerWeek(since)`, `TopSongs(limit)` (hits grouped by `lower(artist), lower(title)`, ordered by count), `TopRequesters(limit)` (from `requests` where status=`done`)
  - requests: `InsertRequestIgnore(...)` (`on conflict (message_id) do nothing`, returns inserted bool), `DueRequests(now, limit)`, `SetRequestStatus(id, status, reply_kind, last_error)`, `BumpAttempt(id, next_attempt_at, last_error)`, `MarkReplying(id)`, `FailStaleReplying()` (returns rows), `UnreadHandledRequests()`, `SetMarkedRead(id)`
  - bot_comments: `UpsertBotComment(...)`, `LatestBotCommentTime()`, `GetBotCommentForItem(item_id)` (newest)
  - bot_state: `GetState(key)`, `SetState(key, value)`

- [ ] **Step 1: Write the failing test** `TestMigrate_UpAndDown` in `internal/db/db_test.go`. Migrate up, assert the tables `songs`, `requests`, `bot_comments`, `bot_state` exist, run goose down to 0, assert they're gone, then up again.
- [ ] **Step 2:** Run `go test ./internal/db/` and expect FAIL.
- [ ] **Step 3:** Write the migration, `db.go`, `testdb`, and the query files. Run `sqlc generate`.
- [ ] **Step 4: Write query tests.**
  - `TestInsertRequestIgnore_Idempotent`: the same `message_id` twice → the second call returns false, and the row count is 1.
  - `TestListRecentSongs_Keyset`: 5 songs → 2 pages of 2 plus 1, with no overlap.
  - `TestTopSongs_GroupsCaseInsensitive`: "Sandstorm/Darude" ×2 plus "sandstorm/darude" → count 3.
- [ ] **Step 5:** Run `go test ./internal/db/` and expect PASS.
- [ ] **Step 6:** Wire up `pr0sauce migrate`, then run `DATABASE_URL=… go run ./cmd/pr0sauce migrate` against a local `docker run postgres:18`. Expected: goose prints `OK 00001_init.sql`.
- [ ] **Step 7:** Commit with `feat(db): schema, goose migrations, sqlc queries, testcontainer helper`.

### Task 4: Legacy data import

**Files:**
- Create: `internal/legacy/import.go`, `internal/legacy/import_test.go`
- Modify: `cmd/pr0sauce/main.go`, so `import-legacy` calls `legacy.Import`

**Interfaces:**
- Consumes: `db.Queries.UpsertSong` and `UpsertBotComment` from Task 3
- Produces: `legacy.Import(ctx, src, dst *pgxpool.Pool, log *slog.Logger) (Report, error)`, with `Report{Songs, Hits, DuplicatesDropped, Comments int}`. It refuses to run if `songs` is not empty, unless `force` is set. The import is idempotent through upserts, but the guard catches mistakes.

**Mapping** (the source tables are in schema `pr0music`):
- `items` → `songs`:
  - `''` becomes NULL for title, artist, album, and the four link fields
  - `provider` is kept as-is
  - `recognized_at = created_at`
  - `cover_url = NULL`
  - legacy `url` and `acr_id` are dropped (the site derives its URL from the item id)
- **Dedupe per `item_id`:** prefer a row with a non-empty title, then the newest `created_at`. Log each dropped row's `id` at INFO.
- `comments` → `bot_comments`: `created` → `created_at`, and `thumb` is kept.
- Rows with `deleted_at` set are skipped. There were 0 on 2026-10-23.

- [ ] **Step 1: Write the failing test** `TestImport_DedupeAndMapping`. The source is a testcontainer DB with the legacy DDL (legacy DDL columns, as seen on prod 2026-10-23: `items` has id, created_at, updated_at, deleted_at, item_id, title, album, artist, url, acr_id, spotify_url, spotify_id, youtube_url, youtube_id, provider; `comments` has id, created_at, updated_at, deleted_at, comment_id, up, down, content, created, item_id, thumb), all in schema `pr0music`. Fixture:
  - item 1: two rows, an older one with title "A" and a newer one with empty title → keep title "A"
  - item 2: two titled rows → keep the newer one
  - item 3: one miss → `title IS NULL`
  - one comment

  Assert `Report{Songs:3, Hits:2, DuplicatesDropped:2, Comments:1}` and the field values.
- [ ] **Step 2:** Run `go test ./internal/legacy/` and expect FAIL.
- [ ] **Step 3:** Implement. Use one `select … order by item_id, (title<>'') desc, created_at desc` and keep the first row per item_id.
- [ ] **Step 4:** Run `go test ./internal/legacy/` and expect PASS.
- [ ] **Step 5: Dry run against real data (read-only on prod).** Take `pg_dump -Fc` on the host, `scp` it to the Mac, restore it into a local `postgres:13`, then run `import-legacy` into a local `postgres:18`. Expected: `Songs=57503, DuplicatesDropped=55, Comments=55228`. Record the output in `docs/phase0-findings.md`.
- [ ] **Step 6:** Commit with `feat(legacy): one-time import from pr0music schema with dedupe`.

---

## Phase 2: Integrations

### Task 5: pr0gramm client

**Files:**
- Create: `internal/pr0gramm/client.go`, `internal/pr0gramm/types.go`, `internal/pr0gramm/session.go`, `internal/pr0gramm/client_test.go`

**Interfaces:**
- Produces:
  - `pr0gramm.New(httpClient *http.Client, baseURL string) *Client`. baseURL defaults to `https://pr0gramm.com/api` and can be overridden in tests.
  - `(*Client).Login(ctx, name, password, token, captcha string) error`. Returns `ErrBanned` or `ErrLoginFailed` wrapping the server's error string.
  - `(*Client).Captcha(ctx) (Captcha{Token, ImageURL string}, error)`, from `GET /user/captcha`
  - `(*Client).SessionValid(ctx) (bool, error)`, using the endpoint chosen in Phase 0 (b)
  - `(*Client).InboxComments(ctx) ([]InboxComment, error)`, as `GET /inbox/comments?markAsRead=false`
  - `(*Client).MarkRead(ctx, messageID int64) error`, as `GET /inbox/read?messageType=comment&messageId=…`
  - `(*Client).PostComment(ctx, itemID, parentID int64, text string) error`, as `POST /comments/post` (`itemId`, `parentId`, `comment`, `_nonce`)
  - `(*Client).AddTags(ctx, itemID int64, tags []string) error`, as `POST /tags/add` (`itemId`, `tags` comma-joined, `_nonce`)
  - `(*Client).SendMessage(ctx, recipient, text string) error`, as `POST /inbox/post` (`recipientName`, `comment`, `_nonce`)
  - `(*Client).UserComments(ctx, name string, after time.Time) (UserComments{Comments []UserComment; HasNewer bool}, error)`, as `GET /profile/comments?name=&flags=31&after=<unix>`
  - `(*Client).ExportCookies() ([]byte, error)` and `ImportCookies([]byte) error`, using JSON of `[]http.Cookie` for host pr0gramm.com
  - `InboxComment{ID, ItemID int64; Thumb, Name, Message, Kind string; Created time.Time; Read bool}`
  - `UserComment{ID, ItemID int64; Up, Down int; Content, Thumb string; Created time.Time}`
  - `APIError{Path string; Status int; Body string}`, returned for every non-2xx and for a 2xx with `{"success":false,"error":…}`. `IsRetryable(err) bool` is true for network errors, 5xx, 429, and `commentTooSoon`.
- The nonce is the first 16 characters of the `id` field of the URL-decoded JSON `me` cookie, which is the same as the legacy `GetNonce`. A missing `me` cookie on a POST returns `ErrNoSession`.

- [ ] **Step 1: Write failing tests** against an `httptest.Server`:
  - `TestInboxComments_SendsMarkAsReadFalse` asserts the query param.
  - `TestPostComment_SendsNonceFromMeCookie`: set the `me` cookie to `%7B%22id%22%3A%220123456789abcdefXYZ%22%7D` and assert `_nonce=0123456789abcdef`.
  - `TestAPIError_On403HTML`: an HTML body with status 403 → `*APIError` with Status 403, and `IsRetryable` is false.
  - `TestIsRetryable_CommentTooSoon`: `{"success":false,"error":"commentTooSoon"}` → true.
  - `TestCookies_RoundTrip`.
  - `TestUserComments_ParsesUnixTimes`.
- [ ] **Step 2:** Run `go test ./internal/pr0gramm/` and expect FAIL.
- [ ] **Step 3:** Implement. Set the User-Agent to `pr0sauce-bot (+https://pr0sauce.info)` and use a 15-second HTTP timeout.
- [ ] **Step 4:** Run `go test ./internal/pr0gramm/` and expect PASS.
- [ ] **Step 5:** Commit with `feat(pr0gramm): API client with markAsRead=false, typed errors, cookie persistence`.

### Task 6: Shazam sidecar

**Files:**
- Create: `shazam/app/main.py`, `shazam/tests/test_main.py`, `shazam/requirements.txt` (pinned: fastapi, uvicorn, shazamio, httpx), `shazam/requirements-dev.txt` (pytest, respx), `shazam/Dockerfile` (`python:3.13-slim` plus `ffmpeg`, non-root, `uvicorn app.main:app --host 0.0.0.0 --port 8000`)

**Interfaces:**
- Produces HTTP endpoints:
  - `GET /healthz` → `200 {"ok":true}`
  - `POST /recognize` with JSON `{"url": "<https video url>"}`:
    - `200` with shazamio's raw result dict
    - `422` if the URL's host is not `vid.pr0gramm.com` (internal service, but don't build an open fetcher)
    - `404 {"error":"not_found"}` if the download returns 404
    - `502 {"error":"<msg>"}` on any other download or recognition failure
  - Downloads are capped at 200 MB and 60 s. The video is written to a temp file, which is always deleted.

- [ ] **Step 1: Write failing tests** using respx to mock the download and monkeypatching `Shazam.recognize`:
  - `test_recognize_returns_raw`
  - `test_rejects_foreign_host` (422)
  - `test_download_404` (404)
  - `test_temp_file_removed_on_error`
  - `test_healthz`
- [ ] **Step 2:** Run `cd shazam && python -m pytest -q` and expect FAIL.
- [ ] **Step 3:** Implement. Call shazamio's current file-path recognize API (`Shazam().recognize(path)`) and check the pinned version's README for the exact name.
- [ ] **Step 4:** Run `python -m pytest -q` and expect PASS.
- [ ] **Step 5:** Run `docker build -t pr0sauce-shazam:dev shazam/`, then call `/recognize` with a real `vid.pr0gramm.com` URL from a known hit (item 6796145's video, via Phase 0). Expected: the track title is "Kumite".
- [ ] **Step 6:** Commit with `feat(shazam): URL-based recognize sidecar with health check`.

### Task 7: Recognition and links (Go)

**Files:**
- Create: `internal/recognize/shazam.go`, `internal/recognize/shazam_test.go`, `internal/recognize/testdata/kumite.json` (a real response saved in Task 6 Step 5)
- Create: `internal/odesli/odesli.go`, `internal/odesli/odesli_test.go`

**Interfaces:**
- Produces:
  - `recognize.Client{BaseURL string; HTTP *http.Client}` with `Recognize(ctx, videoURL string) (Result, error)`
  - `recognize.Result{Found bool; Title, Artist, Album, CoverURL, AppleMusicURL string}`
  - `recognize.ErrNotAVideo`, returned when the sidecar answers 404
  - Mapping, from the legacy `shazam.go`:
    - Title = `track.title`
    - Artist = `track.subtitle`
    - Album = the `sections[type=SONG].metadata[title=Album].text`, or `""`
    - CoverURL = `track.images.coverarthq`, falling back to `coverart`
    - AppleMusicURL = the `hub.options[caption="OPEN IN"].actions[type=applemusicopen].uri`, or `""`
    - Found = `track.title != ""`
  - `VideoURL(thumb string) string` → `https://vid.pr0gramm.com/` + the thumb without its extension + `.mp4`. Adjust if Phase 0 (d) found otherwise.
  - `odesli.Client` with a **single shared** `rate.Limiter` (10 per minute, burst 1) and `Links(ctx, sourceURL string) (Links, error)`. Uses `https://api.song.link/v1-alpha.1/links?songIfSingle=true&url=<escaped>`.
  - `odesli.Links{SpotifyURL, SpotifyID, YouTubeURL, YouTubeID string}`. The ID is the part after `::` in `entityUniqueId`. A missing platform gives empty fields, not an error.

- [ ] **Step 1: Write failing tests.**
  - `TestRecognize_ParsesKumiteFixture`: Title "Kumite", Artist "Paul Hertzog", and non-empty CoverURL and AppleMusicURL.
  - `TestRecognize_NoMatch`: `{}` → Found false, nil error.
  - `TestRecognize_404IsErrNotAVideo`.
  - `TestVideoURL`: `"2026/10/23/abc.jpg"` → `"https://vid.pr0gramm.com/2026/10/23/abc.mp4"`.
  - `TestLinks_ParsesIDs`.
  - `TestLinks_MissingYouTube`.
- [ ] **Step 2:** Run `go test ./internal/recognize/ ./internal/odesli/` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same tests and expect PASS.
- [ ] **Step 5:** Commit with `feat(recognize): shazam sidecar client, odesli links with shared rate limit`.

---

## Phase 3: Bot

### Task 8: Request state machine

**Files:**
- Create: `internal/bot/bot.go`, `internal/bot/messages.go`, `internal/bot/ports.go`, `internal/bot/bot_test.go`, `internal/bot/fakes_test.go`

**Interfaces:**
- Consumes:
  - the Task 3 queries, through the `Store` interface below, implemented by a thin adapter over `*db.Queries`
  - the Task 5 client, through `Pr0`
  - the Task 7 clients, through `Recognizer` and `Linker`
- Produces in `ports.go`:
  - `Pr0` interface: `InboxComments`, `MarkRead`, `PostComment`, `AddTags`, `SendMessage`, `UserComments` (signatures as in Task 5)
  - `Recognizer` interface: `Recognize(ctx, videoURL) (recognize.Result, error)`
  - `Linker` interface: `Links(ctx, url) (odesli.Links, error)`
  - `Store` interface: the request, song, and state queries from Task 3
  - `Clock` interface: `Now() time.Time`
- `bot.New(deps Deps, cfg config.Config, log *slog.Logger) *Bot`
- `(*Bot).Ingest(ctx) error` does one poll:
  1. On the first ever call, sets `bot_state.live_since = now` if it's missing.
  2. Keeps inbox comments that are **unread** and contain `@sauce` (case-insensitive).
  3. Calls `InsertRequestIgnore` for each one. If the requester is banned, the status is `skipped`. If `created < live_since`, the status is `skipped`. Otherwise it's `pending`.
- `(*Bot).ProcessDue(ctx) error` handles `DueRequests(now, 10)` **sequentially**:
  - If a song exists for the item: `SendMessage` with the PM text, then status `done`, `reply_kind = pm`.
  - Otherwise: `Recognize(VideoURL(thumb))`.
    - On `ErrNotAVideo`: status `skipped` and no reply.
    - On a hit: `Links(AppleMusicURL)` if that's non-empty, where a links error is logged and treated as empty links. Then `UpsertSong` with `provider = 'shazam'`, title/artist/album (empty album → NULL), `cover_url = CoverURL` and the Spotify/YouTube fields from `Links`, `MarkReplying`, `PostComment(item, messageID, foundText)`, `AddTags` (an error is logged and the request still counts as done), and status `done` with `reply_kind = comment`.
    - On a miss: `UpsertSong` with a NULL title, `MarkReplying`, `PostComment(missText)`, and status `done`. No tags.
  - A retryable error **before `MarkReplying`** calls `BumpAttempt` with backoff `1m × 2^(attempts-1)`, capped at 1h. Once attempts reach `MaxAttempts`, the status becomes `failed`.
  - A **non-retryable error, or any error after `MarkReplying`**, sets status `failed` with `last_error` and does **not** retry. A failed reply post must never repeat.
- `(*Bot).MarkHandledRead(ctx) error`: for every request with status in (`done`, `skipped`, `failed`) and `marked_read = false`, call `MarkRead`, then `SetMarkedRead`. Errors are logged and retried on the next tick.
- `(*Bot).RecoverReplying(ctx) error`: called once at startup. Runs `FailStaleReplying` and logs each request at ERROR (`"reply state unknown after restart, not retrying"`).
- `messages.go`, with the **exact copy** below. Format with `fmt.Sprintf`. Placeholders are listed in order.

```go
// PM when the item was already looked up and has a title: name, itemID, title, album, artist, link
const pmKnownHit = "Hallo %s,\n\nDu hast bei https://pr0gramm.com/new/%d nach der Musik gefragt.\nJemand hat bereits danach gefragt, daher erhälst du hier nur eine Kopie.\n\nTitel: %s\nAlbum: %s\nArtist: %s\n\nHier ist ein Link: %s"
// PM when the item was already looked up without result: name, itemID
const pmKnownMiss = "Hallo %s,\n\nDu hast bei https://pr0gramm.com/new/%d nach der Musik gefragt.\nLeider wurden keine Informationen gefunden."
// Comment on a hit: title, artist, album, link, checkedAt
const commentHit = "Es wurden folgende Informationen dazu gefunden:\n%s - %s\nAus dem Album: %s\n\nHier ist ein Link: %s\nZeitpunkt der Überprüfung %s"
// Comment on a miss: checkedAt
const commentMiss = "Es wurden keine Informationen zu dem Lied gefunden\n\nZeitpunkt der Überprüfung %s"
```

  - `link` = `cfg.PublicBaseURL + "/" + itemID`
  - `checkedAt` = now in Europe/Berlin, formatted `02.01.2006 um 15:04`
  - tags = `[title, artist, artist + " - " + title]`

- [ ] **Step 1: Write failing tests** with in-memory fakes. Use a real `testdb` for `Store` so the SQL is tested too.
  - `TestIngest_FirstRunSkipsOldMentions`: `live_since` is unset, one mention created 1 minute before now, and the clock fixed → the request is `skipped`. Assert no `PostComment` or `SendMessage` happens, even after `ProcessDue`.
  - `TestIngest_IgnoresReadAndNonMentions`.
  - `TestIngest_BannedUserSkipped`.
  - `TestIngest_Idempotent`: the same inbox twice → 1 row.
  - `TestProcess_HitPostsCommentAndTags`: assert the exact comment text with the fixed clock `2026-10-23T13:15:00Z` → `"…Zeitpunkt der Überprüfung 23.10.2026 um 15:15"`, the tags `["Kumite","Paul Hertzog","Paul Hertzog - Kumite"]`, and a stored song.
  - `TestProcess_MissPostsMissComment`.
  - `TestProcess_SecondMentionGetsPM`: two mentions of the same item → one comment and one PM with `pmKnownHit`.
  - `TestProcess_NotAVideoSkipped`.
  - `TestProcess_PostFailsStaysPending`: Recognize fails with 503 → attempts 1, still `pending`, `next_attempt_at = now+1m`, and `MarkRead` is not called.
  - `TestProcess_PostCommentFailsAfterReplyingIsFailedNotRetried`.
  - `TestProcess_GivesUpAfterMaxAttempts`.
  - `TestRecoverReplying`: a row in `replying` → `failed`, and no post.
  - `TestMarkHandledRead_OnlyAfterHandled`.
- [ ] **Step 2:** Run `go test ./internal/bot/` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/bot/` and expect PASS.
- [ ] **Step 5:** Commit with `feat(bot): durable request state machine with first-run skip and at-most-once replies`.

### Task 9: Incremental bot comment sync

**Files:**
- Create: `internal/bot/commentsync.go`, `internal/bot/commentsync_test.go`

**Interfaces:**
- Produces: `(*Bot).SyncComments(ctx) error`.
  - Starts at `after = LatestBotCommentTime() - 1h`. With no rows, it starts at `live_since - 7 days`, because legacy comments are already imported.
  - Pages `UserComments(cfg.Pr0User, after)` while `HasNewer`, and upserts comments whose content contains `"Es wurden"` (legacy filter).
  - Afterwards it re-fetches **once** from `now - 7 days` to refresh votes on recent comments.
  - It never pages back to 2021.

- [ ] **Step 1: Write failing tests.**
  - `TestSyncComments_StartsFromLatest`: the fake records the `after` values. Assert the first call is `latest - 1h`, and that no call ever has `after < live_since - 7d`.
  - `TestSyncComments_FiltersNonBotReplies`.
  - `TestSyncComments_UpdatesVotes`.
- [ ] **Step 2:** Run `go test ./internal/bot/ -run SyncComments` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same tests and expect PASS.
- [ ] **Step 5:** Commit with `feat(bot): incremental comment/vote sync`.

### Task 10: Bot process (session, health, login, loop)

**Files:**
- Create: `internal/bot/run.go`, `internal/bot/health.go`, `internal/bot/run_test.go`
- Modify: `cmd/pr0sauce/main.go` for `bot` and `bot-login`

**Interfaces:**
- Consumes: Tasks 5, 8, and 9
- Produces:
  - `bot.Run(ctx, cfg, log) error`. Startup order:
    1. `db.Open`. On failure, **return the error** and exit non-zero.
    2. `RecoverReplying`.
    3. Load `bot_state.session_cookies` into the client.
    4. If `!SessionValid`, make exactly **one** `Login(user, pass, "", "")` attempt. On success, save the cookies.
    5. If the session is still invalid, set health to `session_invalid` and log at ERROR `"pr0gramm session invalid — run: docker compose run --rm -it bot bot-login"`. **Keep running without polling**, and re-check `SessionValid` every 10 minutes. It never logs in again by itself.
    6. Main loop: every `PollInterval`, run `Ingest`, `ProcessDue`, then `MarkHandledRead`. Every `CommentSyncInterval`, run `SyncComments`.
    7. A 401 or 403 from any call flips the health to `session_invalid` and stops polling.
  - Health server on `cfg.BotHealthAddr`, `GET /healthz`:
    - 200 `{"db":"ok","session":"ok","last_poll":"<rfc3339>"}` only if the DB pings, the session is valid, and the last successful poll was under 2 minutes ago
    - otherwise 503 with the same body
  - `bot.LoginInteractive(ctx, cfg, in io.Reader, out io.Writer) error`: calls `Captcha`, prints the image URL, reads one line of captcha text, calls `Login(user, pass, token, captcha)`, and saves the cookies.

- [ ] **Step 1: Write failing tests.**
  - `TestRun_DBDownReturnsError`.
  - `TestRun_InvalidSessionStaysUpUnhealthy`: a fake pr0gramm returns 403 → `/healthz` is 503 with `"session":"invalid"`, there was exactly 1 login attempt, and there are 0 inbox calls after 3 ticks.
  - `TestRun_HealthyAfterPoll`.
  - `TestLoginInteractive_SavesCookies`.
- [ ] **Step 2:** Run `go test ./internal/bot/ -run 'Run|Login'` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same tests and expect PASS.
- [ ] **Step 5:** Commit with `feat(bot): process wiring, persisted session, health endpoint, interactive login`.

---

## Phase 4: API and website

### Task 11: JSON API

**Files:**
- Create: `internal/api/server.go`, `internal/api/songs.go`, `internal/api/stats.go`, `internal/api/api_test.go`
- Modify: `cmd/pr0sauce/main.go` for `api`

**Interfaces:**
- Produces: `api.New(q *db.Queries, pool *pgxpool.Pool, log *slog.Logger) http.Handler`, using the Go 1.22+ `ServeMux` patterns.
- `GET /api/songs?q=&cursor=&limit=`:
  - `limit` defaults to 20 with a maximum of 50. Values out of range return 400.
  - With no `q`: `ListRecentSongs` over hits only, using an opaque keyset `cursor` (base64 of `recognized_at|item_id`).
  - With a `q` of only 1–19 digits: return `GetSong` for that id, as 0 or 1 results.
  - Otherwise: `SearchSongs`, using `ILIKE '%' || escaped(q) || '%'` on title or artist with `escape '\'`, ordered by `similarity(...)`, then `recognized_at desc`, with offset pagination in the cursor.
  - Response: `{"items":[Song], "nextCursor": "…"|null}`
- `Song` JSON: `{"itemId":int,"title":str,"artist":str,"album":str|null,"provider":str,"spotifyUrl":str|null,"youtubeUrl":str|null,"coverUrl":str|null,"recognizedAt":rfc3339,"comment":{"up":int,"down":int,"commentId":int}|null}`
- `GET /api/songs/{itemId}`:
  - 400 if `itemId` is not `^[1-9][0-9]{0,18}$` or overflows int64
  - 404 if there's no row or the title is NULL
  - otherwise 200 `Song`, with the comment from `GetBotCommentForItem`
- `GET /api/stats` → `{"totalLookups":int,"hits":int,"hitRate":float,"today":int,"providers":[{"provider":str,"count":int}],"perWeek":[{"week":"YYYY-MM-DD","count":int}],"topSongs":[{"title","artist","count"}],"topRequesters":[{"name","count"}],"requestersSince":rfc3339|null}`
  - `today` counts songs since midnight Europe/Berlin
  - `perWeek` covers the last 26 weeks
  - top lists have 10 entries
  - the response is cached in memory for 60 seconds
- `GET /healthz` → 200 if the pool pings, otherwise 503.

- [ ] **Step 1: Write failing tests** with `testdb` and seeded songs.
  - `TestListRecent_PaginatesHitsOnly`.
  - `TestSearch_MatchesTitleAndArtist` ("hertz" finds Kumite).
  - `TestSearch_LiteralSpecialChars`: `q` values `100%`, `a_b`, `O'Brien`, and `Новая` → 200, and `%` matches only a literal %.
  - `TestSearch_NumericQueryIsItemLookup`.
  - `TestGetSong_BadIDs`: `0123` → 400, `99999999999999999999` → 400, `-5` → 400, `abc` → 400, an unknown valid id → 404.
  - `TestGetSong_IncludesComment`.
  - `TestStats_Shape`.
  - `TestLimitBounds`.
- [ ] **Step 2:** Run `go test ./internal/api/` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/api/` and expect PASS.
- [ ] **Step 5:** Commit with `feat(api): songs, search, stats JSON API`.

### Task 12: Web scaffold and API client

**Files:**
- Create: `web/` (Next.js 16.4.x, TypeScript, App Router, Tailwind v4, `output: "standalone"`), `web/lib/api.ts`, `web/lib/ids.ts`, `web/lib/ids.test.ts`, `web/vitest.config.ts`, `web/app/layout.tsx`, `web/app/globals.css`, `web/public/favicon.ico`, `web/app/robots.ts`, `web/Dockerfile` (node 24 LTS alpine, standalone, non-root)

**Interfaces:**
- Produces:
  - `parseItemId(s: string): number | null`, which returns a number only for `^[1-9][0-9]{0,15}$` (safe integer range) and null otherwise
  - `api.songs(params: {q?: string; cursor?: string}): Promise<{items: Song[]; nextCursor: string|null}>`
  - `api.song(id: number): Promise<Song | null>` (null on 404)
  - `api.stats(): Promise<Stats>`
  - The base URL comes from the server-only env `API_URL` (default `http://api:8080`). Fetches use `cache: "no-store"` for songs and `next: { revalidate: 60 }` for stats.
  - `Song` and `Stats` TS types mirror the Task 11 JSON.
- Theme tokens in `globals.css`: background `#161618`, surface `#2a2e31`, text `#f2f5f4`, accent `#ee4d2e`. The layout nav has "pr0sauce" (accent), "Neu", "Statistik", and "GitHub" (→ `https://github.com/Pacerino/pr0sauce`), with `<html lang="de">`.
- Remove Apollo, Sentry, and GraphQL. None of them are in package.json.

- [ ] **Step 1: Write the failing test** `web/lib/ids.test.ts`. `"6796145"` → 6796145. `"0123"`, `"-5"`, `"abc"`, `"favicon.ico"`, `""`, and `"99999999999999999"` → null.
- [ ] **Step 2:** Run `cd web && npx vitest run` and expect FAIL.
- [ ] **Step 3:** Scaffold and implement.
- [ ] **Step 4:** Run `npx vitest run && npm run build` and expect PASS and a successful build.
- [ ] **Step 5:** Commit with `feat(web): Next.js scaffold, typed API client, id parsing`.

### Task 13: Home and song page

**Files:**
- Create: `web/app/page.tsx`, `web/app/[id]/page.tsx`, `web/app/not-found.tsx`, `web/components/SongRow.tsx`, `web/components/SearchBox.tsx`, `web/components/Kpis.tsx`, `web/lib/format.ts`, `web/lib/format.test.ts`

**Behavior** (from the approved mockup "A · Search-first", with German UI):
- `/`: the headline **"Welches Lied läuft da?"**, the subline "Alle Songs, die @Sauce auf pr0gramm erkannt hat", and a GET form `?q=` with the placeholder "Titel, Artist oder Post-ID suchen …".
  - If `q` passes `parseItemId`: `redirect('/' + id)`.
  - KPIs from `api.stats()`: "Anfragen" (totalLookups), "erkannt" (hitRate as a whole-number percent), "heute" (today).
  - The list heading is "Zuletzt erkannt", or "Ergebnisse für „q"" when searching.
  - Each `SongRow` shows the cover (or an accent gradient placeholder), title, artist, `#itemId`, and a relative German time ("vor 3 Min", "vor 1 Std", "gestern", otherwise `dd.MM.yyyy`).
  - A "Mehr laden" link goes to `?cursor=…` (and keeps `q`). It's a plain link with no client JS.
- `/[id]`: if `parseItemId` is null, call `notFound()` before any API call. If `api.song` is null, call `notFound()`.
  - Render the cover, title, "artist · album", pills ("erkannt via Shazam" or provider, plus the date), and Spotify/YouTube buttons (only if the URLs exist).
  - Row "Original-Post auf pr0gramm" links to `https://pr0gramm.com/new/<id>`, plus "Bot-Kommentar +up / −down" when a comment exists.
  - A greyed, non-interactive row "⚑ Falsch erkannt? Korrektur vorschlagen" with the hint "bald".
  - `generateMetadata` sets the title to `"<title> – <artist> | pr0sauce"`.
- `not-found.tsx`: "Nicht gefunden" with a link back to `/`.

- [ ] **Step 1: Write the failing test** `format.test.ts`. With a fixed now of `2026-10-23T13:15:00Z`:
  - 3 minutes ago → "vor 3 Min"
  - 70 minutes ago → "vor 1 Std"
  - yesterday 20:00 Berlin → "gestern"
  - 2026-05-05 → "05.05.2026"
- [ ] **Step 2:** Run `npx vitest run` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `npx vitest run && npm run build` and expect PASS.
- [ ] **Step 5: Manual check.** Start the API on the locally imported DB from Task 4 Step 5 and run `npm run dev`:
  - `/6796145` shows Kumite
  - `/favicon.ico`, `/abc` and `/0123` show the 404 page, with no API request in the API log
  - `/?q=6796145` redirects
  - `/?q=hertzog` lists Kumite
- [ ] **Step 6:** Commit with `feat(web): search-first home, song page, 404`.

### Task 14: Stats page

**Files:**
- Create: `web/app/statistik/page.tsx`, `web/components/WeekBars.tsx`

**Behavior:**
- KPIs: "gesamt", "Trefferquote", and the provider share (e.g. "Shazam 93 %").
- "Anfragen pro Woche" is an inline SVG bar chart of `perWeek` with an accessible `<title>` per bar. No chart library.
- Two columns: "Top Songs" (artist – title, ×count) and "Top Fragesteller", with the note "seit <requestersSince as dd.MM.yyyy>", or "noch keine Daten" when empty.

- [ ] **Step 1:** Implement the page.
- [ ] **Step 2:** Run `npm run build`, then a manual check against the local API: `/statistik` renders with the imported data and an empty top-requesters list shows "noch keine Daten".
- [ ] **Step 3:** Commit with `feat(web): statistics page`.

---

## Phase 5: Ship

### Task 15: Images, compose, CI

**Files:**
- Create: `Dockerfile` (Go: `golang:1.27` builder → `gcr.io/distroless/static-debian12:nonroot`, `CGO_ENABLED=0`, entrypoint `/pr0sauce`)
- Create: `deploy/compose.yml`, `deploy/.env.example`, `.github/workflows/ci.yml`

**Compose** (`name: pr0sauce2`, so volumes and containers don't clash with the old `pr0sauce` project):
- `postgres`: `postgres:18.<current minor>` pinned, volume `pgdata`, healthcheck `pg_isready`
- `migrate`: `pr0sauce-go:${TAG}`, command `migrate`, `depends_on postgres: service_healthy`, `restart: "no"`
- `shazam`: `pr0sauce-shazam:${TAG}`, healthcheck `GET /healthz`
- `bot`: command `bot`, `depends_on` migrate `service_completed_successfully` and shazam `service_healthy`. Healthcheck `GET :8081/healthz` through the binary's own `pr0sauce healthcheck http://localhost:8081/healthz` subcommand, because distroless has no curl. Add that tiny subcommand in this task.
- `api`: command `api`, `depends_on` migrate, healthcheck `:8080/healthz`
- `web`: `pr0sauce-web:${TAG}`, env `API_URL=http://api:8080`, `depends_on api: service_healthy`, networks `internal` + `cloudflaretunnel` (external), Traefik labels with router **`pr0sauce-web`**: `Host(\`pr0sauce.info\`)`, entrypoint `websecure`, `tls=true`, port 3000
- All services except `migrate` get `restart: unless-stopped`. Only `web` joins `cloudflaretunnel`.

**CI** (`ci.yml`), on push and PR:
- jobs `go` (`go test ./...`, where Docker is available for testcontainers on ubuntu-latest), `python` (pytest), `web` (vitest + build)
- on push to `main` only, `images`: build and push the 3 images to GHCR tagged `sha-${GITHUB_SHA::7}`, using `GITHUB_TOKEN` with `packages: write`

- [ ] **Step 1:** Write the files. Run `docker compose -f deploy/compose.yml config` and expect valid output.
- [ ] **Step 2: Local end-to-end.** Build the 3 images locally, restore the Task 4 dump, set `LEGACY_DATABASE_URL` and run `import-legacy`, then `docker compose up`. Expected: all services healthy (`docker compose ps`) **except `bot`, which reports unhealthy `session_invalid` when given dummy credentials**. That proves it doesn't crash-loop. `curl localhost:3000/6796145` shows Kumite.
- [ ] **Step 3:** Commit with `ci: images, production compose, CI pipeline` and push. Check that CI is green and the 3 images exist in GHCR under `sha-<7>`.

### Task 16: Cutover [OWNER OK at every step]

**Files:**
- Create: `docs/runbook-cutover.md`, written first, with the exact commands of Steps 1–8 below and the rollback commands.

- [ ] **Step 1:** Write `docs/runbook-cutover.md`. Commit it, then show the owner and get OK.
- [ ] **Step 2: Backup.** Run `docker exec pr0sauce-postgres-1 pg_dump -U pr0sauce -Fc pr0music > /opt/pr0sauce/backup-pre-v2-$(date +%F-%H%M).dump` and check that the file size is above 0.
- [ ] **Step 3: Deploy files.** Copy `deploy/compose.yml` to `/opt/pr0sauce/v2/compose.yml` and create `/opt/pr0sauce/v2/.env` (TAG, DB password, PR0_USER/PR0_PASSWORD taken from the old `.env`, BANNED_USERS=`natr0n`, LEGACY_DATABASE_URL=postgres://pr0sauce:…@legacy-restore:5432/pr0music). Then `docker compose pull`. Start a temporary `postgres:13` container `legacy-restore` on the `pr0sauce2_internal` network and `pg_restore` the Step 2 dump into it. Remove it after Step 4. The old volume is never touched.
- [ ] **Step 4: Stop the old bot only.** Run `docker stop pr0sauce-bot-1`. From here on, no bot answers. Start the new `postgres` and run `migrate` and `import-legacy`. Expect `Songs=57503 DuplicatesDropped=55 Comments=55228`, ± rows added since Task 4.
- [ ] **Step 5: Switch the website.** Run `docker stop pr0sauce-frontend-1 pr0sauce-graphql-1` and `docker compose up -d shazam api web`. Check that `https://pr0sauce.info/6796145` shows Kumite, that `/favicon.ico` is 404 and not 500, and that `/statistik` renders.
- [ ] **Step 6: Start the bot.** Run `docker compose up -d bot`. If health shows `session_invalid`, run `docker compose run --rm -it bot bot-login` with the owner present for the captcha. Check that `/healthz` reports ok and that the startup log shows the first-run skips.
- [ ] **Step 7: Live test.** The owner posts a mention from their second account under a video post. Check for exactly one reply comment and tags. Then a second mention on the same post produces exactly one PM. Check `select status, reply_kind, marked_read from requests order by id desc limit 2` → `done/comment/t` and `done/pm/t`.
- [ ] **Step 8: Keep the rollback path.** Leave the old containers stopped (not removed) and leave volume `pr0sauce_pgdata` untouched. Rollback is `docker compose -f /opt/pr0sauce/v2/compose.yml down` followed by `cd /opt/pr0sauce && docker compose up -d`.

### Task 17: Retire the old repos [OWNER OK]

- [ ] **Step 1:** After the owner confirms the new stack is good, run `gh repo archive Pacerino/pr0music Pacerino/pr0sauce_graphql Pacerino/pr0sauce_frontend Pacerino/pr0sauce_shazam` (one call each, with `--yes`). Don't delete them.
- [ ] **Step 2:** Add a line to each old README pointing to `Pacerino/pr0sauce` *before* archiving. That's one commit per repo as Pacerino, and the identity check applies.
- [ ] **Step 3: [OWNER OK]** Remove the stopped old containers. The old volume is removed only on a separate, explicit OK.
