# dredge

[![ci](https://github.com/jigsmiths/dredge/actions/workflows/ci.yml/badge.svg)](https://github.com/jigsmiths/dredge/actions/workflows/ci.yml)

Download singles, albums and Spotify playlists as tagged FLAC into a music library, without a browser. A Go CLI, plus a server with a job queue, an HTTP API and a small web page to watch downloads.

**Status:** `resolve`, `get`, `scan` and `serve` work. Spotify and Monochrome are unofficial APIs and can change without notice.

## Usage

```sh
dredge resolve <spotify-link>   # list each track: have, need, no_match
dredge get <spotify-link>       # download what is needed
dredge search heart barracuda   # find a track by name, numbered
dredge search heart barracuda --get 2   # download result 2
dredge scan                     # index the library
dredge serve                    # API and web page on :8080
```

Links can be a playlist, an album or a track. To fetch one song by name, `search` lists the downloadable recordings and `--get N` downloads the one you pick; nothing is chosen for you. A picked track goes to `Singles/` like MusicGrabber's, and dredge reads its release from Monochrome to tag the album, album artist, date, disc and track number, so Navidrome groups it properly. If that lookup fails, the track is still downloaded with the basic tags. `get` never overwrites a file and skips what the library already has, so rerunning it resumes. Ctrl-C stops cleanly and leaves no partial files.

Flags: `--music DIR`, `--singles-dir NAME`, `--workers N`, `--attempts N`, `--db PATH`, `--no-scan`.

| Env | Default | |
|---|---|---|
| `MUSIC_DIR` | `/music` | library root |
| `DREDGE_SINGLES_DIR` | `Singles` | singles folder under the root; `-` for none |
| `WORKERS` | `4` | parallel lookups and downloads |
| `PORT` | `8080` | `serve` listen port |
| `DREDGE_API_KEY` | unset | when set, `/api` needs it in an `X-Api-Key` header |
| `DB_PATH` | per-user config dir | index database (the image sets `/data/dredge.db`) |

## The library

dredge fits a library that MusicGrabber, Lidarr and Navidrome already share.

- Playlist and single tracks go to `Singles/Artist/Title.flac`, as MusicGrabber files them.
- Albums go to `Artist/Album/01 Title.flac`.
- A playlist also gets `Singles/<name> (Spotify <id>).m3u` with relative paths, which Navidrome imports. Rerunning updates it.
- `scan` reads FLAC, MP3, M4A and Ogg tags into a SQLite index of ISRC, title and artists. Opus files and untagged files under `Singles/` are indexed from their path. Hidden folders are skipped.
- A track counts as already there when an ISRC matches, or the title and an artist match. Live and remix versions count as different tracks.

Files are written 0644 in 0755 folders. Run as the user that owns the library (uid 1000 on Altair). `get` refuses to run if the music dir does not exist, so an unmounted volume is never filled by accident.

## Server and API

`dredge serve` queues links and works through them in the background, one job at a time with `WORKERS` tracks in parallel. Progress is kept in the database, so a restart picks up where it stopped. The web page at `/` takes a Spotify link or a song name (it lists results to pick from) and lists running jobs and history; click a job to see what is downloading, what is still to do and what passed.

| | |
|---|---|
| `GET /api/search?q=` | downloadable recordings for a query, one per ISRC: `isrc`, `title`, `artists`, `duration_ms` |
| `POST /api/jobs` | `{"url": "<spotify link>"}` or a picked result `{"isrc", "title", "artists"}`; 202 and the job, or 200 and the existing job if that link is already queued or running; 400 for a bad link |
| `GET /api/jobs` | newest first, with per-status counts; `?status=done` filters |
| `GET /api/jobs/{id}` | the job and every track with its status |
| `POST /api/jobs/{id}/retry` | run a finished job again; it skips what is now in the library |
| `POST /api/jobs/{id}/cancel` | dequeue, or stop a running job and leave the rest undone |
| `DELETE /api/jobs/{id}` | forget a finished job; files are never touched |
| `POST /api/scan` | re-index the library |
| `GET /health` | liveness |

Job statuses are `queued`, `resolving`, `running`, `done`, `partial` (finished, but a track failed; retry it), `failed` (the link could not be fetched) and `cancelled`. Track statuses are `pending`, `downloading`, `done`, `have`, `duplicate`, `not_stocked`, `lossy_only` (Monochrome only has it as AAC), `no_match`, `ambiguous` and `failed`. A `done` job can still hold tracks that were not fetched, such as `no_match`; the counts say how many.

With `DREDGE_API_KEY` set, send `X-Api-Key` on `/api` calls; the web page asks for it once. `/health` and the page itself stay open. Without a key every `/api` route is open, so keep dredge behind your own auth or on a private network. `serve` refuses to start if the music dir does not exist.

## How it works

1. Spotify: an anonymous token from the embed page, then the web player's GraphQL API for playlists, albums and tracks of any length.
2. Monochrome: searched by artist and title; its results carry the ISRC and list only downloadable tracks. Matching needs the same title, a shared artist and a length within 5s.
3. The FLAC is streamed through a tagger (title, artists, album, album artist, track, disc, date, ISRC, cover) into a temp file, then renamed into place. Transient failures are retried.

Monochrome serves whatever quality it has, often 24-bit, so tracks are 25 to 95 MB.

## Development

Needs [just](https://github.com/casey/just), Go and [golangci-lint](https://golangci-lint.run/). Node 24 is only needed to build `web/`.

```sh
just setup          # once: point git at the repo hooks
just check          # gofmt, lint and tests, as CI and the pre-commit hook run them
just live           # smoke-test the real Spotify and Monochrome (network)
just build          # builds web/ then the binary
```

Commits follow [conventional commits](https://www.conventionalcommits.org/) and are enforced by the hooks. After CI passes on `main`, the release workflow tags the next version with `svu` and publishes binaries and `ghcr.io/jigsmiths/dredge` with GoReleaser.

## License

MIT
