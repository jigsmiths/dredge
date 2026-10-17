// Package resolve decides, for each wanted track, whether it is already in the
// library and otherwise which recording (ISRC) to download.
package resolve

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/pool"
	"github.com/jigsmiths/dredge/internal/track"
)

type Library interface {
	ByISRC(ctx context.Context, isrc string) (string, bool, error)
	ByMeta(ctx context.Context, title string, artists []string) (string, bool, error)
}

type Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]match.Candidate, error)
}

type Status int

const (
	Need      Status = iota // matched, not in the library: to download
	Have                    // already in the library
	NoMatch                 // no recording fits
	Ambiguous               // several recordings fit equally well
	Duplicate               // the same recording as an earlier track in the list
	Failed                  // could not be checked
)

func (s Status) String() string {
	switch s {
	case Need:
		return "need"
	case Have:
		return "have"
	case NoMatch:
		return "no_match"
	case Ambiguous:
		return "ambiguous"
	case Duplicate:
		return "duplicate"
	}
	return "failed"
}

// Item is one wanted track with its outcome. Track.ISRC is set once matched.
type Item struct {
	Pos    int // place in the list it came from
	Track  track.Track
	Status Status
	Path   string // the existing file, when Have
	Reason string

	// the catalogue's track for a Need item, when the search that matched it
	// already found it; nil means download must look it up
	Found *monochrome.Track
}

const searchLimit = 25

// Resolve checks every track with at most workers running at once. A track
// that cannot be checked is reported as Failed and does not affect the rest.
// each, if set, sees every item as it resolves, one at a time, before Duplicate is marked.
func Resolve(ctx context.Context, tracks []track.Track, lib Library, s Searcher, workers int, each func(Item)) []Item {
	type indexed struct {
		pos int
		t   track.Track
	}
	in := make([]indexed, len(tracks))
	for i, t := range tracks {
		in[i] = indexed{i, t}
	}
	var mu sync.Mutex
	res := pool.Map(ctx, workers, in, func(ctx context.Context, w indexed) (Item, error) {
		it, err := resolveOne(ctx, w.t, lib, s)
		if err != nil {
			it = Item{Track: w.t, Status: Failed, Reason: err.Error()}
		}
		it.Pos = w.pos
		if each != nil {
			mu.Lock()
			each(it)
			mu.Unlock()
		}
		return it, err
	})
	items := make([]Item, len(tracks))
	seen := map[string]bool{}
	for i, r := range res {
		items[i] = r.Value
		if r.Err != nil {
			items[i] = Item{Pos: i, Track: tracks[i], Status: Failed, Reason: r.Err.Error()}
			continue
		}
		if items[i].Status != Need {
			continue
		}
		if isrc := items[i].Track.ISRC; seen[isrc] {
			items[i].Status, items[i].Reason = Duplicate, "same recording as an earlier track"
		} else {
			seen[isrc] = true
		}
	}
	return items
}

func resolveOne(ctx context.Context, t track.Track, lib Library, s Searcher) (Item, error) {
	if it, ok, err := inLibrary(ctx, t, lib); err != nil || ok {
		return it, err
	}

	if len(t.Artists) == 0 || strings.TrimSpace(t.Artists[0]) == "" {
		return Item{Track: t, Status: NoMatch, Reason: "no artist to search with"}, nil
	}
	// the title alone is a second chance when the artist is spelled differently
	title := track.NormTitle(t.Title)
	var m match.Result
	for _, q := range []string{t.Artists[0] + " " + title, title} {
		cands, err := s.Search(ctx, q, searchLimit)
		if err != nil {
			return Item{}, fmt.Errorf("search: %w", err)
		}
		if m = match.Pick(t, cands); m.Outcome != match.NoMatch {
			break
		}
	}
	switch m.Outcome {
	case match.NoMatch:
		return Item{Track: t, Status: NoMatch, Reason: m.Reason}, nil
	case match.Ambiguous:
		return Item{Track: t, Status: Ambiguous, Reason: m.Reason}, nil
	}

	t.ISRC = m.Best.ISRC
	if p, ok, err := lib.ByISRC(ctx, t.ISRC); err != nil {
		return Item{}, err
	} else if ok {
		return Item{Track: t, Status: Have, Path: p, Reason: "same ISRC"}, nil
	}
	it := Item{Track: t, Status: Need, Reason: m.Reason}
	if m.Best.ID != "" {
		found := monochrome.TrackFrom(m.Best)
		it.Found = &found
	}
	return it, nil
}
