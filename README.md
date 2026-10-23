# pr0sauce

Monorepo for pr0sauce: a pr0gramm bot that identifies music in posts, plus its API and web frontend.

## Layout

- `cmd/pr0sauce/` - Go entrypoint (`migrate | import-legacy | bot | bot-login | api`)
- `docs/` - design spec, implementation plan, phase-0 findings
- `legacy/` - old repo clones (git-ignored)

Configuration: copy `.env.example` to `.env`.

## Development

    make test
