# Phase 0 findings (Task 0, Steps 1-2 plus finding (d))

Date: 2026-10-23. Read-only. Labels: **[FACT]** = observed output, **[INFERENCE]** = my reading.
Not done: Step 3 (a), (b), (c). They need a session and the owner's OK.

## (d) video URL

**[FACT]** For recent video posts, `https://vid.pr0gramm.com/<thumb-without-ext>.mp4` returns **404**. `https://vid.pr0gramm.com/<image>` returns **200 video/mp4**. The API `image` field now carries a transcoding suffix such as `-h264-sd`, `-h264-hd`, `-h264-full_hd` or `-vp9-low_res`. The thumb stem has no suffix.

| item | image | thumb-derived URL | image URL |
|---|---|---|---|
| 7181507 | 2026/10/23/585e3a3e6433996c-h264-full_hd.mp4 | 404 | 200 |
| 7181504 | 2026/10/23/4b622080fb112ab1-h264-sd.mp4 | 404 | 200 |
| 7181483 | 2026/10/23/831880ef299bd262-vp9-low_res.mp4 | 404 | 200 |

**[FACT]** Item 6796145 is public with `flags=1`. It is an old-scheme item: `image` = `2025/10/22/ee519eee5681d75b.mp4` and thumb = `2025/10/22/ee519eee5681d75b.jpg`. The thumb-derived URL and the image URL are the same string and return 200.
`items/get?id=6796145&flags=31` returns 403 logged out. `flags=1` works.
`/api/items/info?itemId=6796145` returns 19 comments, none by `Sauce`.

**[INFERENCE]** Item 6796145 is an older post (created 2025-10-22) whose last bot comment was written on 2026-05-05, so the old scheme still resolves for it. Using the thumb stem alone breaks on newly uploaded videos, so the thumb-stem URL is not safe to rely on. The new stack should take the `image` field from `items/get` (or `items/info`) and build `https://vid.pr0gramm.com/<image>`.

Commands:
```
curl -s 'https://pr0gramm.com/api/items/get?flags=1&promoted=0' -o items.json   # pick image ending in .mp4
curl -sI -o /dev/null -w '%{http_code} %{content_type}\n' https://vid.pr0gramm.com/2026/10/23/585e3a3e6433996c.mp4
curl -sI -o /dev/null -w '%{http_code} %{content_type}\n' https://vid.pr0gramm.com/2026/10/23/585e3a3e6433996c-h264-full_hd.mp4
# same for the 4b622080fb112ab1 and 831880ef299bd262 stems
curl -s 'https://pr0gramm.com/api/items/get?id=6796145&flags=1'
curl -sI https://vid.pr0gramm.com/2025/10/22/ee519eee5681d75b.mp4
```

## (e) silence since May: evidence and most likely cause

### Evidence
1. **[FACT]** The bot log history is gone. `docker logs -t pr0sauce-bot-1` starts at 2026-10-23T13:32:43Z, and the container uses json-file with max-size 10m and max-file 3. The log is flooded: after the 13:15 restart the bot logged 153,502 lines of `Inserted Comment with ID: N` plus the 5-second debug poll. So **Step 1's "when did Comment written stop" cannot be answered from logs**. No "Comment written", "Private message sent", `level=error`/`warning`, fatal, panic or HTTP-status lines exist in the retained log.
2. **[FACT]** In the source, `failed fetching comments` is logged at **Info** level (main.go:173), so the `level=(error|warning)` filter would never match it. `grep -c "failed fetching comments"` over the retained log gave 0, so I have no per-month count.
3. **[FACT]** DB (`pr0music.items`):
   - Newest item is 2026-05-05 21:47:58Z; 57,558 rows.
   - Weekly counts since 2025-11-01: 166 (week of 11-03), 111, then 18, 18; from December 2025 it is 2-12 per week; last week with rows is 2026-05-04 (1 row).
   - Nothing after 2026-05-05.
4. **[FACT]** DB (`pr0music.comments`):
   - 55,228 rows. Monthly counts: 2025-10 718, 2025-11 341, 2025-12 39, 2026-01 25, 2026-02 20, 2026-03 17, 2026-04 10, 2026-05 2.
   - Newest is comment_id 84042151 (item 6796145) at 2026-05-05 21:50:22Z. The previous one is 2026-05-03.
5. **[FACT]** The bot's own sync loop (main.go ~340-365, "Inserted Comment") ran at 13:32-13:49Z today. It upserts the bot's comments containing "Es wurden" from its own profile. Its last upserted ID is 84042151, the same as the DB maximum. The sync call needs the bot's session, so the credentials worked today at 13:3x-13:49 UTC.
6. **[FACT]** `GET /api/profile/comments?name=Sauce&flags=31` logged out returns **403 `{"code":403,"msg":"Nicht eingeloggt"}`**. The public profile endpoint is no longer available to guests, so the brief's Step 2 as written does not work. The newest bot comment date therefore comes from the DB and the bot's own session sync instead: **2026-05-05 21:50Z**. (A guest on `items/info?itemId=6796145` sees no `Sauce` comment, so that comment may have been deleted or hidden.)
7. **[FACT]** Unanswered mentions, public check: I scanned `items/info` for the newest 840 SFW (`flags=1`) front-page posts from `promoted=0` (about 2026-10-21 16:00Z to now) and looked for `@sauce` or any comment by `Sauce`. Result: **0 hits**. The scan only covers SFW posts. The inbox, where mentions are delivered, needs a session (and `/api/inbox/*` was deliberately not called).
8. **[FACT]** The bot was stopped (exited) for about 4 weeks before 2026-10-23. Monthly item volume fell from 773 (Oct 2025) to about 20/month from Dec 2025.

### Most likely cause (INFERENCE)
- **Mostly usage decay, then a halt around 2026-05-05.**
  - The volume drop (773 in Oct 2025, 166/week early Nov, about 5/week from December) happened while the bot was still running. A bot failure would not produce a gradual decay, so this part is not a crash.
  - The abrupt end at 2026-05-05 21:50Z is consistent with the container stopping or crashing (the host later showed it exited for about 4 weeks). I cannot see why, because the logs rotated away.
- **Confidence:** medium (about 65%) for decay plus a process stop. Low for the specific trigger. A pr0gramm-side change is unconfirmed, though the guest profile endpoint now returning 403 shows the API did change. The guest profile 403 does not itself explain the silence, because the bot uses a session.
- **To confirm:** check `docker inspect` or `journalctl -u docker` for the May exit time and reason (exit code or OOM). The bot's inbox can show mentions that accumulated unread.

### Commands used
```
ssh -o BatchMode=yes root@$PROD_HOST 'docker logs -t pr0sauce-bot-1 2>&1 | grep -vE "check Pr0gramm comments" | grep -E "level=(error|warn)|Comment written|Private message|fatal|panic" | tail -60'
ssh ... 'docker logs -t pr0sauce-bot-1 2>&1 | grep -E "failed fetching comments" | cut -c1-7 | sort | uniq -c'
ssh ... 'docker inspect pr0sauce-bot-1 --format "{{.State.Status}} {{.State.StartedAt}} {{.HostConfig.LogConfig}}"'
ssh ... 'docker exec pr0sauce-postgres-1 psql -U pr0sauce -d pr0music -c "select date_trunc(\$\$week\$\$, created_at)::date, count(*) from pr0music.items where created_at > \$\$2025-11-01\$\$ group by 1 order by 1"'
ssh ... 'docker exec pr0sauce-postgres-1 psql ... -c "select comment_id,item_id,created_at from pr0music.comments order by created_at desc limit 3"'
curl -s 'https://pr0gramm.com/api/profile/comments?name=Sauce&flags=31'     # 403
curl -s 'https://pr0gramm.com/api/items/get?flags=1&promoted=0[&older=ID]'; curl -s 'https://pr0gramm.com/api/items/info?itemId=ID'   # 840 items scanned
```

### Process note
My first log command redirected output to `/dev/shm/x` on the host. That was a write, which the brief forbids. It was in tmpfs, and I removed it right afterwards (`rm -f /dev/shm/x`). The host's other state is untouched (no docker lifecycle commands, no SQL writes, no .env read).

## Open for Step 3 (needs owner OK)

- **(a) `markAsRead=false`: NOT TESTED.** It needs the bot or a test account's session. It also needs the old bot stopped, because the 5-second poll (`/inbox/comments`, pr0gramm.go:120) marks messages read.
- **(b) Session-check endpoint: NOT TESTED.** Candidate `GET /api/user/identifier`; compare with and without cookies. A public hint is the guest 403 `"Nicht eingeloggt"` seen on `/api/profile/comments`.
- **(c) `kind` values: NOT TESTED.** They are only visible in authenticated `/inbox/comments` responses.

## Step 3 results (2026-10-23, with owner OK; old bot stopped 15:57–16:03 UTC+2)

All commands ran on the host with the bot credentials sourced from `/opt/pr0sauce/.env`. Nothing was printed. The cookie jar went in `/root/p0test`, which was removed afterwards, and the test session was logged out (`POST /api/user/logout` returned 200).

- **(a) `markAsRead=false` works. [FACT]** The owner's test mention (message 86233362, item 7181444, from `<user>`) stayed `read: 0` across two `GET /api/inbox/comments?markAsRead=false` calls. `/api/user/sync?offset=0` showed `inbox.mentions: 1` at the same time.
- **(b) Session check = `GET /api/user/identifier`. [FACT]** It returns 200 `{"identifier":…}` with a valid session and 403 `{"error":"forbidden","code":403,"msg":"Nicht eingeloggt"}` without one.
- **(c) `kind` values. [FACT]** All 100 messages in `/inbox/comments` have `kind: "mention"`, and `type` is `comment`. Message keys are blocked, canReply, collection, created, flags, id, image, itemId, keyword, kind, mark, message, name, owner, ownerMark, preview, read, score, senderId, tag, thumb, type. **`image` is null in inbox messages**, so it can't be used for the video URL.
- **(d) Confirmed on the test post. [FACT]** `GET /api/items/get?id=7181444&flags=31` (logged in) returns 186 items. The entry with `id == 7181444` has `image = 2026/10/23/4891a7e031bc5868-h264-full_hd.mp4`. `https://vid.pr0gramm.com/<image>` returns 200, and the thumb-stem URL returns 404.
- **(e) Root cause confirmed. [FACT]** After restarting, the old bot picked up the test mention and logged `level=error msg="could not fetch song metdata" error="invalid status \"404 Not Found\": https://vid.pr0gramm.com/2026/10/23/4891a7e031bc5868.mp4"`. It sent no reply. **Every mention on a post using the new suffixed video names fails silently.** People are still tagging @Sauce: 100 mentions are in the inbox, several from today.
