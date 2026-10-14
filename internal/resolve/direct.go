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
	if p, ok, err := lib.ByISRC(ctx, t.ISRC); err != nil {
		return Item{}, err
	} else if ok {
		return Item{Track: t, Status: Have, Path: p, Reason: "same ISRC"}, nil
	}
	if p, ok, err := lib.ByMeta(ctx, t.Title, t.Artists); err != nil {
		return Item{}, err
	} else if ok {
		return Item{Track: t, Status: Have, Path: p, Reason: "same title and artist"}, nil
	}
	return Item{Track: t, Status: Need}, nil
}
