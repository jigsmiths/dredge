package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		in   string
		want Ref
		bad  bool
	}{
		{"https://open.spotify.com/playlist/37i9dQZF1DWXRqgorJj26U", Ref{KindPlaylist, "37i9dQZF1DWXRqgorJj26U"}, false},
		{"https://open.spotify.com/playlist/37i9dQZF1DWXRqgorJj26U?si=abc123", Ref{KindPlaylist, "37i9dQZF1DWXRqgorJj26U"}, false},
		{"https://open.spotify.com/intl-de/playlist/37i9dQZF1DWXRqgorJj26U", Ref{KindPlaylist, "37i9dQZF1DWXRqgorJj26U"}, false},
		{"https://open.spotify.com/album/1LaeNhiUpL3X6N0LcFvuDF", Ref{KindAlbum, "1LaeNhiUpL3X6N0LcFvuDF"}, false},
		{"https://open.spotify.com/track/7e4zDInS6tA2jwzphvs2Ay?si=x", Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"}, false},
		{"spotify:playlist:37i9dQZF1DWXRqgorJj26U", Ref{KindPlaylist, "37i9dQZF1DWXRqgorJj26U"}, false},
		{"spotify:album:1LaeNhiUpL3X6N0LcFvuDF", Ref{KindAlbum, "1LaeNhiUpL3X6N0LcFvuDF"}, false},
		{"  https://open.spotify.com/track/7e4zDInS6tA2jwzphvs2Ay  ", Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"}, false},
		{"https://open.spotify.com/artist/5PlfkPxwCpRRWQJBxCa0By", Ref{}, true},
		{"https://example.com/playlist/37i9dQZF1DWXRqgorJj26U", Ref{}, true},
		{"https://open.spotify.com/playlist/", Ref{}, true},
		{"https://open.spotify.com/playlist/not/an/id", Ref{}, true},
		{"Heart - Barracuda", Ref{}, true},
		{"", Ref{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseURL(tt.in)
			if tt.bad {
				if err == nil {
					t.Fatalf("ParseURL(%q) = %+v, want an error", tt.in, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ParseURL(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
			}
		})
	}
}

// fake stands in for open.spotify.com and the pathfinder API.
type fake struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	embedHits   int
	scrapeHits  int
	queries     []query
	goodHashes  map[string]string // operation -> hash the API accepts
	tokenExpiry time.Time
	respond     func(q query) (int, string) // nil serves fixtures
}

type query struct {
	Op     string
	Hash   string
	Vars   map[string]any
	Bearer string
	Plat   string
}

const fakeToken = "test-token"

func newFake(t *testing.T) *fake {
	f := &fake{t: t, tokenExpiry: time.Now().Add(time.Hour), goodHashes: map[string]string{
		"fetchPlaylist": "pl-hash", "getAlbum": "al-hash", "getTrack": "tr-hash",
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/embed/", f.embed)
	mux.HandleFunc("/pathfinder/v1/query", f.query)
	mux.HandleFunc("/cdn/web-player.abc123.js", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.scrapeHits++
		f.mu.Unlock()
		_, _ = fmt.Fprintf(w, `a=new x.l("fetchPlaylist","query","%s",null),b=new x.l("getAlbum","query","%s",null),c=new x.l("getTrack","query","%s",null);`,
			hex64("pl-hash"), hex64("al-hash"), hex64("tr-hash"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `<html><script src="%s/cdn/encore~web-player.zzz999.js"></script><script src="%s/cdn/web-player.abc123.js"></script></html>`, f.srv.URL, f.srv.URL)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// hex64 pads a short name out to a persisted-query hash.
func hex64(s string) string {
	h := fmt.Sprintf("%x", s)
	return h + strings.Repeat("0", 64-len(h))
}

func (f *fake) client() *Client {
	c := New(f.srv.URL, f.srv.URL+"/pathfinder/v1/query", f.srv.Client())
	for op := range f.goodHashes {
		c.hashes[op] = hex64(f.goodHashes[op])
	}
	return c
}

func (f *fake) embed(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.embedHits++
	exp := f.tokenExpiry
	f.mu.Unlock()
	_, _ = fmt.Fprintf(w, `<html><script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"state":{"settings":{"session":{"accessToken":%q,"accessTokenExpirationTimestampMs":%d}}}}}}</script></html>`,
		fakeToken, exp.UnixMilli())
}

func (f *fake) query(w http.ResponseWriter, r *http.Request) {
	q := query{
		Op:     r.URL.Query().Get("operationName"),
		Bearer: r.Header.Get("Authorization"),
		Plat:   r.Header.Get("app-platform"),
	}
	var ext struct {
		PersistedQuery struct {
			Hash string `json:"sha256Hash"`
		} `json:"persistedQuery"`
	}
	_ = json.Unmarshal([]byte(r.URL.Query().Get("extensions")), &ext)
	q.Hash = ext.PersistedQuery.Hash
	_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &q.Vars)

	f.mu.Lock()
	f.queries = append(f.queries, q)
	respond := f.respond
	good := f.goodHashes[q.Op]
	f.mu.Unlock()

	if q.Hash != hex64(good) {
		_, _ = fmt.Fprint(w, `{"errors":[{"message":"PersistedQueryNotFound"}]}`)
		return
	}
	status, body := http.StatusOK, ""
	if respond != nil {
		status, body = respond(q)
	} else {
		body = fixture(f.t, map[string]string{
			"fetchPlaylist": "playlist_page.json", "getAlbum": "album.json", "getTrack": "track.json",
		}[q.Op])
	}
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, body)
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// playlistPage builds a response page of a total-track playlist.
func playlistPage(total int, q query) string {
	offset, limit := int(q.Vars["offset"].(float64)), int(q.Vars["limit"].(float64))
	var items []string
	for i := offset; i < offset+limit && i < total; i++ {
		items = append(items, fmt.Sprintf(`{"itemV2":{"__typename":"TrackResponseWrapper","data":{"__typename":"Track","name":"Song %d","uri":"spotify:track:id%d","discNumber":1,"trackNumber":%d,"trackDuration":{"totalMilliseconds":200000},"artists":{"items":[{"profile":{"name":"Artist"},"uri":"spotify:artist:a"}]},"albumOfTrack":{"name":"Album","uri":"spotify:album:b","date":{"isoString":"2020-01-01T00:00:00Z"},"artists":{"items":[]}}}}}`, i, i, i+1))
	}
	return fmt.Sprintf(`{"data":{"playlistV2":{"name":"Big","content":{"totalCount":%d,"items":[%s]}}}}`, total, strings.Join(items, ","))
}

func TestFetchPlaylistParsesTracks(t *testing.T) {
	f := newFake(t)
	// the recorded page holds 3 of 200 tracks; serve it as the whole playlist
	body := strings.Replace(fixture(t, "playlist_page.json"), `"totalCount": 200`, `"totalCount": 3`, 1)
	f.respond = func(q query) (int, string) { return 200, body }

	got, err := f.client().Fetch(context.Background(), Ref{KindPlaylist, "37i9dQZF1DWXRqgorJj26U"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Rock Classics" || got.Kind != KindPlaylist {
		t.Errorf("collection = %q %v", got.Name, got.Kind)
	}
	if len(got.Tracks) != 3 {
		t.Fatalf("got %d tracks, want 3", len(got.Tracks))
	}
	first := got.Tracks[0]
	if first.Title != "Barracuda" || len(first.Artists) != 1 || first.Artists[0] != "Heart" ||
		first.Album != "Little Queen" || first.DurationMS != 261495 || first.SourceID != "4KfSdst7rW39C0sfhArdrz" ||
		first.DiscNo != 1 || first.TrackNo != 1 {
		t.Errorf("first track = %+v", first)
	}
	if got.Tracks[2].Title != "My Sharona" || got.Tracks[2].TrackNo != 7 {
		t.Errorf("third track = %+v", got.Tracks[2])
	}
}

func fixtureEmptyPage() string {
	return `{"data":{"playlistV2":{"name":"x","content":{"totalCount":200,"items":[]}}}}`
}

func TestFetchPlaylistPages(t *testing.T) {
	f := newFake(t)
	f.respond = func(q query) (int, string) { return 200, playlistPage(250, q) }
	got, err := f.client().Fetch(context.Background(), Ref{KindPlaylist, "id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tracks) != 250 || got.Tracks[0].Title != "Song 0" || got.Tracks[249].Title != "Song 249" {
		t.Fatalf("got %d tracks, first %q, last %q", len(got.Tracks), got.Tracks[0].Title, got.Tracks[len(got.Tracks)-1].Title)
	}
	var offsets []int
	for _, q := range f.queries {
		offsets = append(offsets, int(q.Vars["offset"].(float64)))
		if lim := int(q.Vars["limit"].(float64)); lim > 100 {
			t.Errorf("page limit %d, want at most 100", lim)
		}
	}
	if fmt.Sprint(offsets) != "[0 100 200]" {
		t.Errorf("offsets = %v, want [0 100 200]", offsets)
	}
}

func TestFetchPlaylistEmptyPageIsAnErrorNotAPartialList(t *testing.T) {
	f := newFake(t)
	f.respond = func(q query) (int, string) {
		if q.Vars["offset"].(float64) >= 100 {
			return 200, fixtureEmptyPage()
		}
		return 200, playlistPage(250, q)
	}
	if _, err := f.client().Fetch(context.Background(), Ref{KindPlaylist, "id"}); err == nil {
		t.Fatal("want an error when a page comes back empty before the total is reached")
	}
}

func TestFetchPlaylistSkipsItemsThatAreNotTracks(t *testing.T) {
	f := newFake(t)
	f.respond = func(q query) (int, string) {
		return 200, `{"data":{"playlistV2":{"name":"Mixed","content":{"totalCount":3,"items":[
			{"itemV2":{"__typename":"TrackResponseWrapper","data":{"__typename":"Track","name":"Real","uri":"spotify:track:aaa","trackDuration":{"totalMilliseconds":1000},"artists":{"items":[{"profile":{"name":"A"}}]},"albumOfTrack":{"name":"X"}}}},
			{"itemV2":{"__typename":"EpisodeResponseWrapper","data":{"__typename":"Episode","name":"A podcast"}}},
			{"itemV2":{"__typename":"NotFoundResponseWrapper","data":{"__typename":"NotFound"}}}
		]}}}}`
	}
	got, err := f.client().Fetch(context.Background(), Ref{KindPlaylist, "id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tracks) != 1 || got.Tracks[0].Title != "Real" || got.Skipped != 2 {
		t.Errorf("tracks = %d, skipped = %d", len(got.Tracks), got.Skipped)
	}
}

func TestFetchAlbumFixture(t *testing.T) {
	f := newFake(t)
	got, err := f.client().Fetch(context.Background(), Ref{KindAlbum, "1LaeNhiUpL3X6N0LcFvuDF"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Little Queen" || got.Kind != KindAlbum || len(got.Tracks) != 10 {
		t.Fatalf("collection = %q %v with %d tracks", got.Name, got.Kind, len(got.Tracks))
	}
	first := got.Tracks[0]
	if first.Title != "Barracuda" || first.Album != "Little Queen" || first.Artists[0] != "Heart" ||
		first.DurationMS != 261495 || first.DiscNo != 1 || first.TrackNo != 1 || first.SourceID != "4KfSdst7rW39C0sfhArdrz" {
		t.Errorf("first track = %+v", first)
	}
}

func TestFetchTrackFixture(t *testing.T) {
	f := newFake(t)
	got, err := f.client().Fetch(context.Background(), Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(got.Tracks))
	}
	tr := got.Tracks[0]
	if tr.Title != "Jamaican (Bam Bam)" || fmt.Sprint(tr.Artists) != "[HUGEL SOLTO (FR)]" ||
		tr.Album != "Jamaican (Bam Bam)" || tr.DurationMS != 156393 || tr.SourceID != "7e4zDInS6tA2jwzphvs2Ay" {
		t.Errorf("track = %+v", tr)
	}
}

func TestRequestsCarryTheTokenAndPlatformHeader(t *testing.T) {
	f := newFake(t)
	if _, err := f.client().Fetch(context.Background(), Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"}); err != nil {
		t.Fatal(err)
	}
	q := f.queries[0]
	if q.Bearer != "Bearer "+fakeToken || q.Plat != "WebPlayer" || q.Op != "getTrack" {
		t.Errorf("query = %+v", q)
	}
}

func TestTokenIsCachedUntilItExpires(t *testing.T) {
	f := newFake(t)
	c := f.client()
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()

	for range 2 {
		if _, err := c.Fetch(ctx, Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"}); err != nil {
			t.Fatal(err)
		}
	}
	if f.embedHits != 1 {
		t.Fatalf("embed fetched %d times, want 1", f.embedHits)
	}

	now = now.Add(2 * time.Hour)
	f.tokenExpiry = now.Add(time.Hour)
	if _, err := c.Fetch(ctx, Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"}); err != nil {
		t.Fatal(err)
	}
	if f.embedHits != 2 {
		t.Errorf("embed fetched %d times after expiry, want 2", f.embedHits)
	}
}

func TestStaleHashIsRescrapedOnce(t *testing.T) {
	f := newFake(t)
	c := f.client()
	c.hashes["getTrack"] = hex64("stale")
	ctx := context.Background()

	if _, err := c.Fetch(ctx, Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"}); err != nil {
		t.Fatal(err)
	}
	if f.scrapeHits != 1 {
		t.Errorf("player bundle fetched %d times, want 1", f.scrapeHits)
	}
	if n := len(f.queries); n != 2 || f.queries[0].Hash != hex64("stale") || f.queries[1].Hash != hex64("tr-hash") {
		t.Errorf("queries = %+v", f.queries)
	}

	if _, err := c.Fetch(ctx, Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"}); err != nil {
		t.Fatal(err)
	}
	if f.scrapeHits != 1 || len(f.queries) != 3 {
		t.Errorf("second fetch rescraped: scrapes=%d queries=%d", f.scrapeHits, len(f.queries))
	}
}

func TestStaleHashThatStaysStaleFailsAfterOneRetry(t *testing.T) {
	f := newFake(t)
	c := f.client()
	f.goodHashes["getTrack"] = "somewhere-else" // the scraped hash is rejected too
	_, err := c.Fetch(context.Background(), Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"})
	if err == nil || !strings.Contains(err.Error(), "PersistedQueryNotFound") {
		t.Fatalf("err = %v, want one naming PersistedQueryNotFound", err)
	}
	if len(f.queries) != 2 {
		t.Errorf("made %d queries, want 2 (the original and one retry)", len(f.queries))
	}
}

func TestHTTPErrorNamesTheStatus(t *testing.T) {
	f := newFake(t)
	f.respond = func(q query) (int, string) { return http.StatusTooManyRequests, "slow down" }
	_, err := f.client().Fetch(context.Background(), Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want one naming 429", err)
	}
}

func TestMissingTokenIsAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, "<html>no data here</html>") })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, srv.URL+"/q", srv.Client())
	_, err := c.Fetch(context.Background(), Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"})
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("err = %v, want one mentioning the token", err)
	}
}

func TestGarbageResponseIsAnErrorNotAPanic(t *testing.T) {
	f := newFake(t)
	f.respond = func(q query) (int, string) { return 200, `{"data":{"playlistV2":null}}` }
	if _, err := f.client().Fetch(context.Background(), Ref{KindPlaylist, "id"}); err == nil {
		t.Error("want an error for a response with no playlist")
	}
	f.respond = func(q query) (int, string) { return 200, `not json` }
	if _, err := f.client().Fetch(context.Background(), Ref{KindTrack, "id"}); err == nil {
		t.Error("want an error for a response that is not JSON")
	}
}

func TestScrapedHashesComeFromTheRealBundleShape(t *testing.T) {
	b, err := os.ReadFile("testdata/web-player-snippet.js")
	if err != nil {
		t.Fatal(err)
	}
	got := scrapeHashes(string(b))
	want := map[string]string{
		"fetchPlaylist": "8964e8eafb21aa992a7d951d256d83285c04be2105d209262901de70cb97584a",
		"getAlbum":      "11c9cad9cb24c4f9e25ef9456f68621cfd6006a90f679e729c1d2f2fd70dfe16",
		"getTrack":      "a8ef9e9f02b836feb0da3003c31dbb30decc6f4b473ef89ca88c882386d668de",
	}
	for op, h := range want {
		if got[op] != h {
			t.Errorf("hash for %s = %q, want %q", op, got[op], h)
		}
	}
}
