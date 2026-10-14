package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jigsmiths/dredge/internal/flacmeta"
	"github.com/jigsmiths/dredge/internal/monochrome"
)

type fakeDetails struct{ err error }

func (f fakeDetails) Details(context.Context, string, string, string) (monochrome.Details, error) {
	return monochrome.Details{Album: "Little Queen", AlbumArtist: "Heart", Date: "1977-05-14", TrackNo: 1, DiscNo: 1}, f.err
}

func fileTags(t *testing.T, path string) flacmeta.Tags {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	tags, err := flacmeta.Read(f)
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

func TestAPickedTrackIsTaggedWithItsAlbum(t *testing.T) {
	r := newRig(t)
	r.src.stocked[stocked] = true
	r.m.deps.Details = fakeDetails{}
	r.start()
	j, _, _ := r.submitTrack(stocked, "Barracuda", "Heart")
	r.waitFor(j.ID, Done)

	row := r.tracks(j.ID)["Barracuda"]
	if row.Album != "Little Queen" || row.Status != TrackDone {
		t.Errorf("row = %+v", row)
	}
	tags := fileTags(t, filepath.Join(r.root, "Singles/Heart/Barracuda.flac"))
	if tags.Album != "Little Queen" || tags.AlbumArtist != "Heart" || tags.Date != "1977-05-14" || tags.TrackNo != 1 || tags.DiscNo != 1 {
		t.Errorf("tags = %+v", tags)
	}
}

func TestAPickedTrackStillDownloadsWhenTheAlbumLookupFails(t *testing.T) {
	r := newRig(t)
	r.src.stocked[stocked] = true
	r.m.deps.Details = fakeDetails{err: errors.New("releases down")}
	r.start()
	j, _, _ := r.submitTrack(stocked, "Barracuda", "Heart")
	r.waitFor(j.ID, Done)
	if row := r.tracks(j.ID)["Barracuda"]; row.Status != TrackDone || row.Album != "" {
		t.Errorf("row = %+v", row)
	}
}
