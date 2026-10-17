package resolve

import (
	"context"
	"errors"

	"github.com/jigsmiths/dredge/internal/track"
)

// Direct is Resolve for a track the user picked from search results: the
// recording is known, so only the library is checked.
func Direct(ctx context.Context, t track.Track, lib Library) (Item, error) {
	if t.ISRC == "" {
		return Item{}, errors.New("a picked track needs an ISRC")
	}
	if it, ok, err := inLibrary(ctx, t, lib); err != nil || ok {
		return it, err
	}
	return Item{Track: t, Status: Need}, nil
}

// inLibrary reports whether a file for t is already there, by ISRC when t has
// one and by title and artist otherwise.
func inLibrary(ctx context.Context, t track.Track, lib Library) (Item, bool, error) {
	if t.ISRC != "" {
		if p, ok, err := lib.ByISRC(ctx, t.ISRC); err != nil {
			return Item{}, false, err
		} else if ok {
			return Item{Track: t, Status: Have, Path: p, Reason: "same ISRC"}, true, nil
		}
	}
	if p, ok, err := lib.ByMeta(ctx, t.Title, t.Artists); err != nil {
		return Item{}, false, err
	} else if ok {
		return Item{Track: t, Status: Have, Path: p, Reason: "same title and artist"}, true, nil
	}
	return Item{}, false, nil
}
