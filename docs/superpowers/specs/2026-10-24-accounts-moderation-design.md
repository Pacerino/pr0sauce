# pr0sauce accounts, corrections and moderation — design

Date: 2026-10-24
Status: builder check done; owner answers folded in; awaiting owner approval
Builds on: `2026-10-23-pr0sauce-monorepo-design.md` (live since 2026-10-23)

## 1. Intent

The bot sometimes recognizes the wrong song, and the website shows it as fact. pr0gramm users should be able to fix that themselves, and the owner needs one place to run the bot without SSH.

This adds **"Login mit pr0gramm"** to pr0sauce.info. Logged-in users suggest corrections and vote on them. Moderators and the admin review the suggestions, and the admin gets a dashboard for the bot's operations.

## 2. Goals (owner decisions unless marked)

1. **Login via pr0gramm OAuth 2.0** (authorization code). Client ID `644`. The client secret is server-side only.
2. **Roles:** `admin`, `moderator`, `user`.
   - **Admin is the pr0gramm account `Helix`, only.**
   - Moderators are picked by the admin **by pr0gramm name** in the dashboard.
   - Everyone else who logs in is a `user`.
   - Roles and bans bind to the account's **numeric pr0gramm user id** (`/profile/info` → `user.id`), not the name, so renaming keeps the role and a new account can't take over a name. *(my call, owner agreed. See §5.)*
3. **Correction suggestions.**
   - On a song page, a logged-in user suggests the correct **title + artist**, with **album** and a **proof link** optional *(my call)*.
   - It's allowed for misses as well as hits *(my call)*.
   - **One open suggestion per user per post** *(my call)*.
4. **Voting.**
   - Logged-in users vote ↑ or ↓ on open suggestions, one vote each and changeable. No voting on your own suggestion.
   - **Auto-approval at ≥ 5 upvotes AND net score ≥ +5** (owner decision).
   - Competing suggestions on the same post are shown side by side. The first one approved wins, and the others close as `superseded` *(my call)*.
5. **Moderation.** Moderators and the admin can approve or reject any open suggestion (rejection takes an optional reason). They can **revert** any change, including auto-approved ones, to the bot's original or to an earlier version.
6. **History.** Every change to a song's displayed data is recorded: before, after, source (`suggestion`, `vote`, `moderator`, `revert`), actor and time. It's visible to moderators and the admin. Public visibility is the builder's call.
7. **Corrections change the website only.** The bot never posts a follow-up on pr0gramm (owner decision). pr0gramm only allows editing your own comment within 5 minutes anyway.
8. **Admin dashboard:**
   - Moderator management: add or remove by pr0gramm name.
   - **Bot health:** session state, last poll, and failure and backoff counters.
   - **Failed requests:** status `failed`, with `last_error`, item link and requester.
   - **Ban list (website only, owner decision):** ban or unban users from suggesting and voting, with a reason. The bot's `BANNED_USERS` env list is unchanged and separate.

## 3. Non-goals

- No changes to how the bot replies on pr0gramm.
- No OAuth use by the bot. The bot keeps its cookie session, because pr0gramm's write endpoints (`/comments/post`, `/tags/add`, `/inbox/post`) are `csrfProtected` with **no OAuth scope**. This was verified in the API spec on 2026-10-23.
- The stats extension (history since 2021, per-year tops, post categories) is parked as a separate project.
- **No lost-mentions backlog** (owner decision 2026-10-24). The ~5,180 unanswered mentions from Nov 2025 – Oct 2026 are not imported, processed or shown. The facts are kept in `docs/phase0-findings.md` in case it's revisited.
- No email, notifications or comments threads on suggestions.

## 4. Anti-goals

- The client secret, session tokens or any pr0gramm token showing up in the repo, logs, the browser, or the public API.
- One person with several accounts being able to push a wrong correction through unnoticed. Mitigation: voters are visible to moderators, the ban list, and revert.
- A moderator action that can't be undone. Every change is revertible from the history.
- Breaking the existing public pages or the internal-only API boundary.

## 5. Facts the builder needs (verified)

- **OAuth endpoints:** authorize at `https://pr0gramm.com/oauth/authorize` (`client_id`, `redirect_uri`, `scope`, `state`, `response_type=code`). Token from `POST https://pr0gramm.com/api/oauth/createAccessToken` (`grant_type=authorization_code`, `code`, `redirect_uri`, `client_id`, `client_secret`), which returns `{access_token, token_type, scope}`.
- API calls with the token use the `Authorization: Bearer <token>` header. Cloudflare blocks requests with **no `User-Agent`**, so always send one, e.g. `pr0sauce.info (+https://pr0sauce.info)`.
- **Scopes:** one per endpoint, named like the path (`/api/a/b` → `a.b`). Request exactly **`user.name` and `profile.info`**. At login: get your own name via `GET /api/user/name`, then your **numeric id** via `GET /api/profile/info?name=<that name>&flags=1` → `user.id`. `user.me` exists as an endpoint but has no scope in the current spec, and `user.identifier` is an opaque string with no public name→identifier lookup. That's why the binding uses the numeric id.
- **Name → id for people who never logged in** (adding a moderator, banning): the API resolves the name via `/profile/info?name=` using the **bot's existing session**. That's a read-only call, and the bot's pr0gramm client already has the cookie and User-Agent handling. An unknown name is an error shown in the dashboard, and no pending grants exist. *(my call)*
- **Builder must verify once, live:** `profile/info` returns the same `user.id` before and after a rename, judging by pr0gramm's public behaviour that ids are stable. If it doesn't, stop and report.
- **Redirect URI (owner):** `https://pr0sauce.info/api/auth/callback/pr0`. This is a **Next.js route handler** on the public site, not the internal Go API (which has no public route). For local development use `http://localhost:<port>/api/auth/callback/pr0`, since localhost is always allowed.
- **Client secret:** it was pasted in a chat on 2026-10-23. **Rotate it with pr0gramm before go-live** and store the new one only in the server `.env`, as `PR0_OAUTH_CLIENT_SECRET`.

## 6. Shape (decided parts)

- **The Go API stays internal-only.** The browser never talks to it directly.
- **Next.js server code does the OAuth round-trip.** That covers building the authorize URL with `state` (stored in a short-lived httpOnly cookie), the callback, exchanging the code, and fetching the identifier and name. It then asks the Go API to create or update the user and a session.
- The session is an **httpOnly, Secure, SameSite=Lax cookie** holding an opaque random token. Only its hash is stored in Postgres *(my call)*.
- All mutating actions go browser → Next.js server action or route handler → internal API with the session token. That covers suggest, vote, approve, reject, revert, ban, moderators and backfill. The Next.js side checks `Origin` against `https://pr0sauce.info` *(my call)*.
- The **Go API enforces authorization** for every action: role, ban, own-suggestion, and one-open-per-post. Next.js only hides buttons.
- **Data** lives in new tables in the existing Postgres, via goose migration `00003_*`. The names are the builder's call. It needs:
  - users: pr0gramm identifier (unique), current name, role, ban status, reason, timestamps
  - sessions
  - suggestions: item, author, proposed title, artist, album, proof URL, status (`open | approved | auto_approved | rejected | superseded`), reviewer, reason, timestamps
  - votes: one per user per suggestion, ±1
  - song_history
- **The bot's original data is never lost.** `song_history` stores a **full-row snapshot** before every change. A revert restores the chosen snapshot, and "bot original" is the first one *(my call)*.
- **What a correction does to the other fields** *(my call)*:
  - It sets title, artist and album.
  - It **clears** the fields that belonged to the bot's match: `apple_music_url`, `deezer_url`, `isrc`, `spotify_url/id`, `youtube_url/id` and `cover_url`.
  - It sets `provider = 'community'`, and the page shows "korrigiert durch die Community" or "… durch Moderation".
  - It then makes a **best-effort Deezer search** (keyless `GET https://api.deezer.com/search?q=artist:"…" track:"…"`, first result) to refill `deezer_url` and `cover_url` (album cover). A failure leaves them empty.
  - The Spotify and YouTube **search** links derive from the new title automatically.
- **Corrected rows are protected:** `UpsertSong` (bot and backlog) must never overwrite a song that has at least one accepted correction. Add a guard column (e.g. `corrected_at`) and a `where` clause. Bot replies for such items use the corrected data via the existing PM path.
- **Admin bootstrap.** On login, if the pr0gramm name equals `ADMIN_PR0_NAME` (`.env`, value `Helix`) and no admin exists yet, that account becomes admin and is bound by identifier from then on *(my call)*.
- **Bot health in the dashboard:** the API reads the bot's existing `/healthz` via a new env `BOT_HEALTH_URL=http://bot:8081/healthz` in compose. A 503 body is still parsed. The **counters are derived from the DB**: failed and pending counts, max attempts, the next `next_attempt_at`, mark-read backlog, and the last `done` time. No bot code change is needed *(my call)*.
- **Bans** *(my call)*: a banned user can't suggest or vote. Their **open suggestions close** as `rejected` (reason "gesperrt"), and **their votes stop counting** in tallies, which are recomputed on ban and unban. History is untouched.
- **Song page for misses:** today a miss returns 404. With corrections it shows "Nicht erkannt" plus the suggestion button, but only for items the bot actually looked up. Unknown IDs stay 404 *(my call)*.
- German UI throughout, matching the existing design.

## 7. Left to the builder

The table and column names, the API routes and JSON shapes, the dashboard layout and component structure, pagination, the session lifetime (suggested 30 days, sliding), rate limits on suggest and vote (suggested: max 20 suggestions/day/user), the exact proof-link validation (http(s) via the existing `safeHttpUrl` rules), and whether the history is shown publicly.

## 8. Owner answers (2026-10-24)

1. Backlog: skipped for now. No backlog feature (see §3).
2. Callback URL: `https://pr0sauce.info/api/auth/callback/pr0`.
3. Ban list: website only. `BANNED_USERS` for the bot stays as is.
