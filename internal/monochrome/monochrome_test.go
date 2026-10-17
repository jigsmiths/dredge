package monochrome

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jigsmiths/dredge/internal/httpretry"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fake serves Monochrome's web client and its tracks API.
type fake struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	pageHits   int
	searches   []string
	statuses   []int // consumed one per search request, then 200
	resets     int   // search requests to drop the connection on, before statuses
	retryAfter string
	search     func(q string) string
	track      func(w http.ResponseWriter, r *http.Request)
	release    func(w http.ResponseWriter, r *http.Request)
	webBroken  bool
	apiPrefix  string
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, apiPrefix: "/api"}
	f.search = func(q string) string { return fixture(t, "search_barracuda.json") }
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.pageHits++
		broken := f.webBroken
		f.mu.Unlock()
		if broken {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, fixture(t, "page.html"))
	})
	mux.HandleFunc("/assets/index-BwwE3IeY.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, strings.ReplaceAll(fixture(t, "bundle.js"), "__API__", f.srv.URL+f.apiPrefix))
	})
	mux.HandleFunc("/old/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `<html><head><meta http-equiv="refresh" content="0; url=%s/"></head></html>`, f.srv.URL)
	})
	mux.HandleFunc("/api/search/tracks", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.searches = append(f.searches, r.URL.Query().Get("q"))
		status := 200
		if len(f.statuses) > 0 {
			status, f.statuses = f.statuses[0], f.statuses[1:]
		}
		reset := f.resets > 0
		if reset {
			f.resets--
		}
		f.mu.Unlock()
		if reset {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		if status != 200 {
			if f.retryAfter != "" {
				w.Header().Set("Retry-After", f.retryAfter)
			}
			w.WriteHeader(status)
			return
		}
		_, _ = fmt.Fprint(w, f.search(r.URL.Query().Get("q")))
	})
	mux.HandleFunc("/api/track/", func(w http.ResponseWriter, r *http.Request) {
		if f.track != nil {
			f.track(w, r)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/releases/", func(w http.ResponseWriter, r *http.Request) {
		if f.release != nil {
			f.release(w, r)
			return
		}
		http.NotFound(w, r)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) client() *Client {
	c := New(f.srv.URL, "", f.srv.Client())
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestFindDiscoversTheAPIAndMatchesByISRC(t *testing.T) {
	f := newFake(t)
	got, err := f.client().Find(context.Background(), "USSM10009075", "Heart", "Barracuda")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "165966798886137856" || got.Title != "Barracuda" || got.ISRC != "USSM10009075" ||
		got.DurationMS != 261933 || got.ReleaseID != "165809194680307712" || got.Artists[0] != "Heart" {
		t.Errorf("track = %+v", got)
	}
	if f.searches[0] != "Heart Barracuda" {
		t.Errorf("first query = %q, want the artist and title", f.searches[0])
	}
}

func TestSearchReturnsPlayableCandidatesWithTheirISRC(t *testing.T) {
	f := newFake(t)
	f.search = func(string) string {
		return `{"tracks":[
			{"id":"1","trackId":"1","title":"Barracuda","artistNames":["Heart"],"isrc":"USSM10009075","playable":true,"duration":261933},
			{"id":"2","trackId":"2","title":"Barracuda (Live)","artistNames":["Heart"],"isrc":"GBCBR1600287","playable":false,"duration":325000},
			{"id":"3","trackId":"3","title":"No ISRC","artistNames":["Heart"],"isrc":"","playable":true,"duration":1},
			{"id":"4","trackId":"4","title":"Crazy On You","artistNames":["Heart","Guest"],"isrc":"USCA28600007","duration":293440}]}`
	}
	got, err := f.client().Search(context.Background(), "Heart barracuda", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want the 2 playable ones with an ISRC: %+v", len(got), got)
	}
	if got[0].ISRC != "USSM10009075" || got[0].Title != "Barracuda" || got[0].Artists[0] != "Heart" || got[0].DurationMS != 261933 {
		t.Errorf("first = %+v", got[0])
	}
	if len(got[1].Artists) != 2 || got[1].ISRC != "USCA28600007" {
		t.Errorf("second = %+v", got[1])
	}
	if len(f.searches) != 1 || f.searches[0] != "Heart barracuda" {
		t.Errorf("queries = %v, want the query passed through as given", f.searches)
	}
}

func TestSearchCandidatesCarryTheTrackToDownload(t *testing.T) {
	f := newFake(t)
	f.search = func(string) string {
		return `{"tracks":[{"id":"1","trackId":"11","title":"Barracuda","artistNames":["Heart"],"isrc":"USSM10009075","playable":true,"duration":261933,"releaseId":"99","artwork":"https://img/x.jpg"}]}`
	}
	got, err := f.client().Search(context.Background(), "Heart barracuda", 25)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v err=%v", got, err)
	}
	tk := TrackFrom(got[0])
	if tk.ID != "11" || tk.ReleaseID != "99" || tk.ArtworkURL != "https://img/x.jpg" ||
		tk.ISRC != "USSM10009075" || tk.Title != "Barracuda" || tk.DurationMS != 261933 || tk.Artists[0] != "Heart" {
		t.Errorf("track = %+v", tk)
	}
}

func TestSearchSurfacesTransportErrors(t *testing.T) {
	f := newFake(t)
	f.statuses = []int{502, 502, 502}
	if _, err := f.client().Search(context.Background(), "x", 5); err == nil {
		t.Fatal("want an error when the API keeps failing")
	}
}

func TestFindIgnoresCaseAndSpaceInTheISRC(t *testing.T) {
	f := newFake(t)
	if _, err := f.client().Find(context.Background(), " ussm10009075 ", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
}

func TestFindNeedsTheExactISRC(t *testing.T) {
	f := newFake(t)
	_, err := f.client().Find(context.Background(), "USXX00000000", "Heart", "Barracuda")
	if !errors.Is(err, ErrNotStocked) {
		t.Fatalf("err = %v, want ErrNotStocked", err)
	}
}

func TestFindFallsBackToTheTitleAlone(t *testing.T) {
	f := newFake(t)
	f.search = func(q string) string {
		if q == "Barracuda" {
			return fixture(t, "search_barracuda.json")
		}
		return `{"tracks":[]}`
	}
	if _, err := f.client().Find(context.Background(), "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(f.searches) != "[Heart Barracuda Barracuda]" {
		t.Errorf("queries = %v", f.searches)
	}
}

func TestFindSkipsUnplayableAndIdlessResults(t *testing.T) {
	f := newFake(t)
	f.search = func(string) string {
		return `{"tracks":[
			{"id":"1","trackId":"1","title":"Barracuda","artistNames":["Heart"],"isrc":"USSM10009075","playable":false,"duration":1},
			{"id":"abc","title":"Barracuda","artistNames":["Heart"],"isrc":"USSM10009075","playable":true,"duration":1},
			{"id":"3","trackId":"3","title":"Barracuda","artistNames":["Heart"],"isrc":"USSM10009075","playable":true,"duration":1}]}`
	}
	got, err := f.client().Find(context.Background(), "USSM10009075", "Heart", "Barracuda")
	if err != nil || got.ID != "3" {
		t.Fatalf("got %+v, %v; want track 3", got, err)
	}
}

func TestGatewayErrorsAreRetried(t *testing.T) {
	f := newFake(t)
	f.statuses = []int{502, 503}
	if _, err := f.client().Find(context.Background(), "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
}

func TestADroppedConnectionIsRetried(t *testing.T) {
	f := newFake(t)
	f.resets = 2
	if _, err := f.client().Find(context.Background(), "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
}

func TestTooManyRequestsWaitsForRetryAfter(t *testing.T) {
	f := newFake(t)
	f.statuses = []int{429}
	f.retryAfter = "2"
	c := f.client()
	var waits []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	if _, err := c.Find(context.Background(), "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0] != 2*time.Second {
		t.Errorf("waits = %v, want one of 2s", waits)
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	f := newFake(t)
	f.statuses = []int{429}
	f.retryAfter = "3600"
	c := f.client()
	var waits []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	if _, err := c.Find(context.Background(), "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0] > httpretry.MaxWait {
		t.Errorf("waits = %v, want at most %v", waits, httpretry.MaxWait)
	}
}

func TestACancelledRequestIsNotRetried(t *testing.T) {
	f := newFake(t)
	f.resets = 10
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.client().Search(ctx, "x", 5); err == nil {
		t.Fatal("want an error")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.searches) > 1 {
		t.Errorf("%d requests after cancel, want no retries", len(f.searches))
	}
}

func TestPersistentGatewayErrorIsNotAMiss(t *testing.T) {
	f := newFake(t)
	f.statuses = []int{502, 502, 502, 502, 502, 502}
	_, err := f.client().Find(context.Background(), "USSM10009075", "Heart", "Barracuda")
	if err == nil || errors.Is(err, ErrNotStocked) {
		t.Fatalf("err = %v, want a transport error that is not ErrNotStocked", err)
	}
}

func TestAPIAddressIsDiscoveredOnceAndRefreshedAfterTheTTL(t *testing.T) {
	f := newFake(t)
	c := f.client()
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()

	for range 2 {
		if _, err := c.Find(ctx, "USSM10009075", "Heart", "Barracuda"); err != nil {
			t.Fatal(err)
		}
	}
	if f.pageHits != 1 {
		t.Fatalf("web client fetched %d times, want 1", f.pageHits)
	}
	now = now.Add(discoveryTTL + time.Minute)
	if _, err := c.Find(ctx, "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
	if f.pageHits != 2 {
		t.Errorf("web client fetched %d times after the TTL, want 2", f.pageHits)
	}
}

func TestFailedRediscoveryKeepsTheLastKnownAddress(t *testing.T) {
	f := newFake(t)
	c := f.client()
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := c.Find(ctx, "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
	f.webBroken = true
	now = now.Add(discoveryTTL + time.Minute)
	if _, err := c.Find(ctx, "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatalf("a broken web client should not break searches: %v", err)
	}
}

func TestDiscoveryFailureWithNothingKnownIsAnError(t *testing.T) {
	f := newFake(t)
	f.webBroken = true
	if _, err := f.client().Find(context.Background(), "USSM10009075", "Heart", "Barracuda"); err == nil {
		t.Fatal("want an error when the API address cannot be found")
	}
}

func TestMetaRefreshToANewDomainIsFollowed(t *testing.T) {
	f := newFake(t)
	c := New(f.srv.URL+"/old/", "", f.srv.Client())
	c.sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := c.Find(context.Background(), "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
}

func TestAnExplicitAPIAddressSkipsDiscovery(t *testing.T) {
	f := newFake(t)
	f.webBroken = true
	c := New(f.srv.URL, f.srv.URL+"/api/", f.srv.Client())
	c.sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := c.Find(context.Background(), "USSM10009075", "Heart", "Barracuda"); err != nil {
		t.Fatal(err)
	}
	if f.pageHits != 0 {
		t.Errorf("web client fetched %d times, want 0", f.pageHits)
	}
}

var flacBytes = append([]byte("fLaC"), bytes.Repeat([]byte{0x42}, 1000)...)

func TestDownloadStreamsTheFLAC(t *testing.T) {
	f := newFake(t)
	f.track = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/track/165966798886137856" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(flacBytes)
	}
	var buf bytes.Buffer
	n, err := f.client().Download(context.Background(), Track{ID: "165966798886137856"}, &buf)
	if err != nil || n != int64(len(flacBytes)) || !bytes.Equal(buf.Bytes(), flacBytes) {
		t.Fatalf("n=%d err=%v equal=%v", n, err, bytes.Equal(buf.Bytes(), flacBytes))
	}
}

func TestDownloadRejectsAnythingThatIsNotFLAC(t *testing.T) {
	f := newFake(t)
	f.track = func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, "<html>rate limited</html>") }
	var buf bytes.Buffer
	if _, err := f.client().Download(context.Background(), Track{ID: "1"}, &buf); err == nil {
		t.Fatal("want an error for a body that is not FLAC")
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes of a rejected download", buf.Len())
	}
}

func TestDownloadNamesALossyOnlyTrack(t *testing.T) {
	f := newFake(t)
	mp4 := append([]byte("\x00\x00\x00\x1cftypisom"), bytes.Repeat([]byte{1}, 100)...)
	f.track = func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(mp4) }
	var buf bytes.Buffer
	_, err := f.client().Download(context.Background(), Track{ID: "1"}, &buf)
	if !errors.Is(err, ErrLossyOnly) {
		t.Fatalf("err = %v, want ErrLossyOnly", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes of a lossy download", buf.Len())
	}
}

func TestDownloadDetectsATruncatedBody(t *testing.T) {
	f := newFake(t)
	f.track = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(flacBytes)))
		_, _ = w.Write(flacBytes[:500])
	}
	var buf bytes.Buffer
	if _, err := f.client().Download(context.Background(), Track{ID: "1"}, &buf); err == nil {
		t.Fatal("want an error when the body is shorter than Content-Length")
	}
}

func TestDownloadNamesTheStatus(t *testing.T) {
	f := newFake(t)
	f.track = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }
	_, err := f.client().Download(context.Background(), Track{ID: "1"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want one naming 404", err)
	}
}

func TestDownloadReportsProgress(t *testing.T) {
	f := newFake(t)
	f.track = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(flacBytes)))
		_, _ = w.Write(flacBytes)
	}
	var last, total int64
	ctx := WithProgress(context.Background(), func(done, tot int64) { last, total = done, tot })
	if _, err := f.client().Download(ctx, Track{ID: "1"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if last != int64(len(flacBytes)) || total != int64(len(flacBytes)) {
		t.Fatalf("last=%d total=%d, want both %d", last, total, len(flacBytes))
	}
}

func TestDownloadGivesUpOnAStalledServer(t *testing.T) {
	f := newFake(t)
	release := make(chan struct{})
	defer close(release)
	f.track = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fLaC"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
	c := f.client()
	c.stall = 50 * time.Millisecond
	_, err := c.Download(context.Background(), Track{ID: "1"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("err = %v, want one saying the download stalled", err)
	}
}

func TestASearchThatNeverAnswersTimesOut(t *testing.T) {
	f := newFake(t)
	release := make(chan struct{})
	defer close(release)
	f.search = func(string) string {
		<-release
		return ""
	}
	c := f.client()
	c.timeout = 50 * time.Millisecond
	c.override = f.srv.URL + "/api"
	done := make(chan error, 1)
	go func() { _, err := c.Search(context.Background(), "x", 5); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("err = nil, want a timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("search still waiting long after the timeout")
	}
}

func TestArtworkFetchesTheImage(t *testing.T) {
	art := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("\xff\xd8\xffjpeg"))
	}))
	defer art.Close()
	got, err := New("", "", art.Client()).Artwork(context.Background(), Track{ArtworkURL: art.URL + "/a.jpg"})
	if err != nil || !bytes.HasPrefix(got, []byte("\xff\xd8\xff")) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestArtworkWithoutAURLIsEmptyNotAnError(t *testing.T) {
	got, err := New("", "", nil).Artwork(context.Background(), Track{})
	if err != nil || got != nil {
		t.Errorf("got %v, %v", got, err)
	}
}
