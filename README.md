# dredge

[![ci](https://github.com/jigsmiths/dredge/actions/workflows/ci.yml/badge.svg)](https://github.com/jigsmiths/dredge/actions/workflows/ci.yml)

Download singles, albums and Spotify playlists as tagged FLAC into a music library, without a browser. A Go CLI; the HTTP server is a stub for now.

**Status:** `resolve`, `get` and `scan` work. Spotify and Monochrome are unofficial APIs and can change without notice.

## Usage

```sh
dredge resolve <spotify-link>   # list each track: have, need, no_match
dredge get <spotify-link>       # download what is needed
dredge scan                     # index the library
```

Links can be a playlist, an album or a track. `get` never overwrites a file and skips what the library already has, so rerunning it resumes. Ctrl-C stops cleanly and leaves no partial files.

Flags: `--music DIR`, `--singles-dir NAME`, `--workers N`, `--attempts N`, `--db PATH`, `--no-scan`.

| Env | Default | |
|---|---|---|
| `MUSIC_DIR` | `/music` | library root |
| `DREDGE_SINGLES_DIR` | `Singles` | singles folder under the root; `-` for none |
| `WORKERS` | `4` | parallel lookups and downloads |
| `DB_PATH` | per-user config dir | index database (the image sets `/data/dredge.db`) |

## The library

dredge fits a library that MusicGrabber, Lidarr and Navidrome already share.

- Playlist and single tracks go to `Singles/Artist/Title.flac`, as MusicGrabber files them.
- Albums go to `Artist/Album/01 Title.flac`.
- A playlist also gets `Singles/<name> (Spotify <id>).m3u` with relative paths, which Navidrome imports. Rerunning updates it.
- `scan` reads FLAC, MP3, M4A and Ogg tags into a SQLite index of ISRC, title and artists. Opus files and untagged files under `Singles/` are indexed from their path. Hidden folders are skipped.
- A track counts as already there when an ISRC matches, or the title and an artist match. Live and remix versions count as different tracks.

Files are written 0644 in 0755 folders. Run as the user that owns the library (uid 1000 on Altair). `get` refuses to run if the music dir does not exist, so an unmounted volume is never filled by accident.

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
