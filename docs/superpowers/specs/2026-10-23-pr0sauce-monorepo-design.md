# pr0sauce monorepo refactor — design

Date: 2026-10-23
Status: ready for owner review (builder check done)

## 1. Intent

pr0sauce is a pr0gramm.com user bot (`@Sauce`). When someone mentions it under a video, it recognizes the music and replies. pr0sauce.info shows the result for each post.

Right now the stack is spread over four repos (`pr0music`, `pr0sauce_graphql`, `pr0sauce_frontend`, `pr0sauce_shazam`). The DB models are copy-pasted between them, nothing has migrations, dependencies are old, there are no health checks, and the frontend is a stub. On 2026-10-23 a routine stack restart lost user requests and crashed the bot.

This refactor **rebuilds the stack, not the bot's behavior**. pr0gramm users should see the same bot. The owner gets one repo that is easy to work on, a stack that stays healthy on its own, and a finished website.

## 2. Goals

1. **One monorepo** that replaces the four repos.
2. **Healthy by default.** Every service has a health check, a restart policy, and correct startup ordering. Failures are loud and recover on their own. No silent half-alive states.
3. **No lost requests.** A mention is only treated as handled after the bot has answered it.
4. **A finished website** (see section 6), backed by a plain JSON API.
5. **Existing data kept.** About 57.5k items and 55k bot comments are migrated, not discarded.
6. **Current, pinned dependencies** and reproducible image builds.

## 3. Non-goals

- Changing what the bot says or does on pr0gramm. That covers reply texts, tagging, PM-for-duplicates, and the banned-users list.
- Users flagging wrong recognitions or suggesting edits. This is **future work**: leave room for it (see 5.4 and 6.3), but don't build it.
- Multi-host, Kubernetes, or any new hosting. It stays on the current Docker host.
- Rewriting Shazam fingerprinting in Go.

## 4. Anti-goals (failure even if it "works")

- The bot behaves differently towards pr0gramm users, or posts duplicate or extra comments while it's being migrated.
- Any commit or push uses the owner's company identity. **Commits must be authored as `Pacerino <public@pa0x43.me>` — never with the owner's work identity (work email or real name). `~/Projects/pr0sauce/**` gets the right identity via a global git `includeIf`; verify with `git var GIT_AUTHOR_IDENT` before the first commit.** Add no `Co-Authored-By` trailers.
- The website goes down because the bot crashes or pr0gramm is unreachable.
- Historical data is lost or silently changed.

## 5. Decided shape

### 5.1 Languages and repo (owner decisions)

- **Go** for the bot and API. **Next.js** for the frontend. **Python** sidecar for Shazam, kept because shazamio is maintained and porting it is risky.
- **One monorepo.** Proposed layout *(builder may adjust)*:

```
pr0sauce/
  go.mod                 # one Go module
  cmd/bot/               # pr0gramm bot process
  cmd/api/               # JSON API process
  cmd/migrate/           # runs DB migrations, then exits (or a subcommand)
  internal/...           # db, pr0gramm client, recognition, odesli, config
  migrations/            # versioned SQL
  web/                   # Next.js app
  shazam/                # Python FastAPI + shazamio sidecar
  deploy/compose.yml     # production compose
  .github/workflows/     # one CI pipeline
  docs/
```

### 5.2 Processes (owner decision: one Go module, two processes)

- `bot` and `api` are built from the same Go module and image but run as **separate containers**, so a bot crash never takes the site down.
- A **migrate** step runs once before either starts. Use a compose one-shot service with `depends_on: condition: service_completed_successfully`.
- Postgres, the shazam sidecar, the API, and the web app each get a **health check**. All services get `restart: unless-stopped`, and `depends_on` uses `service_healthy`.

### 5.3 Bot

Behavior stays as it is today. Reference: `pr0music/main.go`.

1. Poll the inbox for comment notifications that contain `@sauce` (case-insensitive) and skip `BANNED_USERS`.
2. If the item is already known, **PM the requester** with either the stored result or "Leider wurden keine Informationen gefunden". Use the current German texts verbatim.
3. Otherwise, recognize the music, **store it**, **reply under the mention** with the current German text, and **add the tags** `title`, `artist`, `artist - title`.
4. Periodically **sync the bot's own comments** (votes and content) for display on the site.

Reliability changes, which are the point of the refactor:

- **Fail fast at startup.** If config, the DB, or the pr0gramm session is invalid, exit non-zero and let Docker restart the bot. Never continue with a nil DB (that was today's panic).
- **Don't lose mentions.** Today `GET /api/inbox/comments` marks everything read when it's fetched. That's how the 11 requests were lost on 2026-10-23. Instead, fetch with `markAsRead=false` (documented param, verify it works), **persist each mention in a `requests` table keyed by pr0gramm message id**, process it, and only then mark it read with `GET /api/inbox/read?messageType=comment&messageId=…`. Processing must be idempotent: a mention already answered is never answered twice.
- **Old mentions are skipped (owner decision).** On its first start, the new bot records every mention already in the inbox, or older than the cutover moment, as `skipped` without replying. That includes the 11 lost on 2026-10-23 and anything that arrived during cutover. Only mentions created after the new bot goes live get answered.
- **Bounded retries.** A failed recognition or post stays pending with an attempt count and backoff, and is given up after N attempts with a log line. It must never loop forever or spam.
- **Incremental comment sync.** Today a cron job re-reads every bot comment back to 2021 every 5 minutes. Instead, sync forward from the last stored timestamp and occasionally refresh votes on recent comments. Interval is the builder's call.
- **Session handling.** The pr0gramm API doc (https://pr0.app/api-documentation.html) now lists `token` and `captcha` as required for `POST /api/user/login`. Plain name/password login still worked on 2026-10-23, but that can't be relied on. **Persist the session cookies** (in the DB or a volume) so restarts reuse the session instead of logging in again. Detect an expired session (401/403 or `/api/user/sync` failing) and log it loudly. Provide a small manual login path, e.g. `bot login` CLI that prints the captcha URL, so the owner can re-authenticate. OAuth2 (`/api/oauth/*`) exists but needs a registered client. Out of scope unless it turns out to be easy.
- **A dead session never causes a crash loop** *(my call. This narrows "fail fast": an invalid DB or config still exits non-zero)*. If no valid persisted session exists at startup, the bot tries **one** password login (`PR0_USER`/`PR0_PASSWORD`). If that fails, or the session dies later, the bot **stays running, reports unhealthy, stops polling and logs loudly** until someone runs `bot login`. Repeated logins from restart loops could lock the account or force captchas.
- **Observability.** Structured logs (`log/slog`) for every request lifecycle step, and a clear error log line for any non-2xx from pr0gramm. A small health endpoint on the bot reports DB reachable, last successful inbox poll, and session valid.
- **Recognition: Shazam only** (owner decision). Remove ACRCloud entirely: code, `libacrcloud_extr_tool.so`, and its env vars. A miss is stored as a known item with no title, which is the same as today.
- **Links** keep using Odesli (song.link) to get Spotify/YouTube URLs from the Shazam result. **Store the cover art URL** from the Shazam response, so the website never calls Spotify oEmbed per page view.
- Post-comment errors such as `commentTooSoon` count as retryable.

Starting check *(phase 0, before building)*: requests fell from 773 in Oct 2025 to about 20/month, then 2 in May 2026, and nothing has been stored since 2026-05-05. Find out whether that's real usage or a silent bot failure (for example login, inbox API changes, or the video URL scheme `vid.pr0gramm.com/<thumb>.mp4`). If it's a failure, the fix belongs in the new bot.

### 5.4 Data

- **Postgres stays.** Upgrading from 13 (EOL) to a current major is *my call*, done by dump/restore at cutover. The current instance is **dedicated to pr0sauce**: container `pr0sauce-postgres-1`, its own volume `pr0sauce_pgdata`, and only the `pr0music` database. The new stack gets its **own new Postgres container and volume**, and the old volume stays untouched, so rollback is just starting the old stack again.
- **Real data quirks (checked 2026-10-23):** `items` has 57,558 rows but only 57,503 distinct `item_id`s, so **55 item ids are duplicated** (the old gorm model made both `id` and `item_id` primary-key fields). `comments` has no duplicates (55,228 unique `comment_id`s). Neither table has soft-deleted rows. Dedupe rule for the data migration: for each `item_id`, keep the row with a title, and if several have one, keep the newest. Log what was dropped.
- **Real migrations** (versioned SQL files; goose or similar is the builder's call). The first migration creates the new schema, and a one-time data migration copies everything from the existing `pr0music.items` and `pr0music.comments` tables. Note they live in schema `pr0music`, not `public`.
- Proposed tables *(builder may refine)*:
  - `songs`: one row per pr0gramm item that was looked up. Contains item id, title, artist, album, provider, Spotify/YouTube URLs and ids, cover URL, recognized_at. A miss is a row with no title.
  - `requests`: one row per mention. Contains pr0gramm message id (unique), item id, requester name, created, status (pending/done/failed/skipped), attempts, last error, and the reply type (comment or PM). This makes stats like "requests per week" and "top requesters" possible. Today, repeat requests leave no trace.
  - `bot_comments`: the synced bot comments with up/down votes.
  - Future: `corrections` for the flag-and-suggest feature. **Don't create it now**, but the schema shouldn't make it awkward to add.
- Data access: plain SQL with pgx. sqlc is *my call*. Don't use gorm.
- History only exists in `items`. `requests` starts empty except for what can reasonably be backfilled; backfilling is optional and the builder's call.

### 5.5 JSON API (owner decision: plain JSON, GraphQL removed)

Read-only, built for the website. One shared connection pool, never one connection per request (the current GraphQL service reconnects on every query).

- `GET /api/songs?q=&cursor=&limit=`: search by title or artist with fuzzy matching (pg_trgm or full-text is the builder's call). A purely numeric `q` matches the item id. Without `q`, it returns the most recent recognized songs.
- `GET /api/songs/{itemId}`: a single song, including its bot comment votes. Returns 404 for unknown ids and 400 for non-numeric ids.
- `GET /api/stats`: totals, hit rate, today's count, Shazam share (historic, because ACRCloud rows remain), requests per week, top songs, top requesters. Caching for a short time is fine.
- `GET /healthz`.
- **The API is internal only.** It isn't routed through Traefik, just like the current GraphQL service, which has no Traefik labels and sits only on the internal network, so nothing outside uses it. The browser never calls it directly. Search-as-you-type and "load more" go through Next.js (route handlers or server actions), so the API needs no CORS or public rate limiting.
- Exact JSON shapes and pagination style are the builder's call. Leave room for a future `POST` correction endpoint, but don't build it.

### 5.6 Shazam sidecar

- It lives in `shazam/`, using current Python, a current pinned `shazamio`, FastAPI, and a `/healthz` endpoint.
- It's internal only: not exposed through Traefik and not reachable from outside.
- Whether the sidecar or the bot extracts the audio with ffmpeg is *the builder's call*. The goal is to have ffmpeg in only one image.

## 6. Website (owner-approved via mockups, direction **A: search-first**)

UI language is **German**. The visual style follows today's dark pr0gramm look (background `#161618`, accent `#ee4d2e`).

### 6.1 Home `/`
- Big headline ("Welches Lied läuft da?") and a **search box** for title, artist, or post ID. A numeric ID goes straight to that song.
- Three headline numbers: total requests, hit rate, today.
- A **"Zuletzt erkannt"** list of recent songs with cover, title, artist, post id, and relative time, with load more or pagination.

### 6.2 Song `/[itemId]`
- Cover, title, artist, album, provider pill, recognized date, and Spotify/YouTube buttons.
- A link to the original post `https://pr0gramm.com/new/<itemId>` and the bot comment's votes.
- Invalid or unknown id: a proper 404 page. Never a 500.
- A greyed-out, non-functional "⚑ Falsch erkannt? Korrektur vorschlagen" slot that reserves space for the future feature *(showing it disabled vs. hiding it is the builder's call)*.

### 6.3 Stats `/statistik`
- Totals, hit rate, provider share, requests-per-week chart, top songs, top requesters.

### 6.4 Tech
- A current stable Next.js with the App Router. Server components fetch the Go API over the internal network. Remove Apollo/GraphQL.
- Remove the leftover Sentry/Vercel config *(my call. The owner can ask to keep Sentry)*.
- Add a real favicon and robots.txt. Non-numeric paths must never reach the API as `NaN`.
- The existing URL format `https://pr0sauce.info/<itemId>` **must keep working**, because thousands of old bot comments link to it.

## 7. Build, deploy, cutover

- **One GitHub repo** under the `Pacerino` account (name proposed: `pr0sauce`). One CI workflow builds and pushes images to GHCR (`ghcr.io/pacerino/pr0sauce-{bot-api,web,shazam}` or similar). Images are tagged with the git SHA and, on release, a version. **Production compose pins exact tags. Never use `:latest` or `:main`.**
- Deploy target: the current host, `/opt/pr0sauce` on $PROD_HOST, behind the existing `cloudflared → traefik` chain. Keep the Traefik labels and the external `cloudflaretunnel` network. The `Host(pr0sauce.info)` router goes to the web container.
- **Cutover:**
  1. Back up the current DB (`pg_dump`).
  2. Stop the old bot so two bots never answer the same mention.
  3. Restore the dump into the new Postgres and run the migrations, including the data migration.
  4. Start the new stack.
  5. Verify: the site loads old item URLs, the health checks are green, and a **test mention from the owner's second pr0gramm account** (the owner posts it) gets answered exactly once. Repeat the test mention on the same post to check the PM-for-duplicates path.
  6. Keep the old compose and volumes until the owner confirms. Rollback means bringing the old stack back.
- After cutover, **archive the four old repos** on GitHub *(my call; owner confirms)*. Don't delete them.
- `$PROD_HOST` = the production Docker host (address kept out of the repo).
- Secrets stay in `.env` on the host and are never committed. Provide an `.env.example`.
- **Who runs live steps:** the builder (Claude) has SSH as root to the host and push rights as `Pacerino`. Phase 0, the `markAsRead=false` check against the real inbox, test runs of the data migration against a fresh `pg_dump`, and the cutover are done by the builder. **Every step that writes to production, or posts on pr0gramm, needs the owner's OK first.** Read-only checks don't.

## 8. Left to the builder

Directory naming inside the layout, migration tool, the sqlc vs. hand-written pgx call, search implementation, API JSON shapes and pagination, poll and sync intervals, retry counts and backoff, where ffmpeg lives, chart library, component structure, and test strategy. Use real tests for the bot's request state machine and the data migration, because that's where correctness matters most.

## 9. Calls I made (owner should check)

- Postgres upgrade from 13 to a current major during cutover.
- A `requests` table as a new source of truth for mentions and stats.
- Persisted pr0gramm session plus a manual `bot login` path, instead of OAuth.
- sqlc/pgx instead of gorm.
- Drop Sentry.
- Archive, not delete, the old repos. New repo named `pr0sauce` under `Pacerino`.
- Cover art taken from Shazam and stored, instead of a Spotify lookup on each page view.
- A dead pr0gramm session leaves the bot up and unhealthy, with one password login attempt per start, instead of a crash loop.
- A new, separate Postgres container for the new stack. The old one stays as the rollback.
- Dedupe of the 55 duplicate item ids (keep titled, then newest).
- The API is internal only, and the browser goes through Next.js.
