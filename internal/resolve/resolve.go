// Package resolve decides, for each wanted track, whether it is already in the
// library and otherwise which recording (ISRC) to download.
package resolve

import (
	"context"
	"fmt"
	"strings"

	"github.com/jigsmiths/dredge/internal/match"
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
	Track  track.Track
	Status Status
	Path   string // the existing file, when Have
	Reason string
}

const searchLimit = 10

// Resolve checks every track with at most workers running at once. A track
// that cannot be checked is reported as Failed and does not affect the rest.
func Resolve(ctx context.Context, tracks []track.Track, lib Library, s Searcher, workers int) []Item {
	res := pool.Map(ctx, workers, tracks, func(ctx context.Context, t track.Track) (Item, error) {
		return resolveOne(ctx, t, lib, s)
	})
	items := make([]Item, len(tracks))
	seen := map[string]bool{}
	for i, r := range res {
		items[i] = r.Value
		if r.Err != nil {
			items[i] = Item{Track: tracks[i], Status: Failed, Reason: r.Err.Error()}
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
	if t.ISRC != "" {
		if p, ok, err := lib.ByISRC(ctx, t.ISRC); err != nil {
			return Item{}, err
		} else if ok {
			return Item{Track: t, Status: Have, Path: p, Reason: "same ISRC"}, nil
		}
	}
	if p, ok, err := lib.ByMeta(ctx, t.Title, t.Artists); err != nil {
		return Item{}, err
	} else if ok {
		return Item{Track: t, Status: Have, Path: p, Reason: "same title and artist"}, nil
	}

	if len(t.Artists) == 0 || strings.TrimSpace(t.Artists[0]) == "" {
		return Item{Track: t, Status: NoMatch, Reason: "no artist to search with"}, nil
	}
	cands, err := s.Search(ctx, t.Artists[0]+" "+track.NormTitle(t.Title), searchLimit)
	if err != nil {
		return Item{}, fmt.Errorf("search: %w", err)
	}
	m := match.Pick(t, cands)
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
	return Item{Track: t, Status: Need, Reason: m.Reason}, nil
}
