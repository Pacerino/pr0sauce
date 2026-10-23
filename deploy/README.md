## Accounts deploy (2026-10)

The web service reads its own env file, `deploy/web.env` (git-ignored; template: `web.env.example`). It holds the OAuth client (`PR0_OAUTH_CLIENT_ID`, `PR0_OAUTH_CLIENT_SECRET`), `PUBLIC_URL` and `INTERNAL_API_TOKEN`. The Go services (bot, api, migrate) read all of `.env`, so the OAuth secret must never go there.

`INTERNAL_API_TOKEN` must be identical in `.env` (for api) and `web.env` (for web). Generate it once with `openssl rand -hex 32` (minimum 32 chars). `.env` also needs `ADMIN_PR0_NAME`.

On the server, put `PR0_OAUTH_CLIENT_SECRET` only into `web.env`, typed in by the owner via an editor, never pasted into chat or commits.
