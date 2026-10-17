package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jigsmiths/dredge/internal/config"
	"github.com/jigsmiths/dredge/internal/download"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/resolve"
	"github.com/jigsmiths/dredge/internal/spotify"
)

type options struct {
	cfg      config.Config
	noScan   bool
	attempts int
	pick     int      // search: the result to download
	args     []string // positional arguments
}

// parseFlags reads flags and positional arguments in any order.
func parseFlags(name string, e *env, args []string) (*options, error) {
	o := &options{cfg: *e.cfg}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.cfg.MusicDir, "music", o.cfg.MusicDir, "")
	fs.StringVar(&o.cfg.SinglesDir, "singles-dir", o.cfg.SinglesDir, "")
	fs.IntVar(&o.cfg.Workers, "workers", o.cfg.Workers, "")
	fs.StringVar(&o.cfg.DBPath, "db", o.cfg.DBPath, "")
	fs.BoolVar(&o.noScan, "no-scan", false, "")
	fs.IntVar(&o.attempts, "attempts", download.DefaultAttempts, "")
	fs.IntVar(&o.pick, "get", 0, "")
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usageError{fmt.Sprintf("%s: %v\n\n%s", name, err, usage)}
		}
		if fs.NArg() == 0 {
			break
		}
		o.args = append(o.args, fs.Arg(0))
		args = fs.Args()[1:]
	}
	o.cfg.SinglesDir = config.SinglesDir(o.cfg.SinglesDir)
	if o.cfg.Workers < 1 {
		return nil, usageError{"--workers must be at least 1"}
	}
	if o.attempts < 1 {
		return nil, usageError{"--attempts must be at least 1"}
	}
	return o, nil
}

func (o *options) layout() library.Layout {
	return library.Layout{Root: o.cfg.MusicDir, SinglesDir: o.cfg.SinglesDir}
}

// link is the single Spotify link a command was given.
func (o *options) link() (spotify.Ref, error) {
	switch len(o.args) {
	case 0:
		return spotify.Ref{}, usageError{"expected a Spotify link"}
	case 1:
		return spotify.ParseURL(o.args[0])
	}
	return spotify.Ref{}, usageError{fmt.Sprintf("expected one link, got %d", len(o.args))}
}

// requireMusicDir refuses to download somewhere that may not be the library.
func requireMusicDir(dir string) error {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("music dir %q does not exist; refusing to download somewhere that may not be the library", dir)
	}
	return nil
}

// say writes to the terminal; a failed write has nothing useful to do.
func say(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func scanLibrary(ctx context.Context, e *env, d *deps, o *options) error {
	st, err := d.index.Scan(ctx, o.layout())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.err, "scanned %s: %s, %d new, %d removed, %d unreadable\n",
		o.cfg.MusicDir, plural(st.Files, "file"), st.Indexed, st.Removed, st.Unreadable)
	return err
}

func scanCommand(ctx context.Context, e *env, args []string) error {
	o, err := parseFlags("scan", e, args)
	if err != nil {
		return err
	}
	d, err := e.open(&o.cfg)
	if err != nil {
		return err
	}
	defer d.close()
	st, err := d.index.Scan(ctx, o.layout())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.out, "%s, %d new, %d unchanged, %d removed, %d unreadable\n",
		plural(st.Files, "file"), st.Indexed, st.Unchanged, st.Removed, st.Unreadable)
	return err
}

// session is a link resolved against the library.
type session struct {
	d      *deps
	o      *options
	ref    spotify.Ref
	col    *spotify.Collection
	items  []resolve.Item
	layout library.Layout
}

func resolveCommand(ctx context.Context, e *env, args []string) error {
	_, closeFn, err := prepare(ctx, e, "resolve", false, args)
	if err != nil {
		return err
	}
	closeFn()
	return nil
}

func getCommand(ctx context.Context, e *env, args []string) error {
	s, closeFn, err := prepare(ctx, e, "get", true, args)
	if err != nil {
		return err
	}
	defer closeFn()
	return s.get(ctx, e)
}

// prepare parses the command line, resolves the link against the library and
// lists the result. It insists on an existing music dir when it will download.
func prepare(ctx context.Context, e *env, command string, download bool, args []string) (*session, func(), error) {
	o, err := parseFlags(command, e, args)
	if err != nil {
		return nil, nil, err
	}
	ref, err := o.link()
	if err != nil {
		return nil, nil, err
	}
	if download {
		if err := requireMusicDir(o.cfg.MusicDir); err != nil {
			return nil, nil, err
		}
	}
	d, err := e.open(&o.cfg)
	if err != nil {
		return nil, nil, err
	}
	s := &session{d: d, o: o, ref: ref, layout: o.layout()}
	fail := func(err error) (*session, func(), error) { d.close(); return nil, nil, err }

	if !o.noScan {
		if err := scanLibrary(ctx, e, d, o); err != nil {
			return fail(err)
		}
	}
	col, err := d.spotify.Fetch(ctx, ref)
	if err != nil {
		return fail(err)
	}
	s.col = col
	s.items = resolve.Resolve(ctx, col.Tracks, d.index, d.search, o.cfg.Workers)
	s.list(e)
	return s, d.close, nil
}

func display(it resolve.Item) string {
	artist := ""
	if len(it.Track.Artists) > 0 {
		artist = it.Track.Artists[0]
	}
	return artist + " - " + it.Track.Title
}

func (s *session) list(e *env) {
	say(e.out, "%s (%s, %s)\n", s.col.Name, s.col.Kind, plural(len(s.col.Tracks), "track"))
	if s.col.Skipped > 0 {
		say(e.out, "%s are not tracks and were skipped\n", plural(s.col.Skipped, "item"))
	}
	counts := map[resolve.Status]int{}
	for _, it := range s.items {
		counts[it.Status]++
		var detail string
		switch it.Status {
		case resolve.Need:
			detail = "  [" + it.Track.ISRC + "]"
		case resolve.Have:
			detail = "  (" + s.layout.Rel(it.Path) + ")"
		default:
			if it.Reason != "" {
				detail = "  (" + it.Reason + ")"
			}
		}
		say(e.out, "%-10s %s%s\n", it.Status, display(it), detail)
	}
	var parts []string
	for _, c := range []struct {
		st   resolve.Status
		text string
	}{
		{resolve.Need, "need"}, {resolve.Have, "have"}, {resolve.NoMatch, "no match"},
		{resolve.Ambiguous, "ambiguous"}, {resolve.Duplicate, "duplicate"}, {resolve.Failed, "failed"},
	} {
		if n := counts[c.st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, c.text))
		}
	}
	say(e.out, "%s: %s\n", plural(len(s.items), "track"), strings.Join(parts, ", "))
}

func (s *session) get(ctx context.Context, e *env) error {
	var need int
	for _, it := range s.items {
		if it.Status == resolve.Need {
			need++
		}
	}
	kind := library.KindFor(s.col.Kind == spotify.KindAlbum)

	var results []download.Result
	if need == 0 {
		say(e.out, "nothing to download\n")
	} else {
		say(e.out, "downloading %s\n", plural(need, "track"))
		results = download.Run(ctx, s.items, s.d.source, s.d.index, download.Options{
			Layout: s.layout, Kind: kind, Workers: s.o.cfg.Workers, Attempts: s.o.attempts,
			Progress: func(r download.Result) { progress(e, s, r) },
		})
	}

	counts := map[download.Outcome]int{}
	var interrupted int
	for _, r := range results {
		if r.Outcome == download.Failed && stopped(r.Err) {
			interrupted++
			continue
		}
		counts[r.Outcome]++
	}
	failed := counts[download.Failed]
	for _, it := range s.items {
		if it.Status == resolve.Failed {
			failed++
		}
	}
	var parts []string
	for _, c := range []struct {
		o    download.Outcome
		text string
	}{
		{download.Downloaded, "downloaded"}, {download.Exists, "already existed"},
		{download.NotStocked, "not on Monochrome"}, {download.LossyOnly, "only lossy on Monochrome"}, {download.Failed, "failed"},
	} {
		if n := counts[c.o]; n > 0 || c.o == download.Downloaded && need > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, c.text))
		}
	}
	if len(parts) > 0 {
		say(e.out, "%s\n", strings.Join(parts, ", "))
	}
	if interrupted > 0 {
		say(e.out, "interrupted: %s not downloaded\n", plural(interrupted, "track"))
	}

	if s.col.Kind == spotify.KindPlaylist {
		if err := s.writePlaylist(e, results); err != nil {
			return err
		}
	}
	if interrupted > 0 {
		return errors.New("interrupted")
	}
	if failed > 0 {
		return fmt.Errorf("%s failed", plural(failed, "track"))
	}
	return nil
}

func progress(e *env, s *session, r download.Result) {
	var what, detail string
	switch r.Outcome {
	case download.Downloaded:
		what, detail = "downloaded", "  ("+size(r.Bytes)+")"
	case download.Exists:
		what = "exists"
	case download.NotStocked:
		what = "not on Monochrome"
	case download.LossyOnly:
		what = "only lossy on Monochrome"
	case download.Failed:
		if stopped(r.Err) {
			return // reported once as an interruption
		}
		what = "failed"
		if r.Err != nil {
			detail = "  (" + r.Err.Error() + ")"
		}
	default:
		return
	}
	say(e.out, "%-18s %s%s\n", what, display(r.Item), detail)
}

// stopped is true for a download that ended because the run was cancelled.
func stopped(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// writePlaylist writes an M3U of every track of the playlist that is in the
// library, in playlist order, where Navidrome will pick it up.
func (s *session) writePlaylist(e *env, results []download.Result) error {
	paths := download.Placed(s.items, results)
	if len(paths) == 0 {
		return nil
	}
	pl := s.layout.PlaylistPath(s.col.Name, s.ref.ID)
	if err := library.WriteM3U(pl, paths); err != nil {
		return fmt.Errorf("writing playlist: %w", err)
	}
	_, err := fmt.Fprintf(e.out, "playlist: %s (%s)\n", s.layout.Rel(pl), plural(len(paths), "track"))
	return err
}
