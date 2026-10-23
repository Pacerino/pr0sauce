# pr0sauce

Monorepo for pr0sauce: a pr0gramm bot that identifies music in posts, plus its API and web frontend.

## Layout

- `cmd/pr0sauce/` - Go entrypoint (`migrate | import-legacy | bot | bot-login | api`)
- `docs/` - design spec, implementation plan, phase-0 findings
- `legacy/` - old repo clones (git-ignored)

Configuration: copy `.env.example` to `.env`.

## Development

    make test

### Shazam sidecar (Python)

Tests need a local venv (Python 3.13, ffmpeg for real recognition):

    uv venv -p 3.13 shazam/.venv
    uv pip install -p shazam/.venv/bin/python -r shazam/requirements-dev.txt

Image: `docker build -t pr0sauce-shazam:dev shazam/`
