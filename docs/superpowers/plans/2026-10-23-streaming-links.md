# Streaming Links Follow-up Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the dead song.link API (`401 PUBLIC_API_ACCESS_DEPRECATED`, retired 2026-07-31) with streaming links that need no key: Apple Music (direct, from Shazam), Deezer (exact, by ISRC), a link to the song.link **web page**, and Spotify and YouTube **search** links.

**Architecture:** The bot stores `apple_music_url`, `isrc` and `deezer_url` per song. The API derives three more links per song (`songLinkUrl`, `spotifySearchUrl`, `youtubeSearchUrl`), and the web song page renders the buttons. `internal/odesli` is deleted.

**Tech Stack:** unchanged (Go 1.27 + pgx/sqlc/goose, Next.js 16.4.3).

**Spec:** `docs/superpowers/specs/2026-10-23-pr0sauce-monorepo-design.md`, §5.3 ("Links") and §6.2 (song page). The decisions below were agreed in chat on 2026-10-23 and supersede "Links keep using Odesli". Live-verified facts:
- Shazam returns only Apple Music links plus `track.isrc`. There are no Spotify or YouTube links, and `hub.providers` is empty.
- `GET https://api.deezer.com/track/isrc:TCAFX2160415` → `{"title":"Kumite (From \"Bloodsport\")","link":"https://www.deezer.com/track/1568632112",…}`. No key needed. An unknown ISRC returns HTTP 200 with `{"error":{…}}`.
- `https://song.link/i/1597618301` (the Apple track id) returns a 200 web page with Deezer/Tidal/Amazon/Pandora. song.link itself no longer has Spotify or YouTube URLs.

## Global Constraints

- Every commit is authored as `Pacerino <public@pa0x43.me>`. No work identity, no Co-Authored-By. Check with `git var GIT_AUTHOR_IDENT` before committing.
- No new API keys and no new external accounts.
- The schema change is a **new** migration `migrations/00002_streaming_links.sql`. Production is at version 1, so never edit `00001`.
- Legacy columns `spotify_url`, `spotify_id`, `youtube_url` and `youtube_id` stay. New songs leave them NULL.
- The bot's reply texts and tags on pr0gramm are unchanged. Links appear only on the website.
- Every external URL rendered on the web goes through the existing `safeHttpUrl`.
- German UI labels exactly: `Spotify`, `Auf Spotify suchen`, `Apple Music`, `Deezer`, `YouTube`, `Auf YouTube suchen`, `Alle Plattformen`.

## Review Focus

1. **Deezer answers 200 with `{"error":…}` for an unknown ISRC.** That must mean "no link", not an error and not a broken URL. Test: `TestDeezer_UnknownISRCIsNotFound` (Task 1).
2. **Shazam match without ISRC, or Deezer down/slow.** The song is still stored and replied to, with no Deezer link and no retry. Test: `TestProcess_DeezerFailureStillReplies` (Task 2).
3. **Apple URL without an `i=` track id** (an album link, or a malformed one). `songLinkUrl` is null, not `song.link/i/`. Test: `TestSongLinkURL_NoTrackID` (Task 3).
4. **Titles or artists with `&`, `/`, `?`, `#` or Cyrillic** in the Spotify and YouTube search links must be fully encoded. Tests: `TestSpotifySearchURL_Escaping` and `TestYouTubeSearchURL_Escaping` (Task 3).
5. **Deploy order.** Migration 00002 must run before the new bot and api. Covered by the compose `depends_on: migrate` and verified in Task 5 Step 2.

---

### Task 1: Migration, queries, recognize ISRC, Deezer client

**Files:**
- Create: `migrations/00002_streaming_links.sql`, `internal/deezer/deezer.go`, `internal/deezer/deezer_test.go`
- Modify: `internal/db/queries/songs.sql` (UpsertSong), regenerate `internal/db/*.sql.go` with `go tool sqlc generate`
- Modify: `internal/recognize/shazam.go` (Result.ISRC) plus its tests
**Interfaces:**
- Produces:
  - columns `songs.apple_music_url text`, `songs.isrc text`, `songs.deezer_url text` (all nullable; goose Down drops them)
  - `UpsertSong` gains params `AppleMusicUrl`, `Isrc`, `DeezerUrl` (all `*string`)
  - `recognize.Result.ISRC string`, taken from `track.isrc`
  - `deezer.New(httpClient *http.Client, baseURL string) *Client`, where an empty baseURL means `https://api.deezer.com` and a nil client means a 10 s timeout
  - `(*Client).TrackURL(ctx, isrc string) (string, error)` returns `("", deezer.ErrNotFound)` for an empty isrc or an `{"error":…}` body

- [ ] **Step 1: Write the failing tests.**
  - `TestDeezer_FoundReturnsLink`: the server serves the verified Kumite body. Expect `https://www.deezer.com/track/1568632112`, and that the request path is `/track/isrc:TCAFX2160415`.
  - `TestDeezer_UnknownISRCIsNotFound`: the server serves 200 `{"error":{"type":"DataException","message":"no data","code":800}}`. Expect `errors.Is(err, deezer.ErrNotFound)` and `""`.
  - `TestDeezer_EmptyISRCNoRequest`: no request is made.
  - `TestDeezer_Non2xxIsError`.
  - `TestRecognize_ParsesKumiteFixture` (extended): `res.ISRC == "TCAFX2160415"`.
  - `TestUpsertSong_StoresStreamingLinks` (internal/db): a round-trip of the 3 new columns.
- [ ] **Step 2:** Run `go test ./internal/deezer/ ./internal/recognize/ ./internal/db/` and expect FAIL.
- [ ] **Step 3:** Implement. Deezer: GET `{base}/track/isrc:{url.PathEscape(isrc)}`, and decode `{link string, error *struct{}}`. `link` must start with `https://www.deezer.com/`, otherwise return ErrNotFound. Read at most 1 MiB of the body.
- [ ] **Step 4:** Run `go test -count=1 ./... && go vet ./...` and expect PASS. `internal/odesli` is untouched in this task, so everything still compiles.
- [ ] **Step 5:** Commit with `feat(links): deezer ISRC client, store apple/isrc/deezer`.

### Task 2: Bot uses Deezer instead of song.link

**Files:**
- Modify: `internal/bot/ports.go`, `internal/bot/bot.go` (~L260-280, the hit path), `internal/bot/run.go:95`, `internal/bot/fakes_test.go`, `internal/bot/bot_test.go`
- Delete: `internal/odesli/` (the package and its tests). After this task nothing imports it.

**Interfaces:**
- Consumes: Task 1's `deezer.Client.TrackURL`, `recognize.Result.ISRC/AppleMusicURL` and the `UpsertSong` params.
- Produces: the `Linker` port becomes `DeezerURL(ctx context.Context, isrc string) (string, error)`. `run.go` wires `deezer.New(nil, "")`.

- [ ] **Step 1: Write the failing tests.**
  - `TestProcess_HitStoresAppleIsrcDeezer`: the fake recognizer returns AppleMusicURL `https://music.apple.com/gb/album/x/1597617995?i=1597618301` and ISRC `TCAFX2160415`. The fake linker returns `https://www.deezer.com/track/1568632112`. Assert that the stored song has all three, plus spotify_url and youtube_url NULL.
  - `TestProcess_DeezerFailureStillReplies`: the linker returns an error. Assert the comment is posted once, the status is done, deezer_url is NULL, a WARN is logged, and the attempts are unchanged.
  - Update the existing hit test, whose comment text is unchanged.
- [ ] **Step 2:** Run `go test ./internal/bot/` and expect FAIL.
- [ ] **Step 3:** Implement. On a hit with a non-empty ISRC, call `DeezerURL`. `ErrNotFound` → NULL silently. Any other error → WARN with item, and NULL. Then `UpsertSong` with `AppleMusicUrl = res.AppleMusicURL` (NULL if empty), `Isrc`, and `DeezerUrl`. Misses store none of these.
- [ ] **Step 4:** Run `go test -count=1 ./... && go vet ./...` and expect PASS.
- [ ] **Step 5:** Commit with `feat(bot): deezer links instead of song.link API`.

### Task 3: API exposes the new links

**Files:**
- Create: `internal/api/links.go`, `internal/api/links_test.go`
- Modify: `internal/api/songs.go` (songJSON plus the mapping), `internal/api/api_test.go`

**Interfaces:**
- Produces new `Song` JSON fields, all `string|null`:
  - `appleMusicUrl`
  - `deezerUrl`
  - `songLinkUrl` = `"https://song.link/i/" + <i param>` when the Apple URL parses and has a numeric `i` query param, otherwise null
  - `spotifySearchUrl` = `"https://open.spotify.com/search/" + url.PathEscape(artist + " " + title)` for every hit (legacy songs too)
  - `youtubeSearchUrl` = `"https://www.youtube.com/results?search_query=" + url.QueryEscape(artist + " " + title)` for every hit
- Helpers in links.go: `songLinkURL(apple *string) *string`, `spotifySearchURL(artist, title string) string`, `youtubeSearchURL(artist, title string) string`.

- [ ] **Step 1: Write the failing tests.**
  - `TestSongLinkURL`: the Kumite Apple URL → `https://song.link/i/1597618301`.
  - `TestSongLinkURL_NoTrackID`: `…/album/x/1597617995` without `i`, `i=abc`, nil, and `"::"` → nil.
  - `TestSpotifySearchURL_Escaping`: artist `AC/DC`, title `Rock & Roll Ain't Noise Pollution?` → contains no raw `&`, `/` (after `/search/`), `?` or `#`, and round-trips via `url.PathUnescape` to `AC/DC Rock & Roll Ain't Noise Pollution?`. Also Cyrillic `Новая патриотическая`.
  - `TestYouTubeSearchURL_Escaping`: the same inputs. The query value round-trips via `url.ParseQuery`, and the URL has exactly one `?` and no raw `&` beyond the query start.
  - `TestGetSong_IncludesStreamingLinks`: a seeded song with Apple and Deezer has all five new fields. A legacy song with only spotify_url has appleMusicUrl, deezerUrl and songLinkUrl null, and spotifySearchUrl and youtubeSearchUrl set.
- [ ] **Step 2:** Run `go test ./internal/api/` and expect FAIL.
- [ ] **Step 3:** Implement. The list endpoints include the same fields, for consistency.
- [ ] **Step 4:** Run `go test -count=1 ./internal/api/ && go vet ./...` and expect PASS.
- [ ] **Step 5:** Commit with `feat(api): apple music, deezer, song.link and search links`.

### Task 4: Web song page buttons

**Files:**
- Modify: `web/lib/types.ts` (Song gets the 5 fields), `web/app/[id]/page.tsx` (the buttons), `web/lib/api.test.ts` or a new `web/lib/links.test.ts` if helpers are added

**Behavior:** The button row, left to right. Each button is shown only if its URL passes `safeHttpUrl`, and all open with `target="_blank" rel="noopener noreferrer"`.
1. `Spotify` → `spotifyUrl` (legacy exact link). If it's absent, `Auf Spotify suchen` → `spotifySearchUrl`. Spotify green.
2. `Apple Music` → `appleMusicUrl`. Colour `#fa243c`.
3. `Deezer` → `deezerUrl`. Colour `#a238ff`.
4. `YouTube` → `youtubeUrl` (legacy exact link). If it's absent, `Auf YouTube suchen` → `youtubeSearchUrl`. Red, as today.
5. `Alle Plattformen` → `songLinkUrl`. Neutral surface colour with the accent border.

- [ ] **Step 1: Write the failing test.** Extract `streamingButtons(song: Song): {label: string, href: string}[]` into `web/lib/links.ts` and test it:
  - a legacy song with spotifyUrl+youtubeUrl → `[Spotify, YouTube]`, plus neither search button is added
  - a new song with apple+deezer+songLink+both search URLs → `[Auf Spotify suchen, Apple Music, Deezer, Auf YouTube suchen, Alle Plattformen]`
  - a `javascript:` deezerUrl → dropped
- [ ] **Step 2:** Run `cd web && npx vitest run` and expect FAIL.
- [ ] **Step 3:** Implement, and render from `streamingButtons` in the song page.
- [ ] **Step 4:** Run `npx vitest run && npm run build` and expect PASS.
- [ ] **Step 5:** Commit with `feat(web): apple music, deezer, song.link and search buttons`.

### Task 5: Deploy [OWNER OK]

- [ ] **Step 1:** Push, and wait until CI is green for the new SHA (all 4 jobs, images published).
- [ ] **Step 2: [OWNER OK]** On `$PROD_HOST`:
  1. In `/opt/pr0sauce/v2`, set `TAG=sha-<new>` in `.env`.
  2. Run `docker compose pull -q`.
  3. Run `docker compose run --rm migrate`. Expect goose `OK 00002_streaming_links.sql`.
  4. Run `docker compose up -d bot api web`.
  5. Run `docker compose ps`. Expect all services healthy.
- [ ] **Step 3: Verify.**
  - `docker exec traefik wget -qO- --no-check-certificate --header 'Host: pr0sauce.info' https://127.0.0.1/6796145` contains `Spotify` and `YouTube`. Kumite has legacy exact links for both.
  - A real hit recognized after the deploy has `apple_music_url` and `isrc` set in `songs`, and `deezer_url` set when Deezer knows the ISRC.
  - The owner opens one new song page in the browser.
- [ ] **Step 4:** Update the spec §5.3 "Links" paragraph to describe Apple Music + Deezer + song.link page + Spotify and YouTube search (song.link API retired). Commit with `docs: streaming links after song.link API retirement`.
