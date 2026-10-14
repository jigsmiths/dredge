package resolve

import (
	"context"

	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/track"
)

type Detailer interface {
	Details(ctx context.Context, isrc, artist, title string) (monochrome.Details, error)
}

// Enrich adds the album, album artist, date and track position to a track
// picked from search results, which carry none of them. It never fails the
// track: on an error the track comes back as it was, with the error to log.
func Enrich(ctx context.Context, t track.Track, d Detailer) (track.Track, error) {
	if d == nil || t.ISRC == "" || len(t.Artists) == 0 {
		return t, nil
	}
	det, err := d.Details(ctx, t.ISRC, t.Artists[0], t.Title)
	if err != nil {
		return t, err
	}
	if t.Album == "" {
		t.Album = det.Album
	}
	if t.AlbumArtist == "" {
		t.AlbumArtist = det.AlbumArtist
	}
	if t.Date == "" {
		t.Date = det.Date
	}
	if t.TrackNo == 0 {
		t.TrackNo = det.TrackNo
	}
	if t.DiscNo == 0 {
		t.DiscNo = det.DiscNo
	}
	return t, nil
}
