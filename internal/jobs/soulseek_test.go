package jobs

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jigsmiths/dredge/internal/slskd"
	"github.com/jigsmiths/dredge/internal/track"
)

// flacOfLength is a FLAC whose STREAMINFO declares ms milliseconds at 44.1kHz.
func flacOfLength(ms int) []byte {
	info := make([]byte, 34)
	samples := uint64(ms) * 44100 / 1000
	binary.BigEndian.PutUint64(info[10:18], uint64(44100)<<44|uint64(1)<<41|uint64(15)<<36|samples)
	b := append([]byte("fLaC"), 0x80, 0, 0, 34)
	b = append(b, info...)
	return append(b, "AUDIO"...)
}

// fakeSoulseek "downloads" a FLAC of length ms into its own folder, as slskd does.
type fakeSoulseek struct {
	t    *testing.T
	ms   int
	err  error
	mu   sync.Mutex
	asks []track.Track
	dirs []string
}

func (f *fakeSoulseek) Fetch(_ context.Context, t track.Track, progress func(done, total int64)) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asks = append(f.asks, t)
	if f.err != nil {
		return "", f.err
	}
	dir := filepath.Join(f.t.TempDir(), "dredge", "batch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.dirs = append(f.dirs, dir)
	path := filepath.Join(dir, "01 - ashe - stop the wedding.flac")
	if err := os.WriteFile(path, flacOfLength(f.ms), 0o644); err != nil {
		f.t.Fatal(err)
	}
	progress(100, 100)
	return path, nil
}

func (f *fakeSoulseek) fetched() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asks)
}

var stopTheWedding = track.Track{Title: "Stop The Wedding!", Artists: []string{"Ashe"}, Album: "Stop The Wedding!", DurationMS: 198000, ISRC: "USAT22604492"}

func soulseekRig(t *testing.T, s *fakeSoulseek) *rig {
	r := newRig(t)
	s.t = t
	r.m.deps.Soulseek = s
	if err := r.m.AddWanted(context.Background(), stopTheWedding, "added by name"); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) onlyJob() Job {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		js, err := r.m.List(context.Background(), "")
		if err != nil || len(js) > 1 {
			r.t.Fatalf("jobs = %+v err=%v, want one", js, err)
		}
		if len(js) == 1 {
			return r.waitFor(js[0].ID, Done, Partial, Failed)
		}
		if time.Now().After(deadline) {
			r.t.Fatal("no job was queued")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAWantedTrackMissingFromTheCatalogueIsFetchedFromSoulseek(t *testing.T) {
	s := &fakeSoulseek{ms: 198400}
	r := soulseekRig(t, s)
	r.start()
	r.m.CheckWanted()

	j := r.onlyJob()
	if j.Status != Done || j.Counts[TrackDone] != 1 || j.URL[:len(soulseekPrefix)] != soulseekPrefix {
		t.Fatalf("job = %+v, want a done Soulseek job", j)
	}
	r.waitWanted("emptied", func(ws map[string]Wanted) bool { return len(ws) == 0 })

	tags := fileTags(t, filepath.Join(r.root, "Singles/Ashe/Stop The Wedding!.flac"))
	if tags.Title != "Stop The Wedding!" || tags.ISRC != "USAT22604492" || tags.Album != "Stop The Wedding!" || tags.AlbumArtist != "Ashe" {
		t.Errorf("tags = %+v, want the wanted entry's", tags)
	}
	if _, err := os.Stat(s.dirs[0]); !os.IsNotExist(err) {
		t.Errorf("slskd's folder was left behind: %v", err)
	}
	if p, ok, _ := r.m.deps.Library.ByISRC(context.Background(), "USAT22604492"); !ok {
		t.Errorf("not indexed: %q", p)
	}
}

func TestAWantedTrackStaysWhenSoulseekHasNoFileThatFits(t *testing.T) {
	r := soulseekRig(t, &fakeSoulseek{err: slskd.ErrNoFile})
	r.start()
	r.m.CheckWanted()

	if j := r.onlyJob(); j.Status != Done || j.Counts[TrackNoMatch] != 1 {
		t.Errorf("job = %+v, want done with the track not matched", j)
	}
	ws := r.waitWanted("checked", func(ws map[string]Wanted) bool {
		return ws["Stop The Wedding!"].Reason == "no FLAC on Soulseek fits"
	})
	if len(ws) != 1 {
		t.Errorf("wanted = %+v, want the track kept", ws)
	}
}

func TestAFileOfTheWrongLengthIsNotFiled(t *testing.T) {
	s := &fakeSoulseek{ms: 240000}
	r := soulseekRig(t, s)
	r.start()
	r.m.CheckWanted()

	if j := r.onlyJob(); j.Status != Partial || j.Counts[TrackFailed] != 1 {
		t.Errorf("job = %+v, want the track failed", j)
	}
	if _, err := os.Stat(filepath.Join(r.root, "Singles/Ashe")); !os.IsNotExist(err) {
		t.Errorf("something was filed: %v", err)
	}
	if _, err := os.Stat(s.dirs[0]); !os.IsNotExist(err) {
		t.Errorf("slskd's folder was left behind: %v", err)
	}
	r.waitWanted("checked", func(ws map[string]Wanted) bool { return ws["Stop The Wedding!"].CheckedAt != nil })
}

func TestOnlyAWantedTrackWithALengthGoesToSoulseek(t *testing.T) {
	s := &fakeSoulseek{t: t, ms: 198000}
	r := newRig(t)
	r.m.deps.Soulseek = s
	if err := r.m.Want(context.Background(), "stop the wedding", []string{"ashe"}); err != nil {
		t.Fatal(err)
	}
	r.start()
	r.m.CheckWanted()
	r.waitWanted("checked", func(ws map[string]Wanted) bool { return ws["stop the wedding"].CheckedAt != nil })
	if js, _ := r.m.List(context.Background(), ""); len(js) != 0 || s.fetched() != 0 {
		t.Errorf("jobs = %+v, fetched %d, want nothing without a length", js, s.fetched())
	}
}
