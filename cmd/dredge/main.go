package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jigsmiths/dredge/internal/config"
	"github.com/jigsmiths/dredge/internal/db"
	"github.com/jigsmiths/dredge/internal/download"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/resolve"
	"github.com/jigsmiths/dredge/internal/spotify"
)

var version = "dev"

const usage = `usage: dredge <command> [flags]

  resolve <spotify-link>  list what is in the library and what is needed
  get <spotify-link>      download what is needed
  scan                    index the music library
  serve                   run the HTTP server
  version

flags (resolve, get, scan):
  --music DIR         library root (env MUSIC_DIR)
  --singles-dir NAME  folder for singles under the library root (env DREDGE_SINGLES_DIR)
  --workers N         parallel lookups and downloads (env WORKERS)
  --db PATH           index database (env DB_PATH)
  --attempts N        get: tries per track before giving up (default 3)
  --no-scan           resolve and get: do not refresh the index first`

type fetcher interface {
	Fetch(ctx context.Context, ref spotify.Ref) (*spotify.Collection, error)
}

// deps is everything a command needs from the outside world.
type deps struct {
	spotify fetcher
	search  resolve.Searcher
	source  download.Source
	index   *library.Index
	close   func()
}

type env struct {
	out, err io.Writer
	cfg      *config.Config
	open     func(*config.Config) (*deps, error)
}

// usageError is a mistake in how dredge was invoked.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func openReal(cfg *config.Config) (*deps, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, err
	}
	conn, err := db.Connect(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", cfg.DBPath, err)
	}
	mono := monochrome.New("", "", nil)
	return &deps{
		spotify: spotify.New("", "", nil),
		search:  mono,
		source:  mono,
		index:   library.NewIndex(conn),
		close:   func() { _ = conn.Close() },
	}, nil
}

func run(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return usageError{usage}
	}
	switch args[0] {
	case "resolve":
		return resolveCommand(ctx, e, args[1:])
	case "get":
		return getCommand(ctx, e, args[1:])
	case "scan":
		return scanCommand(ctx, e, args[1:])
	case "serve":
		return serve(ctx, e.cfg)
	case "version":
		_, err := fmt.Fprintln(e.out, "dredge", version)
		return err
	}
	return usageError{fmt.Sprintf("unknown command %q\n\n%s", args[0], usage)}
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	e := &env{out: os.Stdout, err: os.Stderr, cfg: config.Load(), open: openReal}
	if err := run(ctx, e, os.Args[1:]); err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			say(os.Stderr, "%s\n", ue.msg)
			os.Exit(2)
		}
		say(os.Stderr, "dredge: %v\n", err)
		os.Exit(1)
	}
}
