package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/jigsmiths/dredge/internal/download"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/resolve"
	"github.com/jigsmiths/dredge/internal/spotify"
)

// ErrInvalid is a link that cannot be queued.
var ErrInvalid = errors.New("invalid link")

type Fetcher interface {
	Fetch(ctx context.Context, ref spotify.Ref) (*spotify.Collection, error)
}

type Library interface {
	resolve.Library
	download.Recorder
	Scan(ctx context.Context, l library.Layout) (library.ScanStats, error)
}

type Deps struct {
	Spotify  Fetcher
	Search   resolve.Searcher
	Source   download.Source
	Library  Library
	Layout   library.Layout
	Workers  int
	Attempts int
	Backoff  func(n int) time.Duration
	Poll     time.Duration // how often to look for queued jobs; 5s if zero
}

// Manager owns the queue. Jobs run one at a time, each with Workers tracks in
// parallel, so two playlists never compete for bandwidth.
type Manager struct {
	*Store
	deps Deps
	wake chan struct{}

	mu      sync.Mutex
	current int64
	stop    context.CancelFunc
	byUser  bool
}

func NewManager(s *Store, d Deps) *Manager {
	return &Manager{Store: s, deps: d, wake: make(chan struct{}, 1)}
}

// Submit queues a link. created is false when the link is already queued or
// running and that job is returned instead.
func (m *Manager) Submit(ctx context.Context, link string) (Job, bool, error) {
	ref, err := spotify.ParseURL(link)
	if err != nil {
		return Job{}, false, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	j, created, err := m.Create(ctx, "https://open.spotify.com/"+string(ref.Kind)+"/"+ref.ID)
	if created {
		m.poke()
	}
	return j, created, err
}

func (m *Manager) Retry(ctx context.Context, id int64) error {
	if err := m.Requeue(ctx, id); err != nil {
		return err
	}
	m.poke()
	return nil
}

// Stop cancels a queued job or interrupts the one running.
func (m *Manager) Stop(ctx context.Context, id int64) error {
	m.mu.Lock()
	if m.current == id && m.stop != nil {
		m.byUser = true
		m.stop()
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	return m.Cancel(ctx, id)
}

func (m *Manager) poke() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Run works through the queue until ctx ends. A job interrupted by shutdown
// stays running in the database and is queued again by the next Run.
func (m *Manager) Run(ctx context.Context) error {
	if err := m.Recover(ctx); err != nil {
		return err
	}
	poll := m.deps.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		for ctx.Err() == nil {
			j, err := m.Claim(ctx)
			if err != nil {
				slog.Error("claiming a job", "err", err)
				break
			}
			if j == nil {
				break
			}
			m.work(ctx, *j)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-m.wake:
		case <-tick.C:
		}
	}
}

func (m *Manager) work(root context.Context, j Job) {
	ctx, cancel := context.WithCancel(root)
	m.mu.Lock()
	m.current, m.stop, m.byUser = j.ID, cancel, false
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.current, m.stop = 0, nil
		m.mu.Unlock()
	}()

	err := m.process(ctx, j)

	m.mu.Lock()
	byUser := m.byUser
	m.mu.Unlock()
	// Record the outcome even when the run was cancelled.
	bg, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	switch {
	case root.Err() != nil && !byUser:
		return // shutting down; the job is queued again on the next start
	case byUser:
		err = m.SetStatus(bg, j.ID, Cancelled, "")
	case err != nil:
		slog.Warn("job failed", "id", j.ID, "url", j.URL, "err", err)
		err = m.SetStatus(bg, j.ID, Failed, err.Error())
	default:
		err = m.SetStatus(bg, j.ID, Done, "")
	}
	if err != nil {
		slog.Error("recording a job's outcome", "id", j.ID, "err", err)
	}
}

func (m *Manager) process(ctx context.Context, j Job) error {
	d, st := m.deps, m.Store
	ref, err := spotify.ParseURL(j.URL)
	if err != nil {
		return err
	}
	if _, err := d.Library.Scan(ctx, d.Layout); err != nil {
		return fmt.Errorf("scanning the library: %w", err)
	}
	col, err := d.Spotify.Fetch(ctx, ref)
	if err != nil {
		return err
	}
	if err := st.SetCollection(ctx, j.ID, string(col.Kind), col.Name); err != nil {
		return err
	}
	items := resolve.Resolve(ctx, col.Tracks, d.Library, d.Search, d.Workers)
	if err := ctx.Err(); err != nil {
		return err
	}

	rows := make([]Track, len(items))
	posByISRC := map[string]int{}
	for i, it := range items {
		rows[i] = m.row(i, it)
		if it.Status == resolve.Need {
			posByISRC[it.Track.ISRC] = i
		}
	}
	if err := st.ReplaceTracks(ctx, j.ID, rows); err != nil {
		return err
	}
	if err := st.SetStatus(ctx, j.ID, Running, ""); err != nil {
		return err
	}

	kind := library.Single
	if col.Kind == spotify.KindAlbum {
		kind = library.Album
	}
	set := func(pos int, status, path string, bytes int64, reason string) {
		if path != "" {
			path = m.rel(path)
		}
		if err := st.SetTrack(context.WithoutCancel(ctx), j.ID, pos, status, path, bytes, reason); err != nil {
			slog.Error("recording a track", "job", j.ID, "pos", pos, "err", err)
		}
	}
	results := download.Run(ctx, items, d.Source, d.Library, download.Options{
		Layout: d.Layout, Kind: kind, Workers: d.Workers, Attempts: d.Attempts, Backoff: d.Backoff,
		Started: func(it resolve.Item) { set(posByISRC[it.Track.ISRC], TrackDownloading, "", 0, "") },
		Progress: func(r download.Result) {
			if r.Item.Status != resolve.Need {
				return
			}
			status, reason := TrackFailed, ""
			switch {
			case r.Outcome == download.Downloaded, r.Outcome == download.Exists && r.Err == nil:
				status = TrackDone
			case r.Outcome == download.NotStocked:
				status = TrackNotStocked
			case r.Err != nil && ctx.Err() != nil:
				status = TrackPending // cut short, not failed
			case r.Err != nil:
				reason = r.Err.Error()
			}
			set(posByISRC[r.Item.Track.ISRC], status, r.Path, r.Bytes, reason)
		},
	})

	if col.Kind == spotify.KindPlaylist {
		if paths := download.Placed(items, results); len(paths) > 0 {
			if err := library.WriteM3U(d.Layout.PlaylistPath(col.Name, ref.ID), paths); err != nil {
				return fmt.Errorf("writing playlist: %w", err)
			}
		}
	}
	return ctx.Err()
}

func (m *Manager) rel(path string) string {
	if r, err := filepath.Rel(m.deps.Layout.Root, path); err == nil {
		return filepath.ToSlash(r)
	}
	return path
}

func (m *Manager) row(pos int, it resolve.Item) Track {
	t := Track{
		Pos: pos, Title: it.Track.Title, Artists: it.Track.Artists, Album: it.Track.Album,
		ISRC: it.Track.ISRC, Reason: it.Reason,
	}
	switch it.Status {
	case resolve.Need:
		t.Status = TrackPending
	case resolve.Have:
		t.Status, t.Path = TrackHave, m.rel(it.Path)
	case resolve.NoMatch:
		t.Status = TrackNoMatch
	case resolve.Ambiguous:
		t.Status = TrackAmbiguous
	case resolve.Duplicate:
		t.Status = TrackDuplicate
	default:
		t.Status = TrackFailed
	}
	return t
}
