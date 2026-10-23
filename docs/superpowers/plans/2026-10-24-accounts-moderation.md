# Accounts, Corrections & Moderation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add "Login mit pr0gramm" (OAuth), user correction suggestions with community voting, moderator review with history and revert, and an admin dashboard to pr0sauce.info.

**Architecture:**
- The Go API stays internal and owns all data and authorization. Its new routes live under `/api/internal/*` and require a shared `X-Internal-Token`. Per-user calls also carry `X-Session-Token`.
- Next.js server code does the OAuth round-trip and holds the session cookie. The browser never talks to Go.
- The API resolves pr0gramm names to numeric user ids through the bot's existing session, which is stored in `bot_state`.

**Tech Stack:** unchanged. Go 1.27 + pgx/sqlc/goose, Next.js 16.4.3, Postgres 18.6.

**Spec:** `docs/superpowers/specs/2026-10-24-accounts-moderation-design.md` (approved 2026-10-24).

## Global Constraints

- Commits are authored as `Pacerino <public@pa0x43.me>`. No work identity, no Co-Authored-By. Check with `git var GIT_AUTHOR_IDENT` before committing.
- **Never commit or log the OAuth client secret, session tokens, access tokens or the internal token.** Secrets come from env only: `PR0_OAUTH_CLIENT_SECRET` and `INTERNAL_API_TOKEN`. Before every commit, `git diff --cached | grep -iE "client_secret=[0-9a-f]|PR0_OAUTH_CLIENT_SECRET=.|INTERNAL_API_TOKEN=.|Bearer [A-Za-z0-9]{8}"` must print nothing.
- OAuth:
  - client id `644`
  - authorize `https://pr0gramm.com/oauth/authorize`
  - token `https://pr0gramm.com/api/oauth/createAccessToken`
  - redirect `https://pr0sauce.info/api/auth/callback/pr0`
  - scopes exactly `user.name profile.info`
  - every pr0gramm request sends a `User-Agent`
- Roles and bans are bound to the numeric pr0gramm user id. Admin bootstrap uses `ADMIN_PR0_NAME=Helix`.
- Auto-approval needs **≥ 5 upvotes AND net ≥ +5**, counting only votes from non-banned users. The author can't vote on their own suggestion.
- Each user may have **one open suggestion per item**, and at most **20 suggestions per user per rolling 24 h**.
- Corrections change the website only. The bot never posts about them.
- A correction clears `apple_music_url, deezer_url, isrc, spotify_url, spotify_id, youtube_url, youtube_id, cover_url`, sets `provider='community'` (vote) or `'moderation'` (moderator), sets `corrected_at`, and refills `deezer_url`/`cover_url` by best-effort Deezer search.
- `song_history` stores a full-row JSON snapshot **before** every change. A revert restores a chosen snapshot and records itself as history.
- New migration `migrations/00003_accounts.sql`. Production is at version 2, so never edit 00001 or 00002.
- German UI labels: `Mit pr0gramm anmelden`, `Abmelden`, `Falsch erkannt? Korrektur vorschlagen`, `Vorschläge`, `Übernehmen`, `Ablehnen`, `Zurücksetzen`, `Moderation`, `Dashboard`, `Nicht erkannt`, `korrigiert durch die Community`, `korrigiert durch Moderation`, `Gesperrt`.
- Every external URL rendered on the site goes through `safeHttpUrl`. Every mutating web route checks that `Origin` is `https://pr0sauce.info` (or `PUBLIC_URL`).

## Review Focus

1. **A banned user's existing votes.** They must stop counting immediately, and a ban must never trigger an auto-approval by recount. Test `TestBan_RemovesVotesFromTally` (Task 4).
2. **The bot re-processing a corrected item.** `UpsertSong` must not overwrite it. Test `TestUpsertSong_SkipsCorrected` (Task 1).
3. **Two suggestions crossing the threshold concurrently.** Exactly one wins and the other is `superseded`, enforced in one transaction with a row lock on the song. Test `TestAutoApprove_ConcurrentOnlyOneWins` (Task 3).
4. **OAuth `state` mismatch, replay, or a missing cookie** gives 400 with no session. **A user renamed on pr0gramm** keeps their role, because the binding is the numeric id. Tests in Task 5.
5. **Requests without the internal token or with a forged session** get 401, and **a moderator calling admin routes** gets 403. Test `TestAuthz_Matrix` (Task 2).

---

### Task 1: Schema, queries, guard

**Files:**
- Create: `migrations/00003_accounts.sql`, `internal/db/queries/accounts.sql`, `internal/db/queries/suggestions.sql`
- Modify: `internal/db/queries/songs.sql` (the UpsertSong guard, plus `ApplyCorrection`, `SongSnapshot` and `RestoreSnapshot`). Regenerate with `go tool sqlc generate`.
- Test: `internal/db/accounts_test.go`

**Schema (Up; Down drops everything it adds):**
- `songs.corrected_at timestamptz` (nullable)
- `users(id bigserial pk, pr0_user_id bigint unique not null, name text not null, role text not null check (role in ('user','moderator','admin')) default 'user', banned_at timestamptz, ban_reason text, created_at, last_login_at)`
- `sessions(token_hash bytea pk, user_id bigint references users on delete cascade, created_at, expires_at)`
- `suggestions(id bigserial pk, item_id bigint not null references songs(item_id), user_id bigint not null references users, title text not null, artist text not null, album text, proof_url text, status text not null check (status in ('open','approved','auto_approved','rejected','superseded')), reviewer_id bigint references users, reason text, created_at, decided_at)`, plus a partial unique index on `(item_id, user_id) where status='open'`
- `votes(suggestion_id bigint references suggestions on delete cascade, user_id bigint references users, value smallint not null check (value in (-1,1)), created_at, primary key (suggestion_id, user_id))`
- `song_history(id bigserial pk, item_id bigint not null, snapshot jsonb not null, source text not null check (source in ('suggestion','vote','moderator','revert')), actor_id bigint references users, suggestion_id bigint references suggestions, created_at)`

**Interfaces (query names; later tasks use them):**
- users: `UpsertUserLogin(pr0_user_id, name) → users row` (updates name and last_login_at), `GetUserByPr0ID`, `GetUser(id)`, `CountAdmins`, `SetRole(id, role)`, `SetBan(id, reason)`, `ClearBan(id)`, `ListModerators`, `ListBanned`
- sessions: `CreateSession(token_hash, user_id, expires_at)`, `GetSessionUser(token_hash, now) → users row`, `TouchSession(token_hash, expires_at)`, `DeleteSession(token_hash)`
- suggestions: `CreateSuggestion`, `GetSuggestion`, `ListOpenSuggestionsForItem(item_id) → rows with up, down, net`, `ListOpenQueue(limit, offset)`, `CountUserSuggestionsSince(user_id, since)`, `SetSuggestionStatus(id, status, reviewer_id, reason)`, `SupersedeOthers(item_id, keep_id)`, `CloseOpenSuggestionsOfUser(user_id, reason)`
- votes: `UpsertVote(suggestion_id, user_id, value)`, `DeleteVote`, `TallySuggestion(id) → up, down, net` (only votes from users with `banned_at is null`)
- songs: the `UpsertSong` conflict update gains `where songs.corrected_at is null`. Also `LockSong(item_id) for update`, `SongSnapshot(item_id) → jsonb`, `ApplyCorrection(item_id, title, artist, album, provider, deezer_url, cover_url)` (clears the fields listed in Global Constraints and sets `corrected_at=now()`), `RestoreSnapshot(item_id, jsonb)`
- history: `InsertHistory(item_id, snapshot, source, actor_id, suggestion_id)`, `ListHistory(item_id)`, `GetHistory(id)`
- requests (for the dashboard): `CountRequestsByStatus`, `ListFailedRequests(limit, offset)`, `PendingRetryStats → count, max_attempts, next_attempt_at`, `LastDoneAt`

- [ ] **Step 1: Write the failing tests.**
  - `TestMigrate_00003_UpDown`
  - `TestUpsertSong_SkipsCorrected`: the row has `corrected_at` set, and an upsert with a different title leaves the title unchanged.
  - `TestOneOpenSuggestionPerUserItem`: a second open suggestion violates the unique index, but one is allowed after the first is rejected.
  - `TestTally_ExcludesBannedVoters`
  - `TestApplyCorrection_ClearsMatchFields`
  - `TestSnapshotRestore_RoundTrip`
- [ ] **Step 2:** Run `go test ./internal/db/` and expect FAIL.
- [ ] **Step 3:** Implement the migration and queries, then run `go tool sqlc generate`.
- [ ] **Step 4:** Run `go test -count=1 ./... && go vet ./...` and expect PASS.
- [ ] **Step 5:** Commit with `feat(db): accounts, suggestions, votes, history; protect corrected songs`.

### Task 2: API auth core and pr0gramm/Deezer lookups

**Files:**
- Create: `internal/api/auth.go`, `internal/api/auth_test.go`, `internal/api/pr0lookup.go`
- Modify: `internal/api/server.go` (the `New` signature, middleware, routes), `internal/config/config.go` (+ `INTERNAL_API_TOKEN`, `ADMIN_PR0_NAME`, `BOT_HEALTH_URL`), `internal/pr0gramm/client.go` (+ `ProfileInfo`), `internal/deezer/deezer.go` (+ `SearchTrack`), `cmd/pr0sauce/main.go` (`runAPI` wiring)

**Interfaces:**
- `pr0gramm.(*Client).ProfileInfo(ctx, name string) (Profile{ID int64; Name string}, error)` calls `GET /profile/info?name=<name>&flags=1` and reads `user.id` and `user.name`. An unknown name returns `pr0gramm.ErrUserNotFound`.
- `deezer.(*Client).SearchTrack(ctx, artist, title string) (link, cover string, err error)` calls `GET {base}/search?q=artist:"<artist>" track:"<title>"&limit=1`, takes the first result's `link` (with an `https://www.deezer.com/` prefix check) and `album.cover_big`. No result returns ErrNotFound.
- `api.New(q, pool, log, Options{InternalToken, AdminName, BotHealthURL string; Pr0 Pr0Lookup; Deezer DeezerSearch; Now func() time.Time}) http.Handler`
  - `Pr0Lookup` is an interface: `ProfileInfo(ctx, name) (pr0gramm.Profile, error)`.
  - In production it is a pr0gramm client whose cookies are loaded from `bot_state.session_cookies` on each call.
  - A config with `INTERNAL_API_TOKEN` empty must fail at startup.
- Middleware on `/api/internal/*`:
  - It requires `X-Internal-Token` equal to the configured token, compared in constant time. Otherwise 401.
  - It resolves `X-Session-Token` (sha256 of the token, looked up via `GetSessionUser`) into the request context.
  - It provides the helpers `requireUser`, `requireMod` and `requireAdmin`. A banned user gets 403 `{"error":"Gesperrt"}` on any write.
- Routes:
  - `POST /api/internal/sessions {pr0UserId, name}` → `{token, user:{id,name,role}}`. The token is 32 random bytes, base64url, expiring in 30 days. **Admin bootstrap:** if `name == AdminName` and `CountAdmins()==0`, the user becomes admin.
  - `DELETE /api/internal/sessions` (session).
  - `GET /api/internal/me` → `{id,name,role,banned}`. Sliding expiry: touch the session when under 15 days remain.

- [ ] **Step 1: Write the failing tests.**
  - `TestAuthz_Matrix`, table-driven: no token → 401; a wrong token → 401; a valid token with no session on `/me` → 401; a user on a mod route → 403; a mod on an admin route → 403; the admin → 200.
  - `TestSessions_CreateMeDelete`.
  - `TestAdminBootstrap_OnlyFirstHelix`: a second login as Helix doesn't create a second admin. Another name is never admin.
  - `TestSession_TokenNotStoredPlain`.
  - `TestProfileInfo_Parses` / `_Unknown` (pr0gramm, httptest).
  - `TestDeezerSearchTrack` / `_NoResult`.
- [ ] **Step 2:** Run `go test ./internal/api/ ./internal/pr0gramm/ ./internal/deezer/` and expect FAIL.
- [ ] **Step 3:** Implement. Existing public routes (`/api/songs`, `/api/stats`, `/healthz`) stay unauthenticated and unchanged.
- [ ] **Step 4:** Run `go test -count=1 ./... && go vet ./...` and expect PASS.
- [ ] **Step 5:** Commit with `feat(api): internal auth, sessions, admin bootstrap, pr0gramm/deezer lookups`.

### Task 3: Suggestions, votes, auto-approval, moderation, history

**Files:**
- Create: `internal/api/suggestions.go`, `internal/api/corrections.go`, `internal/api/suggestions_test.go`
- Modify: `internal/api/songs.go`. `GET /api/songs/{itemId}` returns **misses too**, with `"found": false` (a title of null means a miss). Unknown items still 404. Add `"corrected": "community"|"moderation"|null` and the `found` field.

**Interfaces (all under `/api/internal`, JSON):**
- `GET /songs/{itemId}/suggestions` (session optional) → `[{id, title, artist, album, proofUrl, author, up, down, net, myVote, mine, createdAt}]`, open ones only
- `POST /songs/{itemId}/suggestions` (user) `{title, artist, album?, proofUrl?}`
  - title and artist are trimmed, 1–200 runes; album ≤ 200
  - proofUrl must be http(s) and ≤ 500, otherwise 400
  - 409 if the user already has an open suggestion for the item
  - 429 if over 20 in 24 h
- `PUT /suggestions/{id}/vote` (user) `{value: 1|-1|0}`. 0 removes the vote. 403 on your own suggestion, 409 if not open. After every vote, run `maybeAutoApprove(id)`.
- `POST /suggestions/{id}/approve` and `/reject {reason?}` (mod)
- `GET /mod/queue?offset=` (mod), open suggestions with tallies, oldest first
- `GET /songs/{itemId}/history` (mod), `POST /songs/{itemId}/revert {historyId}` (mod)
- `applyCorrection(ctx, tx, suggestion, source, actor)`, all in **one transaction**:
  1. `LockSong`
  2. insert history with the pre-change snapshot
  3. `ApplyCorrection` with Deezer `SearchTrack`. A failure means NULL. The Deezer call happens **before** the tx starts, so no network call holds the lock.
  4. set the status
  5. `SupersedeOthers`
- `maybeAutoApprove`: in a tx, lock the song, re-read the tally and the status, and apply if `up ≥ 5 && net ≥ 5` and still open (source `vote`, provider `community`).

- [ ] **Step 1: Write the failing tests.**
  - `TestSuggest_Validation`
  - `TestSuggest_OneOpenPerItem`
  - `TestSuggest_RateLimit`
  - `TestVote_NotOwn`
  - `TestAutoApprove_AtFiveNetFive`: 5 up and 0 down approves. 6 up and 2 down doesn't (net 4). 7 up and 2 down approves.
  - `TestAutoApprove_ConcurrentOnlyOneWins`: two suggestions on one item, with votes pushed in parallel goroutines. Exactly one is `auto_approved` and the other is `superseded`.
  - `TestApprove_ClearsLinksAndSetsProvider`
  - `TestRevert_ToBotOriginal`: after two corrections, revert to the first snapshot. Expect the original title, links and provider, and a new `revert` history row.
  - `TestGetSong_MissFound`: a miss gives 200 `found:false`, an unknown item gives 404.
- [ ] **Step 2:** Run `go test ./internal/api/` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test -count=1 -race ./internal/api/ && go vet ./...` and expect PASS.
- [ ] **Step 5:** Commit with `feat(api): suggestions, voting with auto-approval, moderation, history and revert`.

### Task 4: Admin endpoints (moderators, bans, bot health, failed requests)

**Files:**
- Create: `internal/api/admin.go`, `internal/api/admin_test.go`

**Interfaces (under `/api/internal/admin`, admin only):**
- `GET /moderators`, plus `POST /moderators {name}`
  - The handler resolves the name via `Pr0Lookup.ProfileInfo` and upserts a user `{pr0UserId, name}` with role `moderator`.
  - An unknown name returns 404 `{"error":"pr0gramm-Nutzer nicht gefunden"}`.
  - Admins can't be demoted this way.
- `DELETE /moderators/{userId}`
- `GET /bans`, plus `POST /bans {name, reason}`
  - It resolves the name the same way, then sets the ban, closes the user's open suggestions with reason `gesperrt`, and deletes their sessions.
  - It can't target the admin.
- `DELETE /bans/{userId}`
- `GET /bot` → `{health: <parsed /healthz body or {"error":"unreachable"}>, httpStatus, requests:{pending, replying, done, failed, skipped}, retry:{pending, maxAttempts, nextAttemptAt}, markReadBacklog, lastDoneAt}`. It uses `BOT_HEALTH_URL` with a 3 s timeout and also parses the 503 body.
- `GET /failed?offset=` → `[{id, itemId, requester, attempts, lastError, updatedAt}]`

- [ ] **Step 1: Write the failing tests.**
  - `TestModerators_AddByName_Unknown404`
  - `TestBan_RemovesVotesFromTally`: a suggestion has 5 up, including one from X. Banning X leaves it open with an up-tally of 4, and unbanning restores 5 **without** auto-approving, because approval only runs on a vote.
  - `TestBan_ClosesOpenSuggestionsAndSessions`
  - `TestBan_CannotTargetAdmin`
  - `TestBotHealth_ParsesDegraded`: an httptest server returns 503 with a JSON body.
  - `TestFailedList`
- [ ] **Step 2:** Run `go test ./internal/api/ -run 'Moderators|Ban|BotHealth|Failed'` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test -count=1 ./... && go vet ./...` and expect PASS.
- [ ] **Step 5:** Commit with `feat(api): admin moderators, bans, bot health, failed requests`.

### Task 5: Web OAuth login and session

**Files:**
- Create:
  - `web/lib/session.ts` (server-only: read the cookie, call `/api/internal/me`)
  - `web/lib/internal.ts` (server-only fetch helper that adds `X-Internal-Token` and `X-Session-Token`)
  - `web/lib/origin.ts` (`assertSameOrigin(request)`)
  - `web/app/api/auth/login/route.ts`
  - `web/app/api/auth/callback/pr0/route.ts`
  - `web/app/api/auth/logout/route.ts`
  - `web/components/UserMenu.tsx`
  - tests: `web/lib/origin.test.ts`, `web/lib/oauth.test.ts`
- Modify: `web/app/layout.tsx` (UserMenu in the nav: `Mit pr0gramm anmelden`, or the name plus `Dashboard` for mods and admins plus `Abmelden`)

**Behavior:**
- `GET /api/auth/login?next=/<path>`
  - Generate a `state` (32 random bytes). Store `{state, next}` in an httpOnly Secure SameSite=Lax cookie `pr0sauce_oauth`, valid for 10 min.
  - `next` must start with a single `/` (no `//`, no scheme), otherwise it becomes `/`.
  - 302 to the authorize URL: `response_type=code`, `client_id=644`, `redirect_uri`, `scope=user.name profile.info`, `state`.
- `GET /api/auth/callback/pr0?code&state`
  - The state must match the cookie, otherwise 400. Delete the cookie.
  - POST to the token URL (form: grant_type, code, redirect_uri, client_id, client_secret, plus a User-Agent).
  - `GET https://pr0gramm.com/api/user/name` with Bearer gives the name.
  - `GET /api/profile/info?name=<name>&flags=1` with Bearer gives the id.
  - Internal `POST /sessions` returns the token.
  - Set cookie `pr0sauce_session` (httpOnly, Secure, SameSite=Lax, 30 days), then 302 to `next`.
  - The access token is discarded and never stored.
- `POST /api/auth/logout`: assertSameOrigin, call the internal DELETE, clear the cookie, 303 to `/`.
- Env (server-only):
  - `PR0_OAUTH_CLIENT_ID=644`, `PR0_OAUTH_CLIENT_SECRET`
  - `PUBLIC_URL=https://pr0sauce.info`, from which the redirect URI is derived as `${PUBLIC_URL}/api/auth/callback/pr0`
  - `INTERNAL_API_TOKEN`
  - Nothing is `NEXT_PUBLIC_`.

- [ ] **Step 1: Write the failing tests** (vitest with fetch mocked).
  - `buildAuthorizeUrl` contains the exact params and an encoded scope.
  - `safeNext`: `/6796145` passes. `//evil.com`, `https://evil.com` and `javascript:` become `/`.
  - `assertSameOrigin` rejects a missing or foreign Origin.
  - The callback handler: a state mismatch gives 400 with no Set-Cookie. On the happy path it sets the session cookie, and the token-exchange body includes `client_secret` while the response never echoes it.
- [ ] **Step 2:** Run `cd web && npx vitest run` and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `npx vitest run && npm run build` and expect PASS.
- [ ] **Step 5:** Commit with `feat(web): login with pr0gramm (OAuth), session cookie, user menu`.

### Task 6: Web corrections UI

**Files:**
- Create: `web/components/Suggestions.tsx` (list plus vote buttons), `web/components/SuggestForm.tsx`, `web/app/[id]/actions.ts` (server actions: suggest, vote; each calls assertSameOrigin via headers)
- Modify:
  - `web/app/[id]/page.tsx`: a miss renders `Nicht erkannt` plus the form. The corrected badge reads `korrigiert durch die Community` or `korrigiert durch Moderation`. The greyed-out slot is replaced by the live `Falsch erkannt? Korrektur vorschlagen`.
  - `web/lib/types.ts` (`found`, `corrected`, Suggestion)

**Behavior:**
- Logged out, the button links to `/api/auth/login?next=/<id>`.
- Logged in, it opens the form (title, artist, optional album and proof link).
- The list shows open suggestions with ▲ ▼, the net score and "noch N bis zur Übernahme" (`max(5-up, 5-net)`).
- Your own suggestion shows "dein Vorschlag" and no vote buttons. Banned users see `Gesperrt` and no controls.
- After any action, `revalidatePath('/<id>')`.

- [ ] **Step 1: Write the failing tests.** `web/lib/suggestions.test.ts` covers `remainingToApprove(up, net)` (5/5 → 0, 3/1 → 4) and the form validation helper (lengths, proof via safeHttpUrl).
- [ ] **Step 2:** Run vitest and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `npx vitest run && npm run build` and expect PASS.
- [ ] **Step 5:** Commit with `feat(web): correction suggestions and voting on song pages`.

### Task 7: Web dashboard

**Files:**
- Create: `web/app/dashboard/layout.tsx` (guard: mod or admin, otherwise notFound), `page.tsx` (Moderation queue), `songs/[id]/page.tsx` (history plus revert), `admin/page.tsx` (admin-only: moderators, bans, bot health, failed requests), `actions.ts`

**Behavior:**
- **Queue:** old vs. suggested values side by side, the tally, the author, a link to `https://pr0gramm.com/new/<id>`, and `Übernehmen` / `Ablehnen` (with a reason).
- **History:** snapshots newest first, with a `Zurücksetzen` per entry and a confirmation.
- **Admin page:**
  - moderator list plus "add by name"
  - ban list plus "ban by name with reason", and unban
  - bot health tiles: session, last poll, counters
  - a paged failed-requests table
- Moderators see the queue and history only. The admin page returns 404 for them.

- [ ] **Step 1: Write the failing tests.** These are vitest tests of pure helpers only: `formatHealth(body)` gives a German status text, and `canSeeAdmin(role)`.
- [ ] **Step 2:** Run vitest and expect FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `npx vitest run && npm run build` and expect PASS.
- [ ] **Step 5:** Commit with `feat(web): moderation and admin dashboard`.

### Task 8: Deploy [OWNER OK]

- [ ] **Step 1:** Update `deploy/compose.yml` and `deploy/.env.example` (empty values):
  - **api:** `INTERNAL_API_TOKEN`, `ADMIN_PR0_NAME`, `BOT_HEALTH_URL=http://bot:8081/healthz`
  - **web:** `INTERNAL_API_TOKEN`, `PR0_OAUTH_CLIENT_ID`, `PR0_OAUTH_CLIENT_SECRET`, `PUBLIC_URL`
  - The bot gets none of these.

  Commit, push, and wait for CI to go green.
- [ ] **Step 2 (owner):** **Regenerate the OAuth client secret** with pr0gramm, because the old one was pasted in chat. Keep the new one ready for the server `.env`, and **don't paste it into chat**.
- [ ] **Step 3: [OWNER OK]** On `$PROD_HOST`, in `/opt/pr0sauce/v2/`. Order matters: both env files and both tokens must be in place before `docker compose up -d`, otherwise the api crash-loops on a missing token and takes the site down.
  1. Generate one token with `openssl rand -hex 32` (at least 32 chars). Put the same value into both files, never into chat or commits.
  2. Create `web.env` (git-ignored, template `web.env.example`) **before** any `docker compose pull/up`. It holds `PR0_OAUTH_CLIENT_ID=644`, `PR0_OAUTH_CLIENT_SECRET`, `PUBLIC_URL=https://pr0sauce.info` and `INTERNAL_API_TOKEN`. The web service reads only this file.
  3. The owner types `PR0_OAUTH_CLIENT_SECRET` into `web.env` themselves, via `ssh` + editor. **Never put the OAuth secret into `.env`**: the Go containers (bot, api, migrate) read all of `.env`.
  4. In `.env` add `INTERNAL_API_TOKEN` (same value as in `web.env`) and `ADMIN_PR0_NAME=Helix`.
  5. Set `TAG=sha-<new>` and run `docker compose pull -q`.
  6. Run `docker compose run --rm migrate` (expect `OK 00003_accounts.sql`).
  7. Only now run `docker compose up -d`, and check all services are healthy.
- [ ] **Step 4: Live test (owner):**
  1. Log in as **Helix**. The nav shows `Dashboard`, and the admin page loads with bot health.
  2. Add a moderator by name, then remove it.
  3. On a song, submit a suggestion. Then reject it in the queue, and check the history is untouched.
  4. Submit again, approve it, check the page shows `korrigiert durch Moderation`, then revert to the original.
  5. Log out.
