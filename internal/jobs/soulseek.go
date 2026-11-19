package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jigsmiths/dredge/internal/flacmeta"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/slskd"
	"github.com/jigsmiths/dredge/internal/track"
)

// Soulseek fetches a wanted track the catalogue lacks. The returned file sits
// alone in its own folder, which is removed once the file is filed.
type Soulseek interface {
	Fetch(ctx context.Context, t track.Track, progress func(done, total int64)) (string, error)
}

// a Soulseek job is named after the wanted entry it fetches
const soulseekPrefix = "slskd:"

// queueSoulseek has a wanted track looked for on Soulseek. Only a track with a
// known length is: the length is what tells a fitting file from a wrong one.
func (m *Manager) queueSoulseek(ctx context.Context, w Wanted) {
	if m.deps.Soulseek == nil || w.DurationMS <= 0 || len(w.Artists) == 0 {
		return
	}
	_, created, err := m.Create(ctx, soulseekPrefix+strconv.FormatInt(w.ID, 10), &Seed{
		Kind: "track", Name: w.Artists[0] + " - " + w.Title + " (Soulseek)",
		Tracks: []Track{{Title: w.Title, Artists: w.Artists, Album: w.Album, ISRC: w.ISRC, Status: TrackPending}},
	})
	if err != nil {
		slog.Error("queueing a Soulseek job", "title", w.Title, "err", err)
		return
	}
	if created {
		slog.Info("soulseek job queued", "title", w.Title, "artist", w.Artists[0])
		m.poke()
	}
}

// soulseek runs a Soulseek job: one wanted track, fetched, checked, tagged
// from the wanted entry and filed as a single.
func (m *Manager) soulseek(ctx context.Context, j Job) error {
	d, st := m.deps, m.Store
	if d.Soulseek == nil {
		return errors.New("slskd is not set up")
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(j.URL, soulseekPrefix), 10, 64)
	if err != nil {
		return fmt.Errorf("bad Soulseek job: %w", err)
	}
	w, err := m.GetWanted(ctx, id)
	if errors.Is(err, ErrWantedNotFound) {
		return errors.New("the track is no longer wanted")
	}
	if err != nil {
		return err
	}
	if err := st.SetStatus(ctx, j.ID, Running, ""); err != nil {
		return err
	}
	started := time.Now()
	slog.Info("soulseek job started", "job", j.ID, "title", w.Title, "artist", w.Artists[0])
	// set records the track's status; a final one is also logged, at Warn when it failed
	set := func(status TrackStatus, path string, bytes int64, reason string) {
		if path != "" {
			path = d.Layout.Rel(path)
		}
		if status != TrackDownloading {
			outcome := string(status)
			if status == TrackPending {
				outcome = "stopped"
			}
			args := []any{"job", j.ID, "title", w.Title, "artist", w.Artists[0], "outcome", outcome,
				"took", time.Since(started).Round(time.Millisecond)}
			level := slog.LevelInfo
			switch status {
			case TrackDone, TrackHave:
				args = append(args, "path", path, "bytes", bytes)
			case TrackPending:
			case TrackFailed:
				level = slog.LevelWarn
				args = append(args, "err", reason)
			default:
				args = append(args, "reason", reason)
			}
			slog.Log(context.Background(), level, "soulseek track finished", args...)
		}
		if err := st.SetTrack(context.WithoutCancel(ctx), j.ID, 0, status, path, bytes, reason); err != nil {
			slog.Error("recording a track", "job", j.ID, "err", err)
		}
	}
	keep := func(reason string) {
		if err := m.CheckedWanted(context.WithoutCancel(ctx), w.ID, reason); err != nil {
			slog.Error("updating a wanted track", "title", w.Title, "err", err)
		}
	}
	done := func() {
		if err := m.DeleteWanted(context.WithoutCancel(ctx), w.ID); err != nil {
			slog.Error("removing a wanted track", "title", w.Title, "err", err)
		}
	}

	t := w.Track()
	if p, ok, err := m.had(ctx, t); err != nil {
		return err
	} else if ok {
		set(TrackHave, p, 0, "")
		done()
		return nil
	}

	set(TrackDownloading, "", 0, "")
	src, err := d.Soulseek.Fetch(ctx, t, func(got, total int64) {
		m.mu.Lock()
		m.live[0] = progress{got, total}
		m.mu.Unlock()
	})
	switch {
	case ctx.Err() != nil:
		set(TrackPending, "", 0, "")
		return ctx.Err()
	case errors.Is(err, slskd.ErrNoFile):
		set(TrackNoMatch, "", 0, "no FLAC on Soulseek fits")
		keep("no FLAC on Soulseek fits")
		return nil
	case err != nil:
		set(TrackFailed, "", 0, err.Error())
		keep("Soulseek: " + err.Error())
		return nil
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(src)) }()

	dst, size, err := m.file(ctx, t, src)
	if err != nil {
		set(TrackFailed, "", 0, err.Error())
		keep("Soulseek: " + err.Error())
		return nil
	}
	set(TrackDone, dst, size, "from Soulseek")
	done()
	return nil
}

// had is the library's file for t, when it has one.
func (m *Manager) had(ctx context.Context, t track.Track) (string, bool, error) {
	lib := m.deps.Library
	if _, err := lib.Scan(ctx, m.deps.Layout); err != nil {
		return "", false, fmt.Errorf("scanning the library: %w", err)
	}
	if t.ISRC != "" {
		if p, ok, err := lib.ByISRC(ctx, t.ISRC); err != nil || ok {
			return p, ok, err
		}
	}
	return lib.ByMeta(ctx, t.Title, t.Artists)
}

// file checks that src is a FLAC of t's length, then writes it with t's tags
// to its place among the singles and indexes it.
func (m *Manager) file(ctx context.Context, t track.Track, src string) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = in.Close() }()
	got, err := flacmeta.DurationMS(in)
	if err != nil {
		return "", 0, err
	}
	if diff := got - t.DurationMS; got == 0 || diff > match.DurationToleranceMS || diff < -match.DurationToleranceMS {
		return "", 0, fmt.Errorf("the file is %s long, wanted %s", ms(got), ms(t.DurationMS))
	}
	if _, err := in.Seek(0, 0); err != nil {
		return "", 0, err
	}

	dst := m.deps.Layout.Path(library.Single, t)
	if library.Exists(dst) {
		return "", 0, fmt.Errorf("a file is already at %s", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dredge-*.part")
	if err != nil {
		return "", 0, err
	}
	placed := false
	defer func() {
		if !placed {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	tags := flacmeta.Tags{Title: t.Title, Artists: t.Artists, Album: t.Album, AlbumArtist: t.Artists[0], ISRC: t.ISRC}
	if err := flacmeta.Rewrite(in, tmp, tags); err != nil {
		return "", 0, fmt.Errorf("tagging: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", 0, err
	}
	placed = true

	var size int64
	if fi, err := os.Stat(dst); err == nil {
		size = fi.Size()
	}
	entry := library.Entry{Path: dst, ISRC: t.ISRC, Title: t.Title, Artists: t.Artists}
	if err := m.deps.Library.Add(ctx, entry); err != nil {
		return "", 0, fmt.Errorf("filed at %s but not indexed: %w", dst, err)
	}
	return dst, size, nil
}

func ms(n int) time.Duration { return (time.Duration(n) * time.Millisecond).Round(time.Second) }
