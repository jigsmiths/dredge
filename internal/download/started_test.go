package download

import (
	"context"
	"sync"
	"testing"

	"github.com/jigsmiths/dredge/internal/resolve"
)

func TestStartedFiresForEachNeededItemBeforeItsResult(t *testing.T) {
	opt, _ := setup(t)
	var mu sync.Mutex
	var events []string
	opt.Started = func(it resolve.Item) {
		mu.Lock()
		events = append(events, "start "+it.Track.Title)
		mu.Unlock()
	}
	opt.Progress = func(r Result) {
		mu.Lock()
		events = append(events, "done "+r.Item.Track.Title)
		mu.Unlock()
	}
	have := need("H", "Have", "A")
	have.Status = resolve.Have
	src := &fakeSource{stocked: map[string]bool{"X": true}}
	Run(context.Background(), []resolve.Item{need("X", "One", "A"), have}, src, &fakeRecorder{}, opt)
	var one []string
	for _, e := range events {
		if e != "done Have" {
			one = append(one, e)
		}
	}
	if len(one) != 2 || one[0] != "start One" || one[1] != "done One" {
		t.Errorf("events = %v", events)
	}
}

func TestPlacedListsFilesInItemOrderAndFillsDuplicates(t *testing.T) {
	a, b, dup, miss := need("A", "A", "X"), need("B", "B", "X"), need("A", "A", "X"), need("M", "M", "X")
	b.Status, b.Path = resolve.Have, "/m/b.flac"
	dup.Status = resolve.Duplicate
	results := []Result{
		{Item: a, Outcome: Downloaded, Path: "/m/a.flac"},
		{Item: b, Outcome: Skipped, Path: "/m/b.flac"},
		{Item: dup, Outcome: Skipped},
		{Item: miss, Outcome: NotStocked},
	}
	got := Placed([]resolve.Item{a, b, dup, miss}, results)
	want := []string{"/m/a.flac", "/m/b.flac", "/m/a.flac"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}
