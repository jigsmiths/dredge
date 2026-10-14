package resolve

import (
	"context"
	"strings"
	"testing"

	"github.com/jigsmiths/dredge/internal/track"
)

func picked() track.Track {
	return track.Track{Title: "Barracuda", Artists: []string{"Heart"}, ISRC: "USSM10009075"}
}

func TestDirectNeedsATrackTheLibraryLacks(t *testing.T) {
	it, err := Direct(context.Background(), picked(), &fakeLib{})
	if err != nil || it.Status != Need || it.Track.ISRC != "USSM10009075" {
		t.Errorf("got %+v err=%v", it, err)
	}
}

func TestDirectHaveByISRC(t *testing.T) {
	lib := &fakeLib{byISRC: map[string]string{"USSM10009075": "/m/b.flac"}}
	it, _ := Direct(context.Background(), picked(), lib)
	if it.Status != Have || it.Path != "/m/b.flac" {
		t.Errorf("got %+v", it)
	}
}

func TestDirectHaveByTitleAndArtist(t *testing.T) {
	lib := &fakeLib{byMeta: map[string]string{"barracuda|heart": "/m/b.flac"}}
	it, _ := Direct(context.Background(), picked(), lib)
	if it.Status != Have || it.Path != "/m/b.flac" {
		t.Errorf("got %+v", it)
	}
}

func TestDirectNeedsAnISRC(t *testing.T) {
	tr := picked()
	tr.ISRC = ""
	if _, err := Direct(context.Background(), tr, &fakeLib{}); err == nil || !strings.Contains(err.Error(), "ISRC") {
		t.Errorf("err = %v", err)
	}
}

func TestDirectReportsLibraryErrors(t *testing.T) {
	if _, err := Direct(context.Background(), picked(), &fakeLib{err: context.Canceled}); err == nil {
		t.Error("want an error")
	}
}
