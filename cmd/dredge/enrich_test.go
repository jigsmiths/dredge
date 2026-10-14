package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigsmiths/dredge/internal/flacmeta"
	"github.com/jigsmiths/dredge/internal/monochrome"
)

type fakeDetails struct{ err error }

func (f fakeDetails) Details(context.Context, string, string, string) (monochrome.Details, error) {
	return monochrome.Details{Album: "Little Queen", AlbumArtist: "Heart", Date: "1977-05-14", TrackNo: 1, DiscNo: 1}, f.err
}

func TestSearchGetTagsThePickedTrackWithItsAlbum(t *testing.T) {
	r := newRig(t)
	withHits(r)
	r.details = fakeDetails{}
	if err := r.run("search", "barracuda", "--get", "1"); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(r.root, "Singles/Heart/Barracuda.flac"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	tags, err := flacmeta.Read(f)
	if err != nil {
		t.Fatal(err)
	}
	if tags.Album != "Little Queen" || tags.AlbumArtist != "Heart" || tags.Date != "1977-05-14" || tags.TrackNo != 1 {
		t.Errorf("tags = %+v", tags)
	}
}

func TestSearchGetStillDownloadsWhenTheAlbumLookupFails(t *testing.T) {
	r := newRig(t)
	withHits(r)
	r.details = fakeDetails{err: errors.New("releases down")}
	if err := r.run("search", "barracuda", "--get", "1"); err != nil {
		t.Fatal(err)
	}
	if !r.exists("Singles/Heart/Barracuda.flac") || !strings.Contains(r.errw.String(), "no album details") {
		t.Errorf("stderr: %s", r.errw.String())
	}
}
