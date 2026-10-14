package deezer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/track"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// serve answers every request with handler and returns a client for it that
// never really sleeps.
func serve(t *testing.T, handler http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(srv.URL, srv.Client())
	c.interval = 0
	var slept []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	return c, &slept
}

func TestSearchMapsFields(t *testing.T) {
	var gotPath, gotQ, gotLimit string
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQ, gotLimit = r.URL.Path, r.URL.Query().Get("q"), r.URL.Query().Get("limit")
		_, _ = fmt.Fprint(w, fixture(t, "search_barracuda.json"))
	})
	got, err := c.Search(context.Background(), "Heart Barracuda", 5)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/search/track" || gotQ != "Heart Barracuda" || gotLimit != "5" {
		t.Errorf("request = %s q=%q limit=%q", gotPath, gotQ, gotLimit)
	}
	if len(got) != 5 {
		t.Fatalf("got %d candidates, want 5", len(got))
	}
	want := match.Candidate{
		ISRC: "USSM10009075", Title: "Barracuda", Artists: []string{"Heart"}, Album: "Little Queen", DurationMS: 264000,
	}
	if fmt.Sprint(got[0]) != fmt.Sprint(want) {
		t.Errorf("first = %+v, want %+v", got[0], want)
	}
}

func TestRealResultsPickTheAlbumRecording(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, fixture(t, "search_barracuda.json"))
	})
	cands, err := c.Search(context.Background(), "Heart Barracuda", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := track.Track{Title: "Barracuda", Artists: []string{"Heart"}, Album: "Little Queen", DurationMS: 261495}
	got := match.Pick(want, cands)
	if got.Outcome != match.Matched || got.Best.ISRC != "USSM10009075" {
		t.Errorf("Pick = %v %s (%s)", got.Outcome, got.Best.ISRC, got.Reason)
	}
}

func TestSearchWithNoResults(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, `{"data":[],"total":0}`) })
	got, err := c.Search(context.Background(), "nothing", 5)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want no candidates and no error", got, err)
	}
}

func TestQuotaErrorIsRetried(t *testing.T) {
	var calls atomic.Int32
	c, slept := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			_, _ = fmt.Fprint(w, `{"error":{"type":"Exception","message":"Quota limit exceeded","code":4}}`)
			return
		}
		_, _ = fmt.Fprint(w, fixture(t, "search_barracuda.json"))
	})
	got, err := c.Search(context.Background(), "Heart Barracuda", 5)
	if err != nil || len(got) != 5 {
		t.Fatalf("got %d candidates, err %v", len(got), err)
	}
	if calls.Load() != 3 || len(*slept) != 2 || (*slept)[1] <= (*slept)[0] {
		t.Errorf("calls=%d slept=%v, want 3 calls with growing waits", calls.Load(), *slept)
	}
}

func TestQuotaThatNeverClearsGivesUp(t *testing.T) {
	var calls atomic.Int32
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"error":{"type":"Exception","message":"Quota limit exceeded","code":4}}`)
	})
	_, err := c.Search(context.Background(), "x", 5)
	if err == nil || !strings.Contains(err.Error(), "Quota") {
		t.Fatalf("err = %v, want one naming the quota", err)
	}
	if calls.Load() != int32(maxAttempts) {
		t.Errorf("made %d calls, want %d", calls.Load(), maxAttempts)
	}
}

func TestServerErrorIsRetried(t *testing.T) {
	var calls atomic.Int32
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprint(w, fixture(t, "search_barracuda.json"))
	})
	if got, err := c.Search(context.Background(), "x", 5); err != nil || len(got) != 5 {
		t.Fatalf("got %d candidates, err %v", len(got), err)
	}
}

func TestOtherErrorsAreNotRetried(t *testing.T) {
	var calls atomic.Int32
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"error":{"type":"DataException","message":"no data","code":800}}`)
	})
	if _, err := c.Search(context.Background(), "x", 5); err == nil {
		t.Fatal("want an error")
	}
	if calls.Load() != 1 {
		t.Errorf("made %d calls, want 1", calls.Load())
	}
}

func TestGarbageIsAnError(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, "<html>") })
	if _, err := c.Search(context.Background(), "x", 5); err == nil {
		t.Error("want an error for a body that is not JSON")
	}
}

func TestRequestsAreSpacedOut(t *testing.T) {
	c, slept := serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, `{"data":[]}`) })
	c.interval = 100 * time.Millisecond
	for range 3 {
		if _, err := c.Search(context.Background(), "x", 1); err != nil {
			t.Fatal(err)
		}
	}
	if len(*slept) < 2 {
		t.Errorf("slept %v, want a wait before the second and third request", *slept)
	}
}
