package deezer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jigsmiths/dredge/internal/track"
)

type fake struct {
	srv     *httptest.Server
	mu      sync.Mutex
	queries []string
	replies []func(w http.ResponseWriter) // one per request, then the fixture
}

func newFake(t *testing.T) (*fake, *Client) {
	t.Helper()
	body, err := os.ReadFile("testdata/search_stop_the_wedding.json")
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.queries = append(f.queries, r.URL.Query().Get("q"))
		var reply func(http.ResponseWriter)
		if len(f.replies) > 0 {
			reply, f.replies = f.replies[0], f.replies[1:]
		}
		f.mu.Unlock()
		if reply != nil {
			reply(w)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.srv.Close)
	c := New(f.srv.URL, nil)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return f, c
}

func TestLookupFillsInTheOneRecording(t *testing.T) {
	f, c := newFake(t)
	got, ok, err := c.Lookup(context.Background(), track.Track{Title: "stop the wedding", Artists: []string{"ashe"}})
	if err != nil || !ok {
		t.Fatalf("Lookup = %v %v", ok, err)
	}
	want := track.Track{Title: "Stop The Wedding!", Artists: []string{"Ashe"}, Album: "Stop The Wedding!", DurationMS: 198000, ISRC: "USAT22604492"}
	if got.Title != want.Title || strings.Join(got.Artists, ",") != "Ashe" || got.Album != want.Album || got.DurationMS != want.DurationMS || got.ISRC != want.ISRC {
		t.Errorf("Lookup = %+v, want %+v", got, want)
	}
	if len(f.queries) != 1 || f.queries[0] != "ashe stop the wedding" {
		t.Errorf("queries = %q", f.queries)
	}
}

func TestLookupFindsNothingForAnotherSong(t *testing.T) {
	_, c := newFake(t)
	in := track.Track{Title: "Moral of the Story", Artists: []string{"Ashe"}}
	got, ok, err := c.Lookup(context.Background(), in)
	if err != nil || ok || got.ISRC != "" || got.Title != in.Title {
		t.Errorf("Lookup = %+v %v %v, want the track back unchanged", got, ok, err)
	}
}

func TestLookupRetriesARateLimit(t *testing.T) {
	f, c := newFake(t)
	f.replies = []func(http.ResponseWriter){
		func(w http.ResponseWriter) { http.Error(w, "busy", http.StatusServiceUnavailable) },
		func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"error":{"type":"Exception","message":"Quota limit exceeded","code":4}}`))
		},
	}
	if _, ok, err := c.Lookup(context.Background(), track.Track{Title: "Stop the Wedding", Artists: []string{"Ashe"}}); err != nil || !ok {
		t.Errorf("Lookup = %v %v, want a match on the third try", ok, err)
	}
	if len(f.queries) != 3 {
		t.Errorf("requests = %d, want 3", len(f.queries))
	}
}

func TestLookupReportsAnAPIError(t *testing.T) {
	f, c := newFake(t)
	f.replies = []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"error":{"type":"DataException","message":"no data","code":800}}`))
		},
		func(w http.ResponseWriter) { http.Error(w, "nope", http.StatusForbidden) },
	}
	for range 2 {
		if _, _, err := c.Lookup(context.Background(), track.Track{Title: "x", Artists: []string{"y"}}); err == nil {
			t.Error("Lookup succeeded, want an error")
		}
	}
	if len(f.queries) != 2 {
		t.Errorf("requests = %d, want no retries", len(f.queries))
	}
}
