package resolve

import (
	"context"
	"errors"
	"testing"

	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/track"
)

type fakeDetails struct {
	d   monochrome.Details
	err error
	got [3]string
}

func (f *fakeDetails) Details(_ context.Context, isrc, artist, title string) (monochrome.Details, error) {
	f.got = [3]string{isrc, artist, title}
	return f.d, f.err
}

func TestEnrichFillsInWhatSearchResultsLack(t *testing.T) {
	f := &fakeDetails{d: monochrome.Details{Album: "Little Queen", AlbumArtist: "Heart", Date: "1977-05-14", TrackNo: 1, DiscNo: 1}}
	got, err := Enrich(context.Background(), track.Track{Title: "Barracuda", Artists: []string{"Heart"}, ISRC: "USSM10009075"}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got.Album != "Little Queen" || got.AlbumArtist != "Heart" || got.Date != "1977-05-14" || got.TrackNo != 1 || got.DiscNo != 1 {
		t.Errorf("got %+v", got)
	}
	if f.got != [3]string{"USSM10009075", "Heart", "Barracuda"} {
		t.Errorf("asked for %v", f.got)
	}
}

func TestEnrichKeepsWhatTheTrackAlreadyHas(t *testing.T) {
	f := &fakeDetails{d: monochrome.Details{Album: "Other", TrackNo: 9}}
	got, _ := Enrich(context.Background(), track.Track{Title: "T", Artists: []string{"A"}, ISRC: "X", Album: "Mine", TrackNo: 2}, f)
	if got.Album != "Mine" || got.TrackNo != 2 {
		t.Errorf("got %+v", got)
	}
}

func TestEnrichLeavesTheTrackAloneOnAnError(t *testing.T) {
	in := track.Track{Title: "T", Artists: []string{"A"}, ISRC: "X"}
	got, err := Enrich(context.Background(), in, &fakeDetails{err: errors.New("down")})
	if err == nil || got.Title != "T" || got.Album != "" {
		t.Errorf("got %+v err=%v", got, err)
	}
}

func TestEnrichWithNoDetailerOrNoArtistDoesNothing(t *testing.T) {
	in := track.Track{Title: "T", Artists: []string{"A"}, ISRC: "X"}
	if got, err := Enrich(context.Background(), in, nil); err != nil || got.Album != "" {
		t.Errorf("nil detailer: %+v %v", got, err)
	}
	f := &fakeDetails{d: monochrome.Details{Album: "Z"}}
	if got, _ := Enrich(context.Background(), track.Track{Title: "T", ISRC: "X"}, f); got.Album != "" {
		t.Errorf("no artist: %+v", got)
	}
}
