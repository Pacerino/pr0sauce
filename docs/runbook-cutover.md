# Cutover runbook: legacy pr0sauce → monorepo stack

Every step that changes production needs the owner's OK before it runs.
`$PROD_HOST` is the production Docker host. Its address is kept out of this repo.
All commands run as root on `$PROD_HOST` unless they say otherwise.

| | Old stack (rollback) | New stack |
|---|---|---|
| Compose | `/opt/pr0sauce/docker-compose.yml`, project `pr0sauce` | `/opt/pr0sauce/v2/compose.yml`, project `pr0sauce2` |
| DB | container `pr0sauce-postgres-1`, volume `pr0sauce_pgdata` | `pr0sauce2-postgres-1`, volume `pr0sauce2_pgdata` |
| Traefik router | `frontend` | `pr0sauce-web` (same `Host(pr0sauce.info)`, so never run both webs at once) |

**Never** run `docker compose down -v` on either project. `-v` deletes the database volume.

## 0. Preflight (read-only)

```sh
# on the Mac: CI green for the tag being deployed
gh run list -R Pacerino/pr0sauce -L 1
TAG=sha-<7-char-sha>          # the commit CI just built

# on $PROD_HOST
df -h /                                   # need a few GB free for images + new DB
docker ps --filter name=pr0sauce --format '{{.Names}}\t{{.Status}}'
for i in go web shazam; do docker pull -q ghcr.io/pacerino/pr0sauce-$i:$TAG; done   # public, no login needed
```

## 1. Backup [OWNER OK]

```sh
docker exec pr0sauce-postgres-1 pg_dump -U pr0sauce -Fc pr0music \
  > /opt/pr0sauce/backup-pre-v2-$(date +%F-%H%M).dump
ls -la /opt/pr0sauce/backup-pre-v2-*.dump    # size must be > 0
```

## 2. Deploy files [OWNER OK]

```sh
# on the Mac
ssh root@$PROD_HOST mkdir -p /opt/pr0sauce/v2
scp deploy/compose.yml root@$PROD_HOST:/opt/pr0sauce/v2/compose.yml
```

On `$PROD_HOST`, create `/opt/pr0sauce/v2/.env` with `chmod 600`, starting from `deploy/.env.example`:

- `TAG=$TAG`
- `POSTGRES_USER=pr0sauce`, `POSTGRES_DB=pr0sauce`, and `POSTGRES_PASSWORD=$(openssl rand -hex 24)`, which is a new password unrelated to the old one
- `DATABASE_URL=postgres://pr0sauce:<that password>@postgres:5432/pr0sauce?sslmode=disable`
- `PR0_USER=Sauce`, plus `PR0_PASSWORD` copied from `/opt/pr0sauce/.env`
- `BANNED_USERS=` the value from the old compose file's `BANNED_USERS`
- `LEGACY_DATABASE_URL=` and `PR0_BASE_URL=` both stay **empty**

```sh
cd /opt/pr0sauce/v2 && docker compose config -q && docker compose pull
```

## 3. Stop the old bot, migrate, import [OWNER OK]

From this point no bot answers mentions until step 5. Mentions keep arriving unread, and the new bot skips them as "older than first start".

```sh
docker stop pr0sauce-bot-1
cd /opt/pr0sauce/v2
docker compose up -d postgres
docker compose run --rm migrate                 # goose: OK 00001_init.sql

# temporary copy of the legacy DB, restored from the step-1 dump, on the new network
docker run -d --name legacy-restore --network pr0sauce2_internal \
  -e POSTGRES_USER=pr0sauce -e POSTGRES_PASSWORD=restore -e POSTGRES_DB=pr0music postgres:13
sleep 10
docker exec -i legacy-restore pg_restore -U pr0sauce -d pr0music --no-owner \
  < $(ls -t /opt/pr0sauce/backup-pre-v2-*.dump | head -1)

docker compose run --rm \
  -e LEGACY_DATABASE_URL='postgres://pr0sauce:restore@legacy-restore:5432/pr0music?sslmode=disable' \
  migrate import-legacy
# expect about Songs=57503 Hits=46704 DuplicatesDropped=55 Comments=55228
# (a few more if anything was stored since 2026-10-23; there should be none)

docker rm -f legacy-restore
```

## 4. Switch the website [OWNER OK]

There is a short website outage, about a minute, between these two commands.

```sh
docker stop pr0sauce-frontend-1 pr0sauce-graphql-1 pr0sauce-shazam-1
cd /opt/pr0sauce/v2 && docker compose up -d shazam api web
docker compose ps        # shazam, api, web → healthy

# through Traefik, bypassing the Cloudflare challenge that blocks curl
docker exec traefik wget -qO- --header 'Host: pr0sauce.info' http://pr0sauce2-web-1:3000/6796145 | grep -o 'Kumite' | head -1
docker exec traefik wget -qS -O /dev/null --header 'Host: pr0sauce.info' http://pr0sauce2-web-1:3000/favicon.ico 2>&1 | grep HTTP   # 404
```

Owner check in a browser: `https://pr0sauce.info/`, `/6796145`, `/statistik`.

## 5. Start the new bot [OWNER OK]

```sh
cd /opt/pr0sauce/v2 && docker compose up -d bot
docker compose logs -f bot        # Ctrl-C after ~1 min
docker exec pr0sauce2-bot-1 /pr0sauce healthcheck http://127.0.0.1:8081/healthz && echo healthy
```

- **Healthy:** carry on to step 6.
- **"session invalid":** the one password login failed. Run the following with the owner present:

  ```sh
  docker compose -p pr0sauce2 -f /opt/pr0sauce/v2/compose.yml run --rm -it bot bot-login --captcha-out /tmp
  ```

  It prints the captcha as a `data:` URI. Paste it into a browser address bar, then type the text it shows. The running bot picks up the new session within 10 minutes, with no restart needed.
- **"session unknown":** pr0gramm or Cloudflare is not answering cleanly. Wait, because the bot retries with backoff.

**Mark-read check.** This endpoint was never exercised live before cutover. After the first ticks:

```sh
docker compose exec postgres psql -U pr0sauce -d pr0sauce -c \
  "select status, marked_read, count(*) from requests group by 1,2 order by 1,2"
docker compose logs bot | grep -i 'mark read' | tail
```

- **Expected:** all `skipped` rows have `marked_read = t` within a few minutes. The bot does 10 per 5 s tick.
- **If they stay `f` with ERROR lines:** the endpoint is wrong. Delivery is unaffected and the retries back off on their own, but report it so the client can be fixed.

## 6. Live test (owner) [OWNER OK]

1. The owner posts `@Sauce` from their second account under a **recent video**. Within about 1 minute: **exactly one** reply comment and the tags.
2. The owner posts `@Sauce` again under **the same post**. Within about 1 minute: **exactly one** PM ("Jemand hat bereits danach gefragt…").

```sh
docker compose exec postgres psql -U pr0sauce -d pr0sauce -c \
  "select message_id, status, reply_kind, marked_read, last_error from requests order by id desc limit 2"
# expect: done | pm | t   and   done | comment | t
```

## 7. Keep the rollback path

- Leave the old containers **stopped, not removed**, and leave volume `pr0sauce_pgdata` untouched.
- Remove the backup dump only after the owner confirms.
- Old containers have no restart policy, so they stay stopped across reboots.

### Rollback (any time)

```sh
cd /opt/pr0sauce/v2 && docker compose down        # NO -v
cd /opt/pr0sauce && docker compose up -d
```

The old bot still has the 404 bug on new videos, so rollback restores the old state, not a working bot.

## 8. After cutover (separate OKs)

- Archive the 4 old GitHub repos after adding a pointer to `Pacerino/pr0sauce` in each README (Task 17).
- Remove the stopped old containers. The old volume and the dump go only on a separate, explicit OK.
