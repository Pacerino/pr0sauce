## Accounts deploy (2026-10)

The web service reads its own env file, `deploy/web.env` (git-ignored; template: `web.env.example`). It holds the OAuth client (`PR0_OAUTH_CLIENT_ID`, `PR0_OAUTH_CLIENT_SECRET`), `PUBLIC_URL` and `INTERNAL_API_TOKEN`. The Go services (bot, api, migrate) read all of `.env`, so the OAuth secret must never go there.

`INTERNAL_API_TOKEN` must be identical in `.env` (for api) and `web.env` (for web). Generate it once with `openssl rand -hex 32` (minimum 32 chars). `.env` also needs `ADMIN_PR0_NAME`.

On the server, put `PR0_OAUTH_CLIENT_SECRET` only into `web.env`, typed in by the owner via an editor, never pasted into chat or commits.

## Sentry (bot only)

The bot reports to Sentry when `SENTRY_DSN` is set in `deploy/.env`; empty means Sentry is fully off (no init, no network). Only the `bot` service uses it; `api`, `migrate` and `bot-login` never do. `SENTRY_RELEASE` (`$TAG`) and `SENTRY_ENVIRONMENT` (`production`) are set in `compose.yml`.

What is sent: slog records at ERROR or above (grouped by message), panics (the bot still crashes and restarts), a one-off error when the pr0gramm session turns invalid, and a cron check-in for monitor `pr0sauce-bot-poll` after successful polls (at most one per minute, monitor created on first check-in). No check-ins while the session is invalid or unknown, so Sentry flags the missed check-ins on purpose. Cookies, headers and keys matching cookie/token/password/secret/authorization/nonce are scrubbed. Type the DSN in via an editor, never into chat or commits.
