package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const stocked = "USTEST000001"

func (r *rig) submitTrack(isrc, title string, artists ...string) (Job, bool, error) {
	return r.m.SubmitTrack(context.Background(), isrc, title, artists)
}

func TestSubmitTrackQueuesAPickedRecording(t *testing.T) {
	r := newRig(t)
	j, created, err := r.submitTrack("ussm10009075", "Barracuda", "Heart")
	if err != nil || !created {
		t.Fatalf("err=%v created=%v", err, created)
	}
	if j.URL != "isrc:USSM10009075" || j.Kind != "track" || j.Name != "Heart - Barracuda" || j.Total != 1 || j.Counts[TrackPending] != 1 {
		t.Errorf("job = %+v", j)
	}
	again, created, _ := r.submitTrack("USSM10009075", "Barracuda", "Heart")
	if created || again.ID != j.ID {
		t.Errorf("again = %+v created=%v, want the first job", again, created)
	}
}

func TestSubmitTrackRejectsIncompleteInput(t *testing.T) {
	r := newRig(t)
	for name, c := range map[string]struct {
		isrc, title string
		artists     []string
	}{
		"short isrc": {"US1", "T", []string{"A"}},
		"no title":   {"USSM10009075", " ", []string{"A"}},
		"no artist":  {"USSM10009075", "T", nil},
	} {
		if _, _, err := r.m.SubmitTrack(context.Background(), c.isrc, c.title, c.artists); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAPickedTrackIsDownloadedWithoutSpotify(t *testing.T) {
	r := newRig(t)
	r.src.stocked[stocked] = true
	r.start()
	j, _, err := r.submitTrack("USTEST000001", "Barracuda", "Heart")
	if err != nil {
		t.Fatal(err)
	}
	j = r.waitFor(j.ID, Done, Failed)
	ts := r.tracks(j.ID)
	if j.Status != Done || ts["Barracuda"].Status != TrackDone || ts["Barracuda"].Path != "Singles/Heart/Barracuda.flac" {
		t.Fatalf("job = %+v tracks = %+v", j, ts)
	}
	if _, err := os.Stat(filepath.Join(r.root, "Singles/Heart/Barracuda.flac")); err != nil {
		t.Error(err)
	}
	if len(r.spot.seen) != 0 {
		t.Errorf("Spotify was asked for %v", r.spot.seen)
	}
}

func TestAPickedTrackTheCatalogueLacksIsNotStocked(t *testing.T) {
	r := newRig(t)
	r.start()
	j, _, _ := r.submitTrack("USTEST000009", "Rare", "Nobody")
	r.waitFor(j.ID, Done)
	if got := r.tracks(j.ID)["Rare"]; got.Status != TrackNotStocked {
		t.Errorf("got %+v", got)
	}
}

func TestAPickedTrackAlreadyInTheLibraryIsNotFetchedAgain(t *testing.T) {
	r := newRig(t)
	r.src.stocked[stocked] = true
	r.start()
	first, _, _ := r.submitTrack("USTEST000001", "Barracuda", "Heart")
	r.waitFor(first.ID, Done)
	second, created, _ := r.submitTrack("USTEST000001", "Barracuda", "Heart")
	if !created {
		t.Fatal("the finished job should not block a new one")
	}
	r.waitFor(second.ID, Done)
	if got := r.tracks(second.ID)["Barracuda"]; got.Status != TrackHave {
		t.Errorf("got %+v, want have", got)
	}
}

func TestRetryOfAPickedTrackKeepsWhatWasPicked(t *testing.T) {
	r := newRig(t)
	r.start()
	j, _, _ := r.submitTrack("USTEST000002", "Uprising", "Muse")
	r.waitFor(j.ID, Done)
	r.src.mu.Lock()
	r.src.stocked["USTEST000002"] = true
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
}
