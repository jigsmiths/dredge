package monochrome

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func serveRelease(t *testing.T, f *fake) {
	f.release = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/releases/165809194680307712" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, fixture(t, "release_little_queen.json"))
	}
}

func TestDetailsGivesTheAlbumDateAndTrackPosition(t *testing.T) {
	f := newFake(t)
	serveRelease(t, f)
	d, err := f.client().Details(context.Background(), "USSM10009075", "Heart", "Barracuda")
	if err != nil {
		t.Fatal(err)
	}
	if d.Album != "Little Queen" || d.AlbumArtist != "Heart" || d.Date != "1977-05-14" || d.TrackNo != 1 || d.DiscNo != 1 {
		t.Errorf("details = %+v", d)
	}
}

func TestDetailsOfALaterDisc(t *testing.T) {
	f := newFake(t)
	f.search = func(string) string {
		return `{"tracks":[{"id":"165966798886138000","trackId":"165966798886138000","title":"Bonus","artistNames":["Heart"],
			"releaseId":"165809194680307712","playable":true,"duration":1000,"isrc":"USSM10009099"}]}`
	}
	serveRelease(t, f)
	d, err := f.client().Details(context.Background(), "USSM10009099", "Heart", "Bonus")
	if err != nil || d.DiscNo != 2 || d.TrackNo != 2 {
		t.Errorf("details = %+v err=%v", d, err)
	}
}

func TestDetailsOfATrackTheCatalogueLacks(t *testing.T) {
	f := newFake(t)
	serveRelease(t, f)
	if _, err := f.client().Details(context.Background(), "USZZ00000000", "Heart", "Barracuda"); !errors.Is(err, ErrNotStocked) {
		t.Errorf("err = %v, want ErrNotStocked", err)
	}
}

func TestDetailsFailsWhenTheReleaseCannotBeRead(t *testing.T) {
	f := newFake(t) // no release route: 404
	if _, err := f.client().Details(context.Background(), "USSM10009075", "Heart", "Barracuda"); err == nil {
		t.Error("want an error")
	}
}

func TestDetailsKeepsTheAlbumWhenTheTrackIsNotListedInIt(t *testing.T) {
	f := newFake(t)
	f.release = func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"title":"Little Queen","artists":[{"name":"Heart"}],"releaseDate":"1977-05-14T00:00:00.000Z","tracks":[]}`)
	}
	d, err := f.client().Details(context.Background(), "USSM10009075", "Heart", "Barracuda")
	if err != nil || d.Album != "Little Queen" || d.TrackNo != 0 {
		t.Errorf("details = %+v err=%v", d, err)
	}
}

func TestDetailsJoinsSeveralAlbumArtists(t *testing.T) {
	f := newFake(t)
	f.release = func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"title":"Duets","artists":[{"name":"A"},{"name":"B"}],"releaseDate":"2001","tracks":[]}`)
	}
	d, _ := f.client().Details(context.Background(), "USSM10009075", "Heart", "Barracuda")
	if d.AlbumArtist != "A, B" || d.Date != "2001" {
		t.Errorf("details = %+v", d)
	}
}
