package resolve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/track"
)

type fakeLib struct {
	byISRC map[string]string
	byMeta map[string]string // "title|artist" -> path
	err    error
}

func (l *fakeLib) ByISRC(_ context.Context, isrc string) (string, bool, error) {
	p, ok := l.byISRC[isrc]
	return p, ok, l.err
}

func (l *fakeLib) ByMeta(_ context.Context, title string, artists []string) (string, bool, error) {
	for _, a := range artists {
		if p, ok := l.byMeta[track.NormTitle(title)+"|"+track.NormArtist(a)]; ok {
			return p, true, l.err
		}
	}
	return "", false, l.err
}

type fakeSearch struct {
	mu      sync.Mutex
	queries []string
	results map[string][]match.Candidate // by query
	errFor  map[string]error
}

func (s *fakeSearch) Search(_ context.Context, q string, _ int) ([]match.Candidate, error) {
	s.mu.Lock()
	s.queries = append(s.queries, q)
	s.mu.Unlock()
	if err := s.errFor[q]; err != nil {
		return nil, err
	}
	return s.results[q], nil
}

func tr(title, artist string, ms int) track.Track {
	return track.Track{Title: title, Artists: []string{artist}, Album: "Album", DurationMS: ms}
}

func cand(isrc, title, artist string, ms int) match.Candidate {
	return match.Candidate{ISRC: isrc, Title: title, Artists: []string{artist}, Album: "Album", DurationMS: ms}
}

func TestNeedWhenMatchedAndNotInTheLibrary(t *testing.T) {
	s := &fakeSearch{results: map[string][]match.Candidate{
		"Heart barracuda": {cand("USSM10009075", "Barracuda", "Heart", 264000)},
	}}
	got := Resolve(context.Background(), []track.Track{tr("Barracuda", "Heart", 261495)}, &fakeLib{}, s, 4)
	if len(got) != 1 || got[0].Status != Need || got[0].Track.ISRC != "USSM10009075" {
		t.Fatalf("got %+v", got)
	}
}

func TestHaveByMetadataSkipsTheSearchEntirely(t *testing.T) {
	lib := &fakeLib{byMeta: map[string]string{"barracuda|heart": "/music/Heart/b.flac"}}
	s := &fakeSearch{}
	got := Resolve(context.Background(), []track.Track{tr("Barracuda", "Heart", 261495)}, lib, s, 4)
	if got[0].Status != Have || got[0].Path != "/music/Heart/b.flac" {
		t.Fatalf("got %+v", got[0])
	}
	if len(s.queries) != 0 {
		t.Errorf("searched Deezer %d times for a track already in the library", len(s.queries))
	}
}

func TestHaveByISRCAfterTheMatch(t *testing.T) {
	lib := &fakeLib{byISRC: map[string]string{"USSM10009075": "/music/x.flac"}}
	s := &fakeSearch{results: map[string][]match.Candidate{
		"Heart barracuda": {cand("USSM10009075", "Barracuda", "Heart", 264000)},
	}}
	got := Resolve(context.Background(), []track.Track{tr("Barracuda", "Heart", 261495)}, lib, s, 4)
	if got[0].Status != Have || got[0].Path != "/music/x.flac" || got[0].Track.ISRC != "USSM10009075" {
		t.Fatalf("got %+v", got[0])
	}
}

func TestNoMatchAndAmbiguousSayWhy(t *testing.T) {
	s := &fakeSearch{results: map[string][]match.Candidate{
		"A song": {cand("X", "Song", "A", 100000), cand("Y", "Song", "A", 100000)},
	}}
	got := Resolve(context.Background(), []track.Track{
		tr("Unknown", "Nobody", 1000),
		tr("Song", "A", 0), // no length to tell the recordings apart by
	}, &fakeLib{}, s, 2)
	if got[0].Status != NoMatch || got[0].Reason == "" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Status != Ambiguous || got[1].Reason == "" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestOneFailureDoesNotSpoilTheRest(t *testing.T) {
	boom := errors.New("deezer down")
	s := &fakeSearch{
		results: map[string][]match.Candidate{"B two": {cand("ISRC2", "Two", "B", 1000)}},
		errFor:  map[string]error{"A one": boom},
	}
	got := Resolve(context.Background(), []track.Track{tr("One", "A", 1000), tr("Two", "B", 1000)}, &fakeLib{}, s, 2)
	if got[0].Status != Failed || !strings.Contains(got[0].Reason, "deezer down") {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Status != Need {
		t.Errorf("second = %+v", got[1])
	}
}

func TestLibraryErrorFailsTheItem(t *testing.T) {
	lib := &fakeLib{err: errors.New("db locked")}
	got := Resolve(context.Background(), []track.Track{tr("One", "A", 1000)}, lib, &fakeSearch{}, 1)
	if got[0].Status != Failed || !strings.Contains(got[0].Reason, "db locked") {
		t.Errorf("got %+v", got[0])
	}
}

func TestTheSameRecordingTwiceInAPlaylistIsOneDownload(t *testing.T) {
	s := &fakeSearch{results: map[string][]match.Candidate{
		"Heart barracuda": {cand("USSM10009075", "Barracuda", "Heart", 264000)},
	}}
	got := Resolve(context.Background(), []track.Track{
		tr("Barracuda", "Heart", 261495), tr("Barracuda", "Heart", 261495),
	}, &fakeLib{}, s, 4)
	if got[0].Status != Need || got[1].Status != Duplicate {
		t.Errorf("statuses = %v, %v; want need then duplicate", got[0].Status, got[1].Status)
	}
}

func TestQueryDropsReleaseTagsAndKeepsTheFirstArtist(t *testing.T) {
	s := &fakeSearch{results: map[string][]match.Candidate{
		"Heart crazy on you": {cand("USCA28600007", "Crazy On You", "Heart", 1000)},
	}}
	got := Resolve(context.Background(), []track.Track{{
		Title: "Crazy On You - 2004 Remaster", Artists: []string{"Heart", "Guest"}, DurationMS: 1000,
	}}, &fakeLib{}, s, 1)
	if len(s.queries) != 1 || s.queries[0] != "Heart crazy on you" || got[0].Status != Need {
		t.Errorf("queries = %v, status = %v", s.queries, got[0].Status)
	}
}

func TestRetriesWithTheTitleAloneWhenTheArtistQueryFindsNothing(t *testing.T) {
	s := &fakeSearch{results: map[string][]match.Candidate{
		"free bird": {cand("USMC17301722", "Free Bird", "Lynyrd Skynyrd", 558000)},
	}}
	got := Resolve(context.Background(), []track.Track{tr("Free Bird", "Lynyrd Skynyrd", 558000)}, &fakeLib{}, s, 1)
	if got[0].Status != Need || got[0].Track.ISRC != "USMC17301722" {
		t.Fatalf("got %+v", got[0])
	}
	if len(s.queries) != 2 || s.queries[0] != "Lynyrd Skynyrd free bird" || s.queries[1] != "free bird" {
		t.Errorf("queries = %v", s.queries)
	}
}

func TestDoesNotRetryOnceTheFirstQueryDecided(t *testing.T) {
	s := &fakeSearch{results: map[string][]match.Candidate{
		"A song": {cand("X", "Song", "A", 100000), cand("Y", "Song", "A", 100000)},
	}}
	got := Resolve(context.Background(), []track.Track{tr("Song", "A", 0)}, &fakeLib{}, s, 1)
	if got[0].Status != Ambiguous || len(s.queries) != 1 {
		t.Errorf("status = %v after %d queries, want ambiguous after 1", got[0].Status, len(s.queries))
	}
}

func TestAnErrorOnTheSecondQueryFailsTheItem(t *testing.T) {
	s := &fakeSearch{errFor: map[string]error{"free bird": errors.New("api down")}}
	got := Resolve(context.Background(), []track.Track{tr("Free Bird", "Lynyrd Skynyrd", 1000)}, &fakeLib{}, s, 1)
	if got[0].Status != Failed || !strings.Contains(got[0].Reason, "api down") {
		t.Errorf("got %+v", got[0])
	}
}

func TestResultsKeepInputOrderUnderConcurrency(t *testing.T) {
	s := &fakeSearch{results: map[string][]match.Candidate{}}
	var in []track.Track
	for i := range 30 {
		title := fmt.Sprintf("Song %d", i)
		in = append(in, tr(title, "Artist", 1000))
		s.results["Artist "+track.NormTitle(title)] = []match.Candidate{cand(fmt.Sprintf("ISRC%02d", i), title, "Artist", 1000)}
	}
	got := Resolve(context.Background(), in, &fakeLib{}, s, 8)
	for i, it := range got {
		if it.Track.Title != in[i].Title || it.Track.ISRC != fmt.Sprintf("ISRC%02d", i) {
			t.Fatalf("item %d = %+v", i, it.Track)
		}
	}
}

func TestCancelledContextFailsTheItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := Resolve(ctx, []track.Track{tr("One", "A", 1000)}, &fakeLib{}, &fakeSearch{}, 1)
	if got[0].Status != Failed {
		t.Errorf("got %+v", got[0])
	}
}

func TestTracksWithoutAnArtistCannotBeMatched(t *testing.T) {
	got := Resolve(context.Background(), []track.Track{{Title: "Orphan", DurationMS: 1}}, &fakeLib{}, &fakeSearch{}, 1)
	if got[0].Status != NoMatch {
		t.Errorf("got %+v", got[0])
	}
}

func TestStatusStrings(t *testing.T) {
	want := map[Status]string{Need: "need", Have: "have", NoMatch: "no_match", Ambiguous: "ambiguous", Duplicate: "duplicate", Failed: "failed"}
	for s, w := range want {
		if s.String() != w {
			t.Errorf("%d.String() = %q, want %q", s, s.String(), w)
		}
	}
}
