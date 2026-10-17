package download

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhowden/tag"

	"github.com/jigsmiths/dredge/internal/flacmeta"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/resolve"
	"github.com/jigsmiths/dredge/internal/track"
)

const audio = "AUDIO-FRAMES-0123456789"

func flacFile() []byte {
	b := append([]byte("fLaC"), 0x80, 0, 0, 34)
	b = append(b, bytes.Repeat([]byte{3}, 34)...)
	return append(b, audio...)
}

type fakeSource struct {
	mu        sync.Mutex
	stocked   map[string]bool // ISRC -> in the catalogue
	findErr   error
	downloads int
	running   atomic.Int32
	peak      atomic.Int32
	hold      time.Duration
	body      func(w io.Writer) error // nil writes a valid FLAC
	art       []byte
	artErr    error
}

func (s *fakeSource) Find(_ context.Context, isrc, artist, title string) (monochrome.Track, error) {
	if s.findErr != nil {
		return monochrome.Track{}, s.findErr
	}
	if !s.stocked[isrc] {
		return monochrome.Track{}, monochrome.ErrNotStocked
	}
	return monochrome.Track{ID: "id-" + isrc, ISRC: isrc, Title: title, ArtworkURL: "x"}, nil
}

func (s *fakeSource) Download(ctx context.Context, _ monochrome.Track, w io.Writer) (int64, error) {
	s.mu.Lock()
	s.downloads++
	s.mu.Unlock()
	n := s.running.Add(1)
	defer s.running.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if s.hold > 0 {
		select {
		case <-time.After(s.hold):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if s.body != nil {
		return 0, s.body(w)
	}
	b := flacFile()
	_, err := w.Write(b)
	monochrome.ReportProgress(ctx, int64(len(b)), int64(len(b)))
	return int64(len(b)), err
}

func (s *fakeSource) Artwork(context.Context, monochrome.Track) ([]byte, error) {
	return s.art, s.artErr
}

type fakeRecorder struct {
	mu      sync.Mutex
	entries []library.Entry
	err     error
}

func (r *fakeRecorder) Add(_ context.Context, e library.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return r.err
}

func need(isrc, title, artist string) resolve.Item {
	return resolve.Item{
		Status: resolve.Need,
		Track: track.Track{
			Title: title, Artists: []string{artist}, Album: "Little Queen", AlbumArtist: artist,
			TrackNo: 1, DiscNo: 1, Date: "1977-05-14", ISRC: isrc,
		},
	}
}

func setup(t *testing.T) (Options, string) {
	root := t.TempDir()
	return Options{Layout: library.Layout{Root: root, SinglesDir: "Singles"}, Workers: 4, Backoff: noBackoff}, root
}

func files(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func TestADifferentRecordingAtThePathIsAFailureNotExists(t *testing.T) {
	opt, root := setup(t)
	dst := filepath.Join(root, "Singles", "Heart", "Barracuda.flac")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	var tagged bytes.Buffer
	if err := flacmeta.Rewrite(bytes.NewReader(flacFile()), &tagged, flacmeta.Tags{Title: "Barracuda", ISRC: "OTHER0000001"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, tagged.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{stocked: map[string]bool{"USSM10009075": true}}
	res := Run(context.Background(), []resolve.Item{need("USSM10009075", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Failed || res[0].Err == nil {
		t.Fatalf("result = %+v, want a failure naming the other recording", res[0])
	}
	if b, _ := os.ReadFile(dst); !bytes.Equal(b, tagged.Bytes()) {
		t.Error("the existing file was changed")
	}
}

func TestAFoundTrackIsDownloadedWithoutSearchingAgain(t *testing.T) {
	opt, root := setup(t)
	src := &fakeSource{findErr: errors.New("Find must not be called")}
	it := need("USSM10009075", "Barracuda", "Heart")
	it.Found = &monochrome.Track{ID: "11", ISRC: "USSM10009075", Title: "Barracuda"}
	res := Run(context.Background(), []resolve.Item{it}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Downloaded || res[0].Err != nil {
		t.Fatalf("result = %+v", res[0])
	}
	if got := files(t, root); len(got) != 1 {
		t.Errorf("files = %v", got)
	}
}

func TestDownloadsTagsAndFilesATrack(t *testing.T) {
	opt, root := setup(t)
	src := &fakeSource{stocked: map[string]bool{"USSM10009075": true}, art: []byte("\xff\xd8\xffjpeg")}
	rec := &fakeRecorder{}
	res := Run(context.Background(), []resolve.Item{need("USSM10009075", "Barracuda", "Heart")}, src, rec, opt)

	want := filepath.Join(root, "Singles", "Heart", "Barracuda.flac")
	if res[0].Outcome != Downloaded || res[0].Path != want || res[0].Err != nil {
		t.Fatalf("result = %+v, want downloaded to %s", res[0], want)
	}
	if got := files(t, root); len(got) != 1 || got[0] != "Singles/Heart/Barracuda.flac" {
		t.Fatalf("files = %v, want just the track and no leftovers", got)
	}
	b, _ := os.ReadFile(want)
	if !bytes.HasSuffix(b, []byte(audio)) {
		t.Error("the audio was altered")
	}
	tags, err := flacmeta.Read(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if tags.Title != "Barracuda" || tags.Album != "Little Queen" || tags.AlbumArtist != "Heart" || tags.ISRC != "USSM10009075" ||
		tags.TrackNo != 1 || tags.DiscNo != 1 || tags.Date != "1977-05-14" || len(tags.Artists) != 1 || tags.Artists[0] != "Heart" {
		t.Errorf("tags = %+v", tags)
	}
	m, err := tag.ReadFrom(bytes.NewReader(b))
	if err != nil || m.Picture() == nil {
		t.Errorf("cover missing: %v", err)
	}
	if len(rec.entries) != 1 || rec.entries[0].Path != want || rec.entries[0].ISRC != "USSM10009075" || rec.entries[0].Title != "Barracuda" {
		t.Errorf("recorded = %+v", rec.entries)
	}
	if fi, _ := os.Stat(want); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", fi.Mode().Perm())
	}
	if res[0].Bytes != sizeOf(want) {
		t.Errorf("bytes = %d, want the file size %d", res[0].Bytes, sizeOf(want))
	}
}

func TestBytesCallbackNamesTheTrack(t *testing.T) {
	opt, _ := setup(t)
	var got []string
	var done, total int64
	opt.Bytes = func(it resolve.Item, d, tot int64) { got, done, total = append(got, it.Track.ISRC), d, tot }
	src := &fakeSource{stocked: map[string]bool{"USSM10009075": true}}
	Run(context.Background(), []resolve.Item{need("USSM10009075", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if len(got) != 1 || got[0] != "USSM10009075" || done == 0 || done != total {
		t.Fatalf("got=%v done=%d total=%d", got, done, total)
	}
}

func sizeOf(p string) int64 {
	s, _ := os.Stat(p)
	return s.Size()
}

func TestAlbumKindFilesUnderArtistAndAlbum(t *testing.T) {
	opt, root := setup(t)
	opt.Kind = library.Album
	src := &fakeSource{stocked: map[string]bool{"I": true}}
	res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Downloaded || filepath.ToSlash(res[0].Path[len(root)+1:]) != "Heart/Little Queen/01 Barracuda.flac" {
		t.Errorf("result = %+v", res[0])
	}
}

func TestMissingArtworkDoesNotFailTheDownload(t *testing.T) {
	opt, _ := setup(t)
	src := &fakeSource{stocked: map[string]bool{"I": true}, artErr: errors.New("cdn down")}
	res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Downloaded {
		t.Fatalf("result = %+v", res[0])
	}
	b, _ := os.ReadFile(res[0].Path)
	if m, err := tag.ReadFrom(bytes.NewReader(b)); err != nil || m.Picture() != nil {
		t.Errorf("expected a tagged file without a cover (err %v)", err)
	}
}

func TestALossyOnlyTrackIsNamedAndNotRetried(t *testing.T) {
	opt, root := setup(t)
	opt.Attempts, opt.Backoff = 3, func(int) time.Duration { return 0 }
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: func(io.Writer) error { return monochrome.ErrLossyOnly }}
	res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != LossyOnly || src.downloads != 1 || len(files(t, root)) != 0 {
		t.Errorf("result = %+v, downloads = %d, files = %v", res[0], src.downloads, files(t, root))
	}
}

func TestOnlyNeededItemsAreDownloaded(t *testing.T) {
	opt, root := setup(t)
	src := &fakeSource{stocked: map[string]bool{"I": true}}
	items := []resolve.Item{
		{Status: resolve.Have, Path: "/music/x.flac", Track: track.Track{Title: "A"}},
		{Status: resolve.NoMatch, Track: track.Track{Title: "B"}},
		{Status: resolve.Ambiguous, Track: track.Track{Title: "C"}},
		{Status: resolve.Duplicate, Track: track.Track{Title: "D"}},
		{Status: resolve.Failed, Track: track.Track{Title: "E"}},
		need("I", "Barracuda", "Heart"),
	}
	res := Run(context.Background(), items, src, &fakeRecorder{}, opt)
	for i := range 5 {
		if res[i].Outcome != Skipped {
			t.Errorf("item %d outcome = %v, want skipped", i, res[i].Outcome)
		}
	}
	if res[0].Path != "/music/x.flac" {
		t.Errorf("a skipped have lost its path: %+v", res[0])
	}
	if res[5].Outcome != Downloaded || src.downloads != 1 || len(files(t, root)) != 1 {
		t.Errorf("last = %+v, downloads = %d", res[5], src.downloads)
	}
}

func TestAnExistingFileIsNeverOverwritten(t *testing.T) {
	opt, root := setup(t)
	dst := filepath.Join(root, "Singles", "Heart", "Barracuda.flac")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{stocked: map[string]bool{"I": true}}
	res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Exists || res[0].Path != dst {
		t.Fatalf("result = %+v", res[0])
	}
	if b, _ := os.ReadFile(dst); string(b) != "precious" {
		t.Errorf("the existing file was changed to %q", b)
	}
	if src.downloads != 0 {
		t.Error("downloaded a track whose file already exists")
	}
}

func TestNotInTheCatalogue(t *testing.T) {
	opt, root := setup(t)
	res := Run(context.Background(), []resolve.Item{need("NOPE", "Barracuda", "Heart")}, &fakeSource{}, &fakeRecorder{}, opt)
	if res[0].Outcome != NotStocked || res[0].Err != nil {
		t.Errorf("result = %+v", res[0])
	}
	if got := files(t, root); len(got) != 0 {
		t.Errorf("files = %v", got)
	}
}

func TestSearchTroubleIsAFailureNotAMiss(t *testing.T) {
	opt, _ := setup(t)
	src := &fakeSource{findErr: errors.New("monochrome: search: HTTP 502")}
	res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Failed || res[0].Err == nil {
		t.Errorf("result = %+v", res[0])
	}
}

func TestAFailedDownloadLeavesNothingBehind(t *testing.T) {
	for name, body := range map[string]func(io.Writer) error{
		"cut short": func(w io.Writer) error {
			b := flacFile()
			_, _ = w.Write(b[:len(b)/2])
			return errors.New("connection reset")
		},
		"not a flac": func(w io.Writer) error {
			_, err := w.Write([]byte("<html>rate limited</html>"))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			opt, root := setup(t)
			src := &fakeSource{stocked: map[string]bool{"I": true}, body: body}
			res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
			if res[0].Outcome != Failed || res[0].Err == nil {
				t.Fatalf("result = %+v", res[0])
			}
			if got := files(t, root); len(got) != 0 {
				t.Errorf("files left behind: %v", got)
			}
		})
	}
}

func TestADownloadThatEndsWithoutErrorButWasShortIsStillCaught(t *testing.T) {
	opt, root := setup(t)
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: func(w io.Writer) error {
		_, _ = w.Write([]byte("fLaC"))
		return io.ErrUnexpectedEOF
	}}
	res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Failed || len(files(t, root)) != 0 {
		t.Errorf("result = %+v, files = %v", res[0], files(t, root))
	}
}

func TestWorkersAreBounded(t *testing.T) {
	opt, _ := setup(t)
	opt.Workers = 2
	src := &fakeSource{stocked: map[string]bool{}, hold: 30 * time.Millisecond}
	var items []resolve.Item
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		src.stocked[n] = true
		items = append(items, need(n, "Song "+n, "Artist"))
	}
	res := Run(context.Background(), items, src, &fakeRecorder{}, opt)
	for i, r := range res {
		if r.Outcome != Downloaded {
			t.Fatalf("item %d = %+v", i, r)
		}
	}
	if p := src.peak.Load(); p > 2 || p < 2 {
		t.Errorf("peak concurrent downloads = %d, want exactly 2", p)
	}
}

func TestTwoRecordingsWithTheSamePathDoNotClobberEachOther(t *testing.T) {
	opt, root := setup(t)
	src := &fakeSource{stocked: map[string]bool{"one": true, "two": true}, hold: 20 * time.Millisecond}
	res := Run(context.Background(), []resolve.Item{need("one", "Same", "Artist"), need("two", "Same", "Artist")}, src, &fakeRecorder{}, opt)
	outcomes := map[Outcome]int{}
	for _, r := range res {
		outcomes[r.Outcome]++
	}
	if outcomes[Downloaded] != 1 || outcomes[Failed] != 1 || len(files(t, root)) != 1 {
		t.Errorf("outcomes = %v, files = %v", outcomes, files(t, root))
	}
}

func TestResultsKeepInputOrderAndProgressFiresOncePerItem(t *testing.T) {
	opt, _ := setup(t)
	src := &fakeSource{stocked: map[string]bool{}, hold: 5 * time.Millisecond}
	var items []resolve.Item
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		src.stocked[n] = true
		items = append(items, need(n, "Song "+n, "Artist"))
	}
	var mu sync.Mutex
	var seen []string
	var inside atomic.Int32
	opt.Progress = func(r Result) {
		if inside.Add(1) != 1 {
			t.Error("progress callbacks overlapped")
		}
		mu.Lock()
		seen = append(seen, r.Item.Track.Title)
		mu.Unlock()
		inside.Add(-1)
	}
	res := Run(context.Background(), items, src, &fakeRecorder{}, opt)
	for i, r := range res {
		if r.Item.Track.ISRC != items[i].Track.ISRC {
			t.Errorf("result %d is for %s, want %s", i, r.Item.Track.ISRC, items[i].Track.ISRC)
		}
	}
	if len(seen) != 5 {
		t.Errorf("progress fired %d times, want 5", len(seen))
	}
}

func TestARecorderFailureIsReportedAndTheFileKept(t *testing.T) {
	opt, root := setup(t)
	src := &fakeSource{stocked: map[string]bool{"I": true}}
	res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{err: errors.New("db locked")}, opt)
	if res[0].Outcome != Failed || res[0].Err == nil {
		t.Errorf("result = %+v", res[0])
	}
	if len(files(t, root)) != 1 {
		t.Errorf("files = %v, want the downloaded file kept", files(t, root))
	}
}

func TestCancellationStopsAndCleansUp(t *testing.T) {
	opt, root := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	src := &fakeSource{stocked: map[string]bool{"a": true, "b": true}, hold: time.Second}
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res := Run(ctx, []resolve.Item{need("a", "A", "X"), need("b", "B", "X")}, src, &fakeRecorder{}, opt)
	if time.Since(start) > 900*time.Millisecond {
		t.Error("cancellation did not interrupt the downloads")
	}
	for _, r := range res {
		if r.Outcome != Failed {
			t.Errorf("outcome = %v, want failed", r.Outcome)
		}
	}
	if got := files(t, root); len(got) != 0 {
		t.Errorf("files left behind: %v", got)
	}
}

func noBackoff(int) time.Duration { return 0 }

func TestTransientDownloadErrorsAreRetried(t *testing.T) {
	opt, root := setup(t)
	opt.Backoff = noBackoff
	var calls atomic.Int32
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: func(w io.Writer) error {
		if calls.Add(1) < 3 {
			_, _ = w.Write(flacFile()[:30])
			return errors.New("stream error: INTERNAL_ERROR; received from peer")
		}
		_, err := w.Write(flacFile())
		return err
	}}
	rec := &fakeRecorder{}
	res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, rec, opt)
	if res[0].Outcome != Downloaded || res[0].Err != nil {
		t.Fatalf("result = %+v", res[0])
	}
	if calls.Load() != 3 || len(rec.entries) != 1 || len(files(t, root)) != 1 {
		t.Errorf("attempts = %d, recorded = %d, files = %v", calls.Load(), len(rec.entries), files(t, root))
	}
}

func TestGivesUpAfterTheAttemptLimit(t *testing.T) {
	opt, root := setup(t)
	opt.Backoff = noBackoff
	var calls atomic.Int32
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: func(io.Writer) error {
		calls.Add(1)
		return errors.New("connection reset")
	}}
	res := Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Failed || calls.Load() != 3 {
		t.Errorf("outcome = %v after %d attempts, want failed after 3", res[0].Outcome, calls.Load())
	}
	if len(files(t, root)) != 0 {
		t.Errorf("files left behind: %v", files(t, root))
	}
}

func TestAttemptsCanBeSetToOne(t *testing.T) {
	opt, _ := setup(t)
	opt.Attempts = 1
	var calls atomic.Int32
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: func(io.Writer) error {
		calls.Add(1)
		return errors.New("boom")
	}}
	Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if calls.Load() != 1 {
		t.Errorf("made %d attempts, want 1", calls.Load())
	}
}

func TestACancelledDownloadIsNotRetried(t *testing.T) {
	opt, _ := setup(t)
	opt.Backoff = noBackoff
	ctx, cancel := context.WithCancel(context.Background())
	src := &fakeSource{stocked: map[string]bool{"a": true}, hold: time.Second}
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	Run(ctx, []resolve.Item{need("a", "A", "X")}, src, &fakeRecorder{}, opt)
	if src.downloads != 1 {
		t.Errorf("made %d attempts after cancellation, want 1", src.downloads)
	}
}

func TestBackoffGrowsBetweenAttempts(t *testing.T) {
	opt, _ := setup(t)
	var waits []time.Duration
	opt.Backoff = func(n int) time.Duration { waits = append(waits, time.Duration(n)); return 0 }
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: func(io.Writer) error { return errors.New("boom") }}
	Run(context.Background(), []resolve.Item{need("I", "Barracuda", "Heart")}, src, &fakeRecorder{}, opt)
	if len(waits) != 2 || waits[0] != 1 || waits[1] != 2 {
		t.Errorf("backoff asked for attempts %v, want [1 2]", waits)
	}
}

func TestOutcomeStrings(t *testing.T) {
	for o, want := range map[Outcome]string{Downloaded: "downloaded", Exists: "exists", NotStocked: "not_stocked", Skipped: "skipped", Failed: "failed"} {
		if o.String() != want {
			t.Errorf("%d.String() = %q, want %q", o, o.String(), want)
		}
	}
}
