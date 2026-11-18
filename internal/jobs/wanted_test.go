package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jigsmiths/dredge/internal/match"
)

func (r *rig) wanted() map[string]Wanted {
	r.t.Helper()
	ws, err := r.m.ListWanted(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string]Wanted{}
	for _, w := range ws {
		out[w.Title] = w
	}
	return out
}

func (r *rig) waitWanted(what string, ok func(map[string]Wanted) bool) map[string]Wanted {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ws := r.wanted()
		if ok(ws) {
			return ws
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("wanted list never %s: %+v", what, ws)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestASpotifyJobKeepsTheTracksItCouldNotGet(t *testing.T) {
	r := newRig(t)
	r.start()
	r.waitFor(r.submit(plID).ID, Done, Partial)

	ws := r.wanted()
	if len(ws) != 2 {
		t.Fatalf("wanted = %+v, want Unknown and Uprising", ws)
	}
	if w := ws["Unknown"]; w.DurationMS != 200000 || len(w.Artists) != 1 || w.Artists[0] != "Nobody" {
		t.Errorf("Unknown = %+v, want its Spotify length and artist", w)
	}
	if w := ws["Uprising"]; w.Reason != "not stocked" {
		t.Errorf("Uprising reason = %q, want not stocked", w.Reason)
	}

	// running the link again does not list them twice
	j, err := r.m.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.Retry(context.Background(), j[0].ID); err != nil {
		t.Fatal(err)
	}
	r.waitFor(j[0].ID, Done, Partial)
	if ws := r.wanted(); len(ws) != 2 {
		t.Errorf("after a rerun wanted = %+v, want the same two", ws)
	}
}

func TestCheckingQueuesAWantedTrackTheCatalogueNowHas(t *testing.T) {
	r := newRig(t)
	const isrc = "USAAA2600001"
	r.search.results["Nobody unknown"] = []match.Candidate{cand(isrc, "Unknown", "Nobody")}
	r.src.stocked[isrc] = true
	if err := r.m.AddWanted(context.Background(), tr("Unknown", "Nobody"), "no match"); err != nil {
		t.Fatal(err)
	}
	r.start()
	r.m.CheckWanted()

	r.waitWanted("emptied", func(ws map[string]Wanted) bool { return len(ws) == 0 })
	js, err := r.m.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 1 || js[0].URL != pickedPrefix+isrc {
		t.Fatalf("jobs = %+v, want one picked job for %s", js, isrc)
	}
	if j := r.waitFor(js[0].ID, Done, Partial, Failed); j.Status != Done || j.Counts[TrackDone] != 1 {
		t.Errorf("job = %+v, want the track downloaded", j)
	}
}

func TestCheckingForgetsAWantedTrackTheLibraryNowHas(t *testing.T) {
	r := newRig(t)
	r.start()
	r.waitFor(r.submit(plID2).ID, Done)
	if err := r.m.AddWanted(context.Background(), tr("Barracuda", "Heart"), "no match"); err != nil {
		t.Fatal(err)
	}
	r.m.CheckWanted()
	r.waitWanted("emptied", func(ws map[string]Wanted) bool { return len(ws) == 0 })
	if js, _ := r.m.List(context.Background(), ""); len(js) != 1 {
		t.Errorf("jobs = %+v, want no new job for a track already had", js)
	}
}

func TestCheckingKeepsAWantedTrackStillMissing(t *testing.T) {
	r := newRig(t)
	if err := r.m.AddWanted(context.Background(), tr("Unknown", "Nobody"), "no match"); err != nil {
		t.Fatal(err)
	}
	r.start()
	r.m.CheckWanted()
	ws := r.waitWanted("checked", func(ws map[string]Wanted) bool { return ws["Unknown"].CheckedAt != nil })
	if len(ws) != 1 {
		t.Errorf("wanted = %+v, want Unknown kept", ws)
	}
}

func TestATrackWantedByNameIsQueuedOnceFound(t *testing.T) {
	r := newRig(t)
	const isrc = "USAAA2600002"
	r.search.results["Ashe stop the wedding"] = []match.Candidate{cand(isrc, "Stop the Wedding", "Ashe")}
	r.src.stocked[isrc] = true
	for _, bad := range [][2]string{{"", "Ashe"}, {"Stop the Wedding", " "}} {
		if err := r.m.Want(context.Background(), bad[0], []string{bad[1]}); !errors.Is(err, ErrInvalid) {
			t.Errorf("Want(%q, %q) = %v, want ErrInvalid", bad[0], bad[1], err)
		}
	}
	if err := r.m.Want(context.Background(), " Stop the Wedding ", []string{"Ashe", ""}); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Want(context.Background(), "stop the wedding", []string{"ashe"}); err != nil {
		t.Fatal(err)
	}
	if ws := r.wanted(); len(ws) != 1 || ws["Stop the Wedding"].Reason != "added by name" {
		t.Fatalf("wanted = %+v, want one entry for Stop the Wedding", ws)
	}

	r.start()
	r.m.CheckWanted()
	r.waitWanted("emptied", func(ws map[string]Wanted) bool { return len(ws) == 0 })
	js, err := r.m.List(context.Background(), "")
	if err != nil || len(js) != 1 || js[0].URL != pickedPrefix+isrc {
		t.Fatalf("jobs = %+v err=%v, want one picked job for %s", js, err, isrc)
	}
}

func TestOnlyDueWantedTracksAreLookedForOnASchedule(t *testing.T) {
	r := newRig(t)
	r.m.deps.WantEvery = time.Hour
	if err := r.m.AddWanted(context.Background(), tr("Unknown", "Nobody"), "no match"); err != nil {
		t.Fatal(err)
	}
	r.m.checkWanted(context.Background(), false)
	if w := r.wanted()["Unknown"]; w.CheckedAt != nil {
		t.Errorf("a track added just now was looked for: %+v", w)
	}
}
