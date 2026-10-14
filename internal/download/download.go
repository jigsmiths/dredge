// Package download fetches the tracks that resolve marked as needed, tags
// them and files them in the library.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/jigsmiths/dredge/internal/flacmeta"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/pool"
	"github.com/jigsmiths/dredge/internal/resolve"
)

type Source interface {
	Find(ctx context.Context, isrc, artist, title string) (monochrome.Track, error)
	Download(ctx context.Context, t monochrome.Track, w io.Writer) (int64, error)
	Artwork(ctx context.Context, t monochrome.Track) ([]byte, error)
}

type Recorder interface {
	Add(ctx context.Context, e library.Entry) error
}

type Outcome int

const (
	Downloaded Outcome = iota
	Exists             // a file is already at the destination; left alone
	NotStocked         // the catalogue does not have the recording
	Skipped            // nothing to download for this item
	Failed
)

func (o Outcome) String() string {
	switch o {
	case Downloaded:
		return "downloaded"
	case Exists:
		return "exists"
	case NotStocked:
		return "not_stocked"
	case Skipped:
		return "skipped"
	}
	return "failed"
}

type Result struct {
	Item    resolve.Item
	Outcome Outcome
	Path    string
	Bytes   int64
	Err     error
}

type Options struct {
	Layout   library.Layout
	Kind     library.Kind
	Workers  int
	Progress func(Result) // called once per item, one at a time
}

// Run downloads the Need items with at most Workers at once and returns a
// result per item, in order. One item failing never affects the others.
func Run(ctx context.Context, items []resolve.Item, src Source, rec Recorder, opt Options) []Result {
	var place, progress sync.Mutex
	report := func(r Result) {
		if opt.Progress != nil {
			progress.Lock()
			defer progress.Unlock()
			opt.Progress(r)
		}
	}
	res := pool.Map(ctx, opt.Workers, items, func(ctx context.Context, it resolve.Item) (Result, error) {
		r := one(ctx, it, src, rec, opt, &place)
		report(r)
		return r, nil
	})
	out := make([]Result, len(items))
	for i, r := range res {
		out[i] = r.Value
		if r.Err != nil { // never started: the context ended first
			out[i] = Result{Item: items[i], Outcome: Failed, Err: r.Err}
			report(out[i])
		}
	}
	return out
}

func one(ctx context.Context, it resolve.Item, src Source, rec Recorder, opt Options, place *sync.Mutex) Result {
	if it.Status != resolve.Need {
		return Result{Item: it, Outcome: Skipped, Path: it.Path}
	}
	fail := func(format string, args ...any) Result {
		return Result{Item: it, Outcome: Failed, Err: fmt.Errorf(format, args...)}
	}

	dst := opt.Layout.Path(opt.Kind, it.Track)
	if exists(dst) {
		return Result{Item: it, Outcome: Exists, Path: dst}
	}
	var artist string
	if len(it.Track.Artists) > 0 {
		artist = it.Track.Artists[0]
	}
	mono, err := src.Find(ctx, it.Track.ISRC, artist, it.Track.Title)
	if errors.Is(err, monochrome.ErrNotStocked) {
		return Result{Item: it, Outcome: NotStocked}
	}
	if err != nil {
		return fail("find: %w", err)
	}
	cover, _ := src.Artwork(ctx, mono) // a missing cover is not worth failing for

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fail("%w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dredge-*.part")
	if err != nil {
		return fail("%w", err)
	}
	placed := false
	defer func() {
		if !placed {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	if err := stream(ctx, src, mono, tmp, tagsFor(it, cover)); err != nil {
		return fail("download: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fail("%w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fail("%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fail("%w", err)
	}

	place.Lock()
	if exists(dst) {
		place.Unlock()
		return Result{Item: it, Outcome: Exists, Path: dst}
	}
	err = os.Rename(tmp.Name(), dst)
	place.Unlock()
	if err != nil {
		return fail("%w", err)
	}
	placed = true

	var size int64
	if fi, err := os.Stat(dst); err == nil {
		size = fi.Size()
	}
	r := Result{Item: it, Outcome: Downloaded, Path: dst, Bytes: size}
	entry := library.Entry{
		Path: dst, ISRC: it.Track.ISRC, Title: it.Track.Title,
		Artists: append(append([]string{}, it.Track.Artists...), it.Track.AlbumArtist),
	}
	if err := rec.Add(ctx, entry); err != nil {
		r.Outcome, r.Err = Failed, fmt.Errorf("downloaded to %s but not indexed: %w", dst, err)
	}
	return r
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func tagsFor(it resolve.Item, cover []byte) flacmeta.Tags {
	t := it.Track
	albumArtist := t.AlbumArtist
	if albumArtist == "" && len(t.Artists) > 0 {
		albumArtist = t.Artists[0]
	}
	return flacmeta.Tags{
		Title: t.Title, Artists: t.Artists, Album: t.Album, AlbumArtist: albumArtist,
		TrackNo: t.TrackNo, DiscNo: t.DiscNo, Date: t.Date, ISRC: t.ISRC, Cover: cover,
	}
}

// stream pipes the download through the tagger into w, so the audio is
// written once and never held in memory.
func stream(ctx context.Context, src Source, mono monochrome.Track, w io.Writer, tags flacmeta.Tags) error {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := src.Download(ctx, mono, pw)
		_ = pw.CloseWithError(err)
		done <- err
	}()
	rerr := flacmeta.Rewrite(pr, w, tags)
	_ = pr.CloseWithError(rerr) // frees the downloader if the tagger stopped first
	if derr := <-done; derr != nil {
		return derr
	}
	return rerr
}
