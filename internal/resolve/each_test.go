package resolve

import (
	"context"
	"sync"
	"testing"

	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/track"
)

func resolveAll(ctx context.Context, tracks []track.Track, lib Library, s Searcher, workers int) []Item {
	return Resolve(ctx, tracks, lib, s, workers, nil)
}

func TestEachItemIsReportedOnceAsItResolves(t *testing.T) {
	s := &fakeSearch{results: map[string][]match.Candidate{
		"Heart barracuda": {cand("USSM10009075", "Barracuda", "Heart", 261495)},
	}}
	lib := &fakeLib{byMeta: map[string]string{"free bird|lynyrd skynyrd": "/m/Free Bird.flac"}}
	in := []track.Track{
		tr("Barracuda", "Heart", 261495),
		tr("Free Bird", "Lynyrd Skynyrd", 558000),
		tr("Nothing Like It", "Nobody", 1000),
	}
	var mu sync.Mutex
	seen := map[int]Item{}
	got := Resolve(context.Background(), in, lib, s, 3, func(it Item) {
		mu.Lock()
		defer mu.Unlock()
		if _, dup := seen[it.Pos]; dup {
			t.Errorf("track %d reported twice", it.Pos)
		}
		seen[it.Pos] = it
	})
	want := []Status{Need, Have, NoMatch}
	for i, st := range want {
		if seen[i].Status != st || got[i].Status != st {
			t.Errorf("track %d: reported %v, returned %v, want %v", i, seen[i].Status, got[i].Status, st)
		}
	}
}

func TestAFailedTrackIsReportedToo(t *testing.T) {
	lib := &fakeLib{err: context.DeadlineExceeded}
	var statuses []Status
	Resolve(context.Background(), []track.Track{tr("One", "A", 1000)}, lib, &fakeSearch{}, 1, func(it Item) {
		statuses = append(statuses, it.Status)
	})
	if len(statuses) != 1 || statuses[0] != Failed {
		t.Errorf("statuses = %v, want [failed]", statuses)
	}
}
