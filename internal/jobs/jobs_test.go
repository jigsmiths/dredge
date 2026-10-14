package jobs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jigsmiths/dredge/internal/db"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/spotify"
	"github.com/jigsmiths/dredge/internal/track"
)

const (
	plID  = "37i9dQZF1DWXRqgorJj26U"
	plID2 = "37i9dQZF1EIfFW8AEFKKOD"
)

func link(id string) string { return "https://open.spotify.com/playlist/" + id }

type fakeSpotify struct {
	mu   sync.Mutex
	cols map[string]*spotify.Collection
	err  error
	seen []string
}

func (f *fakeSpotify) Fetch(_ context.Context, ref spotify.Ref) (*spotify.Collection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, ref.ID)
	if f.err != nil {
		return nil, f.err
	}
	return f.cols[ref.ID], nil
}

type fakeSearch struct{ results map[string][]match.Candidate }

func (s *fakeSearch) Search(_ context.Context, q string, _ int) ([]match.Candidate, error) {
	return s.results[q], nil
}

type fakeSource struct {
	mu      sync.Mutex
	stocked map[string]bool
	stall   chan struct{} // downloads wait for this to close or the context to end
	started chan string
}

func (s *fakeSource) Find(_ context.Context, isrc, _, title string) (monochrome.Track, error) {
	s.mu.Lock()
	ok := s.stocked[isrc]
	s.mu.Unlock()
	if !ok {
		return monochrome.Track{}, monochrome.ErrNotStocked
	}
	return monochrome.Track{ID: isrc, ISRC: isrc, Title: title}, nil
}

func (s *fakeSource) Download(ctx context.Context, t monochrome.Track, w io.Writer) (int64, error) {
	monochrome.ReportProgress(ctx, 10, 100)
	if s.started != nil {
		s.started <- t.ISRC
	}
	if s.stall != nil {
		select {
		case <-s.stall:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	b := append([]byte("fLaC"), 0x80, 0, 0, 34)
	b = append(b, bytes.Repeat([]byte{9}, 34)...)
	b = append(b, "AUDIO"...)
	_, err := w.Write(b)
	return int64(len(b)), err
}

func (s *fakeSource) Artwork(context.Context, monochrome.Track) ([]byte, error) { return nil, nil }

func tr(title, artist string) track.Track {
	return track.Track{Title: title, Artists: []string{artist}, Album: "Album", AlbumArtist: artist, TrackNo: 1, DiscNo: 1, DurationMS: 200000}
}

func cand(isrc, title, artist string) match.Candidate {
	return match.Candidate{ISRC: isrc, Title: title, Artists: []string{artist}, Album: "Album", DurationMS: 200000}
}

type rig struct {
	t    *testing.T
	root string
	spot *fakeSpotify
	src  *fakeSource
	m    *Manager
}

func newRig(t *testing.T) *rig {
	t.Helper()
	root := t.TempDir()
	conn, err := db.Connect(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	r := &rig{t: t, root: root}
	r.spot = &fakeSpotify{cols: map[string]*spotify.Collection{
		plID: {Kind: spotify.KindPlaylist, Name: "Roadtrip", Tracks: []track.Track{
			tr("Barracuda", "Heart"), tr("Uprising", "Muse"), tr("Unknown", "Nobody"),
		}},
		plID2: {Kind: spotify.KindPlaylist, Name: "Second", Tracks: []track.Track{tr("Barracuda", "Heart")}},
	}}
	search := &fakeSearch{results: map[string][]match.Candidate{
		"Heart barracuda": {cand("ISRC1", "Barracuda", "Heart")},
		"Muse uprising":   {cand("ISRC2", "Uprising", "Muse")},
	}}
	r.src = &fakeSource{stocked: map[string]bool{"ISRC1": true}}
	r.m = NewManager(NewStore(conn), Deps{
		Spotify: r.spot, Search: search, Source: r.src, Library: library.NewIndex(conn),
		Layout:  library.Layout{Root: root, SinglesDir: "Singles"},
		Workers: 2, Attempts: 1, Poll: 20 * time.Millisecond,
	})
	return r
}

func (r *rig) start() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.m.Run(ctx); close(done) }()
	r.t.Cleanup(func() { cancel(); <-done })
	return cancel
}

func (r *rig) submit(id string) Job {
	r.t.Helper()
	j, _, err := r.m.Submit(context.Background(), link(id))
	if err != nil {
		r.t.Fatal(err)
	}
	return j
}

func (r *rig) waitFor(id int64, want ...Status) Job {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, err := r.m.Get(context.Background(), id)
		if err != nil {
			r.t.Fatal(err)
		}
		for _, w := range want {
			if j.Status == w {
				return j
			}
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("job %d is %s, wanted %v", id, j.Status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *rig) tracks(id int64) map[string]Track {
	r.t.Helper()
	ts, err := r.m.Tracks(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string]Track{}
	for _, t := range ts {
		out[t.Title] = t
	}
	return out
}

func TestSubmitNormalisesTheLinkAndReusesAnActiveJob(t *testing.T) {
	r := newRig(t)
	a, created, err := r.m.Submit(context.Background(), link(plID)+"?si=abc")
	if err != nil || !created || a.URL != link(plID) || a.Status != Queued {
		t.Fatalf("a = %+v created=%v err=%v", a, created, err)
	}
	b, created, err := r.m.Submit(context.Background(), "spotify:playlist:"+plID)
	if err != nil || created || b.ID != a.ID {
		t.Errorf("b = %+v created=%v err=%v, want the first job", b, created, err)
	}
}

func TestSubmitRejectsLinksThatAreNotSpotify(t *testing.T) {
	r := newRig(t)
	_, _, err := r.m.Submit(context.Background(), "https://example.com/x")
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestRunsAJobAndRecordsEveryTrack(t *testing.T) {
	r := newRig(t)
	r.start()
	j := r.waitFor(r.submit(plID).ID, Done, Failed)
	if j.Status != Done || j.Name != "Roadtrip" || j.Kind != "playlist" || j.StartedAt == nil || j.FinishedAt == nil {
		t.Fatalf("job = %+v", j)
	}
	ts := r.tracks(j.ID)
	if ts["Barracuda"].Status != TrackDone || ts["Barracuda"].Path != "Singles/Heart/Barracuda.flac" || ts["Barracuda"].Bytes == 0 {
		t.Errorf("Barracuda = %+v", ts["Barracuda"])
	}
	if ts["Uprising"].Status != TrackNotStocked || ts["Unknown"].Status != TrackNoMatch {
		t.Errorf("Uprising = %+v, Unknown = %+v", ts["Uprising"], ts["Unknown"])
	}
	if j.Total != 3 || j.Counts[TrackDone] != 1 || j.Counts[TrackNotStocked] != 1 || j.Counts[TrackNoMatch] != 1 {
		t.Errorf("counts = %v total %d", j.Counts, j.Total)
	}
	for _, p := range []string{"Singles/Heart/Barracuda.flac", "Singles/Roadtrip (Spotify " + plID + ").m3u"} {
		if _, err := os.Stat(filepath.Join(r.root, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
}

func TestTracksShowAsDownloadingWhileTheyRun(t *testing.T) {
	r := newRig(t)
	r.src.stall = make(chan struct{})
	r.src.started = make(chan string, 4)
	r.start()
	j := r.submit(plID)
	<-r.src.started
	j = r.waitFor(j.ID, Running)
	if ts := r.tracks(j.ID); ts["Barracuda"].Status != TrackDownloading || ts["Unknown"].Status != TrackNoMatch {
		t.Errorf("while running: %+v", ts)
	}
	close(r.src.stall)
	r.waitFor(j.ID, Done)
}

func TestLiveProgressShowsOnDownloadingTracksOnly(t *testing.T) {
	r := newRig(t)
	r.src.stall = make(chan struct{})
	r.src.started = make(chan string, 4)
	r.start()
	j := r.submit(plID)
	<-r.src.started
	j = r.waitFor(j.ID, Running)
	rows, err := r.m.Tracks(context.Background(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	r.m.Live(j.ID, rows)
	var live int
	for _, tk := range rows {
		if tk.BytesTotal == 100 && tk.BytesDone == 10 && tk.Status == TrackDownloading {
			live++
		} else if tk.BytesDone != 0 {
			t.Errorf("%s has progress but is %s", tk.Title, tk.Status)
		}
	}
	if live == 0 {
		t.Errorf("no downloading track shows progress: %+v", rows)
	}
	close(r.src.stall)
	r.waitFor(j.ID, Done)
}

func TestJobsRunOneAtATimeInOrder(t *testing.T) {
	r := newRig(t)
	first := r.submit(plID)
	second := r.submit(plID2)
	r.start()
	r.waitFor(second.ID, Done)
	if a := r.waitFor(first.ID, Done); a.FinishedAt.After(r.mustGet(second.ID).StartedAt.Add(time.Second)) {
		t.Errorf("first finished after second started: %+v", a)
	}
	if len(r.spot.seen) != 2 || r.spot.seen[0] != plID || r.spot.seen[1] != plID2 {
		t.Errorf("fetched %v", r.spot.seen)
	}
}

func (r *rig) mustGet(id int64) Job {
	j, err := r.m.Get(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return j
}

func TestAFailedFetchFailsTheJobWithTheReason(t *testing.T) {
	r := newRig(t)
	r.spot.err = errors.New("spotify down")
	r.start()
	j := r.waitFor(r.submit(plID).ID, Failed)
	if j.Error != "spotify down" {
		t.Errorf("error = %q", j.Error)
	}
}

func TestRetryRunsAgainAndPicksUpWhatIsNowAvailable(t *testing.T) {
	r := newRig(t)
	r.start()
	j := r.waitFor(r.submit(plID).ID, Done)
	r.src.mu.Lock()
	r.src.stocked["ISRC2"] = true
	r.src.mu.Unlock()
	if err := r.m.Retry(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.tracks(j.ID)["Uprising"].Status != TrackDone {
		if time.Now().After(deadline) {
			t.Fatalf("Uprising = %+v", r.tracks(j.ID)["Uprising"])
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.tracks(j.ID)["Barracuda"].Status != TrackHave {
		t.Errorf("Barracuda = %+v, want have (already in the library)", r.tracks(j.ID)["Barracuda"])
	}
}

func TestRetryOfAJobThatIsStillActiveIsAConflict(t *testing.T) {
	r := newRig(t)
	j := r.submit(plID)
	if err := r.m.Retry(context.Background(), j.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v", err)
	}
	if err := r.m.Retry(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestStoppingARunningJobLeavesTheRestToDo(t *testing.T) {
	r := newRig(t)
	r.src.stall = make(chan struct{})
	r.src.started = make(chan string, 4)
	r.start()
	j := r.submit(plID)
	<-r.src.started
	r.waitFor(j.ID, Running)
	if err := r.m.Stop(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	j = r.waitFor(j.ID, Cancelled)
	if ts := r.tracks(j.ID); ts["Barracuda"].Status != TrackPending {
		t.Errorf("Barracuda = %+v, want pending", ts["Barracuda"])
	}
}

func TestStoppingAQueuedJobDequeuesIt(t *testing.T) {
	r := newRig(t)
	j := r.submit(plID)
	if err := r.m.Stop(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.mustGet(j.ID); got.Status != Cancelled || got.FinishedAt == nil {
		t.Errorf("job = %+v", got)
	}
	if err := r.m.Stop(context.Background(), j.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("second stop err = %v", err)
	}
}

func TestAJobLeftRunningByACrashIsQueuedAgain(t *testing.T) {
	r := newRig(t)
	j := r.submit(plID)
	if err := r.m.SetStatus(context.Background(), j.ID, Running, ""); err != nil {
		t.Fatal(err)
	}
	r.start()
	r.waitFor(j.ID, Done)
}

func TestShutdownMidJobDoesNotRecordItAsFinished(t *testing.T) {
	r := newRig(t)
	r.src.stall = make(chan struct{})
	r.src.started = make(chan string, 4)
	stop := r.start()
	j := r.submit(plID)
	<-r.src.started
	stop()
	time.Sleep(100 * time.Millisecond)
	if got := r.mustGet(j.ID); got.Status.active() == false {
		t.Errorf("status = %s, want still active so it resumes", got.Status)
	}
}

func TestDeleteOnlyRemovesFinishedJobs(t *testing.T) {
	r := newRig(t)
	j := r.submit(plID)
	if err := r.m.Delete(context.Background(), j.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("active delete err = %v", err)
	}
	r.start()
	r.waitFor(j.ID, Done)
	if err := r.m.Delete(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Get(context.Background(), j.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if ts, _ := r.m.Tracks(context.Background(), j.ID); len(ts) != 0 {
		t.Errorf("%d tracks left behind", len(ts))
	}
	if _, err := os.Stat(filepath.Join(r.root, "Singles/Heart/Barracuda.flac")); err != nil {
		t.Errorf("deleting the record removed a file: %v", err)
	}
}

func TestListIsNewestFirstAndFiltersByStatus(t *testing.T) {
	r := newRig(t)
	a, b := r.submit(plID), r.submit(plID2)
	if err := r.m.Stop(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	all, err := r.m.List(context.Background(), "")
	if err != nil || len(all) != 2 || all[0].ID != b.ID {
		t.Fatalf("all = %+v err=%v", all, err)
	}
	only, _ := r.m.List(context.Background(), string(Cancelled))
	if len(only) != 1 || only[0].ID != a.ID {
		t.Errorf("cancelled = %+v", only)
	}
}
