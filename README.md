# dredge

[![ci](https://github.com/jigsmiths/dredge/actions/workflows/ci.yml/badge.svg)](https://github.com/jigsmiths/dredge/actions/workflows/ci.yml)

Download singles, albums and Spotify playlists as FLAC. A Go CLI and HTTP API with a SQLite-backed job queue.

**Status:** scaffold. Nothing works yet.

## Plan

- Resolve Spotify playlists and albums without a browser (embed token plus the web player's GraphQL API).
- Match tracks to Monochrome by ISRC (via Deezer) and download the FLAC.
- Skip tracks already in the library, checked by ISRC and then by normalised artist, title and duration.
- Download concurrently in the background, with per-track status and retry stored in SQLite.

## Development

Needs [just](https://github.com/casey/just), Go and [golangci-lint](https://golangci-lint.run/). Node 24 is only needed to build `web/`.

```sh
just setup          # once: point git at the repo hooks
just check          # gofmt, lint and tests, as CI and the pre-commit hook run them
just test
just lint
just fmt
just build          # builds web/ then the binary
just web-dev        # Vite dev server, proxies /api and /health to :8080
just version        # current and next version
```

Commits follow [conventional commits](https://www.conventionalcommits.org/) and are enforced by the hooks. After CI passes on `main`, the release workflow tags the next version with `svu` and publishes binaries and `ghcr.io/jigsmiths/dredge` with GoReleaser.

## License

MIT
