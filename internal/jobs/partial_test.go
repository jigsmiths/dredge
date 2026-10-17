package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAJobWithAFailedTrackEndsPartial(t *testing.T) {
	r := newRig(t)
	r.src.stocked["ISRC2"] = true
	r.src.failFor = map[string]error{"ISRC2": errors.New("connection reset")}
	r.start()
	j := r.waitFor(r.submit(plID).ID, Done, Partial, Failed)
	if j.Status != Partial || j.Counts[TrackFailed] != 1 || j.Counts[TrackDone] != 1 {
		t.Fatalf("job = %+v", j)
	}
}

func TestMissingTracksAloneLeaveAJobDone(t *testing.T) {
	r := newRig(t)
	r.start()
	if j := r.waitFor(r.submit(plID).ID, Done, Partial, Failed); j.Status != Done {
		t.Fatalf("status = %s, want done: not stocked and no match are not failures", j.Status)
	}
}

func TestAPartialJobCanBeRetried(t *testing.T) {
	r := newRig(t)
	r.src.stocked["ISRC2"] = true
	r.src.failFor = map[string]error{"ISRC2": errors.New("connection reset")}
	r.start()
	j := r.waitFor(r.submit(plID).ID, Partial)

	r.src.mu.Lock()
	r.src.failFor = nil
	r.src.mu.Unlock()
	if err := r.m.Retry(context.Background(), j.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if j = r.waitFor(j.ID, Done); j.Counts[TrackFailed] != 0 {
		t.Errorf("counts = %v after the retry", j.Counts)
	}
}

func TestAPartialJobCanBeDeleted(t *testing.T) {
	r := newRig(t)
	r.src.stocked["ISRC2"] = true
	r.src.failFor = map[string]error{"ISRC2": errors.New("connection reset")}
	r.start()
	j := r.waitFor(r.submit(plID).ID, Partial)
	if err := r.m.Delete(context.Background(), j.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
}

func TestTracksShowWhileTheRestAreStillBeingLookedUp(t *testing.T) {
	r := newRig(t)
	gate := make(chan struct{})
	r.search.hold = map[string]chan struct{}{"Muse uprising": gate}
	r.start()
	j := r.submit(plID)

	deadline := time.Now().Add(5 * time.Second)
	for {
		ts := r.tracks(j.ID)
		if ts["Barracuda"].Status == TrackPending && ts["Unknown"].Status == TrackNoMatch {
			if _, there := ts["Uprising"]; there {
				t.Fatal("a track still being looked up should have no row yet")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no rows while resolving: %+v", ts)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, _ := r.m.Get(context.Background(), j.ID); got.Status != Resolving {
		t.Errorf("status = %s, want resolving", got.Status)
	}
	close(gate)
	r.waitFor(j.ID, Done)
	if len(r.tracks(j.ID)) != 3 {
		t.Errorf("tracks = %+v, want all three once resolved", r.tracks(j.ID))
	}
}
