package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jigsmiths/dredge/internal/download"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/resolve"
	"github.com/jigsmiths/dredge/internal/spotify"
	"github.com/jigsmiths/dredge/internal/track"
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
	Details  resolve.Detailer // optional: albums for tracks picked from search
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
	live    map[int]progress // by track position, for the current job
}

type progress struct{ done, total int64 }

// Live adds the bytes received so far to the downloading tracks of the job.
func (m *Manager) Live(id int64, ts []Track) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != id {
		return
	}
	for i := range ts {
		if p, ok := m.live[ts[i].Pos]; ok && ts[i].Status == TrackDownloading {
			ts[i].BytesDone, ts[i].BytesTotal = p.done, max(p.total, 0)
		}
	}
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
	j, created, err := m.Create(ctx, "https://open.spotify.com/"+string(ref.Kind)+"/"+ref.ID, nil)
	if created {
		m.poke()
	}
	return j, created, err
}

var isrcRe = regexp.MustCompile(`^[A-Z0-9]{12}$`)

const pickedPrefix = "isrc:"

// SubmitTrack queues one recording the user picked from search results.
func (m *Manager) SubmitTrack(ctx context.Context, isrc, title string, artists []string) (Job, bool, error) {
	isrc, title = strings.ToUpper(strings.TrimSpace(isrc)), strings.TrimSpace(title)
	var names []string
	for _, a := range artists {
		if a = strings.TrimSpace(a); a != "" {
			names = append(names, a)
		}
	}
	switch {
	case !isrcRe.MatchString(isrc):
		return Job{}, false, fmt.Errorf("%w: %q is not an ISRC", ErrInvalid, isrc)
	case title == "" || len(names) == 0:
		return Job{}, false, fmt.Errorf("%w: a title and an artist are needed", ErrInvalid)
	}
	j, created, err := m.Create(ctx, pickedPrefix+isrc, &Seed{
		Kind: "track", Name: names[0] + " - " + title,
		Tracks: []Track{{Title: title, Artists: names, ISRC: isrc, Status: TrackPending}},
	})
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
	m.current, m.stop, m.byUser, m.live = j.ID, cancel, false, map[int]progress{}
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
	case byUser && err != nil: // a stop that came after the job finished leaves it done
		err = m.SetStatus(bg, j.ID, Cancelled, "")
	case err != nil:
		slog.Warn("job failed", "id", j.ID, "url", j.URL, "err", err)
		err = m.SetStatus(bg, j.ID, Failed, err.Error())
	default:
		err = m.SetStatus(bg, j.ID, m.finishedStatus(bg, j.ID), "")
	}
	if err != nil {
		slog.Error("recording a job's outcome", "id", j.ID, "err", err)
	}
}

// finishedStatus is Partial when any track failed, otherwise Done.
func (m *Manager) finishedStatus(ctx context.Context, id int64) Status {
	j, err := m.Get(ctx, id)
	if err != nil {
		slog.Error("counting failed tracks", "id", id, "err", err)
		return Done
	}
	if j.Counts[TrackFailed] > 0 {
		return Partial
	}
	return Done
}

func (m *Manager) process(ctx context.Context, j Job) error {
	d, st := m.deps, m.Store
	if _, err := d.Library.Scan(ctx, d.Layout); err != nil {
		return fmt.Errorf("scanning the library: %w", err)
	}

	var (
		items []resolve.Item
		kind  = library.Single
		list  *spotify.Ref // set for a playlist, which also gets an M3U
		name  string
	)
	if _, picked := strings.CutPrefix(j.URL, pickedPrefix); picked {
		rows, err := st.Tracks(ctx, j.ID)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return errors.New("a picked track job has lost its track")
		}
		t, err := resolve.Enrich(ctx, track.Track{Title: rows[0].Title, Artists: rows[0].Artists, ISRC: rows[0].ISRC}, d.Details)
		if err != nil && ctx.Err() == nil && !errors.Is(err, monochrome.ErrNotStocked) {
			slog.Warn("no album details", "isrc", rows[0].ISRC, "err", err)
		}
		it, err := resolve.Direct(ctx, t, d.Library)
		if err != nil {
			return err
		}
		items = []resolve.Item{it}
	} else {
		ref, err := spotify.ParseURL(j.URL)
		if err != nil {
			return err
		}
		col, err := d.Spotify.Fetch(ctx, ref)
		if err != nil {
			return err
		}
		if err := st.SetCollection(ctx, j.ID, string(col.Kind), col.Name); err != nil {
			return err
		}
		if err := st.ReplaceTracks(ctx, j.ID, nil); err != nil {
			return err
		}
		items = resolve.Resolve(ctx, col.Tracks, d.Library, d.Search, d.Workers, func(it resolve.Item) {
			if err := st.PutTrack(context.WithoutCancel(ctx), j.ID, m.row(it)); err != nil {
				slog.Error("recording a track", "job", j.ID, "pos", it.Pos, "err", err)
			}
		})
		kind = library.KindFor(col.Kind == spotify.KindAlbum)
		if col.Kind == spotify.KindPlaylist {
			list, name = &ref, col.Name
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	rows := make([]Track, len(items))
	for i, it := range items {
		rows[i] = m.row(it)
	}
	if err := st.ReplaceTracks(ctx, j.ID, rows); err != nil {
		return err
	}
	if err := st.SetStatus(ctx, j.ID, Running, ""); err != nil {
		return err
	}

	set := func(pos int, status TrackStatus, path string, bytes int64, reason string) {
		if path != "" {
			path = d.Layout.Rel(path)
		}
		if err := st.SetTrack(context.WithoutCancel(ctx), j.ID, pos, status, path, bytes, reason); err != nil {
			slog.Error("recording a track", "job", j.ID, "pos", pos, "err", err)
		}
	}
	results := download.Run(ctx, items, d.Source, d.Library, download.Options{
		Layout: d.Layout, Kind: kind, Workers: d.Workers, Attempts: d.Attempts, Backoff: d.Backoff,
		Started: func(it resolve.Item) { set(it.Pos, TrackDownloading, "", 0, "") },
		Bytes: func(it resolve.Item, done, total int64) {
			m.mu.Lock()
			m.live[it.Pos] = progress{done, total}
			m.mu.Unlock()
		},
		Progress: func(r download.Result) {
			if r.Item.Status != resolve.Need {
				return
			}
			status, reason := trackStatus(r, ctx.Err() != nil)
			set(r.Item.Pos, status, r.Path, r.Bytes, reason)
		},
	})

	if list != nil {
		if paths := download.Placed(items, results); len(paths) > 0 {
			if err := library.WriteM3U(d.Layout.PlaylistPath(name, list.ID), paths); err != nil {
				return fmt.Errorf("writing playlist: %w", err)
			}
		}
	}
	return ctx.Err()
}

// trackStatus is how a download result shows on its track row. A failure that
// came from the run being cancelled leaves the track pending.
func trackStatus(r download.Result, cancelled bool) (status TrackStatus, reason string) {
	switch {
	case r.Outcome == download.Downloaded, r.Outcome == download.Exists:
		return TrackDone, ""
	case r.Outcome == download.NotStocked:
		return TrackNotStocked, ""
	case r.Outcome == download.LossyOnly:
		return TrackLossyOnly, ""
	case r.Err != nil && cancelled:
		return TrackPending, ""
	case r.Err != nil:
		return TrackFailed, r.Err.Error()
	}
	return TrackFailed, ""
}

func (m *Manager) row(it resolve.Item) Track {
	t := Track{
		Pos: it.Pos, Title: it.Track.Title, Artists: it.Track.Artists, Album: it.Track.Album,
		ISRC: it.Track.ISRC, Reason: it.Reason,
	}
	switch it.Status {
	case resolve.Need:
		t.Status = TrackPending
	case resolve.Have:
		t.Status, t.Path = TrackHave, m.deps.Layout.Rel(it.Path)
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
