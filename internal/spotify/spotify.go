// Package spotify reads playlists, albums and tracks the way the web player
// does: an anonymous token from the embed page, then the GraphQL API.
package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jigsmiths/dredge/internal/httpretry"
	"github.com/jigsmiths/dredge/internal/track"
)

const (
	defaultWeb = "https://open.spotify.com"
	defaultAPI = "https://api-partner.spotify.com/pathfinder/v1/query"
	userAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

	playlistPageSize = 100
	albumPageSize    = 50
	tokenMargin      = time.Minute
	maxBody          = 16 << 20
	maxAttempts      = 3
)

// Persisted-query hashes from the web player bundle. They change when the
// player is rebuilt; a rejected one is re-scraped.
var defaultHashes = map[string]string{
	"fetchPlaylist": "8964e8eafb21aa992a7d951d256d83285c04be2105d209262901de70cb97584a",
	"getAlbum":      "11c9cad9cb24c4f9e25ef9456f68621cfd6006a90f679e729c1d2f2fd70dfe16",
	"getTrack":      "a8ef9e9f02b836feb0da3003c31dbb30decc6f4b473ef89ca88c882386d668de",
}

type Kind string

const (
	KindPlaylist Kind = "playlist"
	KindAlbum    Kind = "album"
	KindTrack    Kind = "track"
)

type Ref struct {
	Kind Kind
	ID   string
}

var (
	idRe     = regexp.MustCompile(`^[0-9A-Za-z]{22}$`)
	localeRe = regexp.MustCompile(`^intl-[a-z]{2}(-[a-z]+)?$`)
)

// ParseURL accepts open.spotify.com links and spotify: URIs.
func ParseURL(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	var kind, id string
	if rest, ok := strings.CutPrefix(s, "spotify:"); ok {
		parts := strings.Split(rest, ":")
		if len(parts) != 2 {
			return Ref{}, fmt.Errorf("spotify: unsupported URI %q", s)
		}
		kind, id = parts[0], parts[1]
	} else {
		u, err := url.Parse(s)
		if err != nil || u.Host != "open.spotify.com" {
			return Ref{}, fmt.Errorf("spotify: not a Spotify link: %q", s)
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) > 0 && localeRe.MatchString(parts[0]) {
			parts = parts[1:]
		}
		if len(parts) != 2 {
			return Ref{}, fmt.Errorf("spotify: unsupported link %q", s)
		}
		kind, id = parts[0], parts[1]
	}
	k := Kind(kind)
	if k != KindPlaylist && k != KindAlbum && k != KindTrack {
		return Ref{}, fmt.Errorf("spotify: %q links are not supported", kind)
	}
	if !idRe.MatchString(id) {
		return Ref{}, fmt.Errorf("spotify: invalid id %q", id)
	}
	return Ref{k, id}, nil
}

// Collection is what a link resolves to. Skipped counts playlist entries that
// are not tracks, such as podcast episodes and removed items.
type Collection struct {
	Kind    Kind
	Name    string
	Tracks  []track.Track
	Skipped int
}

type Client struct {
	web, api string
	http     *http.Client
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error

	mu      sync.Mutex
	token   string
	expires time.Time
	hashes  map[string]string
}

// New takes the web and API base URLs, and the HTTP client; empty or nil
// values use the real ones.
func New(web, api string, hc *http.Client) *Client {
	if web == "" {
		web = defaultWeb
	}
	if api == "" {
		api = defaultAPI
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	hashes := make(map[string]string, len(defaultHashes))
	for k, v := range defaultHashes {
		hashes[k] = v
	}
	return &Client{web: strings.TrimRight(web, "/"), api: api, http: hc, now: time.Now, sleep: sleep, hashes: hashes}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) Fetch(ctx context.Context, ref Ref) (*Collection, error) {
	switch ref.Kind {
	case KindPlaylist:
		return c.playlist(ctx, ref)
	case KindAlbum:
		return c.album(ctx, ref)
	case KindTrack:
		return c.track(ctx, ref)
	}
	return nil, fmt.Errorf("spotify: unsupported kind %q", ref.Kind)
}

type artistList struct {
	Items []struct {
		Profile struct {
			Name string `json:"name"`
		} `json:"profile"`
	} `json:"items"`
}

func (a artistList) names() []string {
	out := make([]string, 0, len(a.Items))
	for _, i := range a.Items {
		out = append(out, i.Profile.Name)
	}
	return out
}

type isoDate struct {
	ISOString string `json:"isoString"`
}

type duration struct {
	TotalMilliseconds int `json:"totalMilliseconds"`
}

type trackData struct {
	Typename      string     `json:"__typename"`
	Name          string     `json:"name"`
	URI           string     `json:"uri"`
	DiscNumber    int        `json:"discNumber"`
	TrackNumber   int        `json:"trackNumber"`
	TrackDuration duration   `json:"trackDuration"`
	Duration      duration   `json:"duration"`
	Artists       artistList `json:"artists"`
	AlbumOfTrack  struct {
		Name    string     `json:"name"`
		Date    isoDate    `json:"date"`
		Artists artistList `json:"artists"`
	} `json:"albumOfTrack"`
}

// toTrack takes the album details from the collection when it has them (an
// album fetch), and from the track's own album otherwise.
func (d trackData) toTrack(album, albumArtist, date string) track.Track {
	ms := d.TrackDuration.TotalMilliseconds
	if ms == 0 {
		ms = d.Duration.TotalMilliseconds
	}
	if album == "" {
		album = d.AlbumOfTrack.Name
	}
	if albumArtist == "" {
		if names := d.AlbumOfTrack.Artists.names(); len(names) > 0 {
			albumArtist = names[0]
		}
	}
	if date == "" {
		date = d.AlbumOfTrack.Date.ISOString
	}
	return track.Track{
		Title:       d.Name,
		Artists:     d.Artists.names(),
		Album:       album,
		AlbumArtist: albumArtist,
		Date:        date[:min(len(date), 10)],
		DurationMS:  ms,
		DiscNo:      d.DiscNumber,
		TrackNo:     d.TrackNumber,
		SourceID:    d.URI[strings.LastIndex(d.URI, ":")+1:],
	}
}

func (c *Client) playlist(ctx context.Context, ref Ref) (*Collection, error) {
	var out Collection
	out.Kind = KindPlaylist
	for offset, total := 0, 1; offset < total; {
		var resp struct {
			Data struct {
				PlaylistV2 *struct {
					Name    string `json:"name"`
					Content struct {
						TotalCount int `json:"totalCount"`
						Items      []struct {
							ItemV2 struct {
								Data trackData `json:"data"`
							} `json:"itemV2"`
						} `json:"items"`
					} `json:"content"`
				} `json:"playlistV2"`
			} `json:"data"`
		}
		err := c.query(ctx, ref, "fetchPlaylist", map[string]any{
			"uri": "spotify:playlist:" + ref.ID, "offset": offset, "limit": playlistPageSize, "enableWatchFeedEntrypoint": false,
		}, &resp)
		if err != nil {
			return nil, err
		}
		pl := resp.Data.PlaylistV2
		if pl == nil {
			return nil, errors.New("spotify: response has no playlist")
		}
		out.Name, total = pl.Name, pl.Content.TotalCount
		if len(pl.Content.Items) == 0 {
			return nil, fmt.Errorf("spotify: playlist page at offset %d is empty, expected %d tracks", offset, total)
		}
		for _, it := range pl.Content.Items {
			if it.ItemV2.Data.Typename != "Track" {
				out.Skipped++
				continue
			}
			out.Tracks = append(out.Tracks, it.ItemV2.Data.toTrack("", "", ""))
		}
		offset += len(pl.Content.Items)
	}
	return &out, nil
}

func (c *Client) album(ctx context.Context, ref Ref) (*Collection, error) {
	var out Collection
	out.Kind = KindAlbum
	for offset, total := 0, 1; offset < total; {
		var resp struct {
			Data struct {
				AlbumUnion *struct {
					Name     string     `json:"name"`
					Date     isoDate    `json:"date"`
					Artists  artistList `json:"artists"`
					TracksV2 struct {
						TotalCount int `json:"totalCount"`
						Items      []struct {
							Track trackData `json:"track"`
						} `json:"items"`
					} `json:"tracksV2"`
				} `json:"albumUnion"`
			} `json:"data"`
		}
		err := c.query(ctx, ref, "getAlbum", map[string]any{
			"uri": "spotify:album:" + ref.ID, "locale": "", "offset": offset, "limit": albumPageSize,
		}, &resp)
		if err != nil {
			return nil, err
		}
		al := resp.Data.AlbumUnion
		if al == nil {
			return nil, errors.New("spotify: response has no album")
		}
		out.Name, total = al.Name, al.TracksV2.TotalCount
		if len(al.TracksV2.Items) == 0 {
			return nil, fmt.Errorf("spotify: album page at offset %d is empty, expected %d tracks", offset, total)
		}
		var albumArtist string
		if names := al.Artists.names(); len(names) > 0 {
			albumArtist = names[0]
		}
		for _, it := range al.TracksV2.Items {
			out.Tracks = append(out.Tracks, it.Track.toTrack(al.Name, albumArtist, al.Date.ISOString))
		}
		offset += len(al.TracksV2.Items)
	}
	return &out, nil
}

func (c *Client) track(ctx context.Context, ref Ref) (*Collection, error) {
	var resp struct {
		Data struct {
			TrackUnion *struct {
				trackData
				FirstArtist  artistList `json:"firstArtist"`
				OtherArtists artistList `json:"otherArtists"`
			} `json:"trackUnion"`
		} `json:"data"`
	}
	if err := c.query(ctx, ref, "getTrack", map[string]any{"uri": "spotify:track:" + ref.ID}, &resp); err != nil {
		return nil, err
	}
	tu := resp.Data.TrackUnion
	if tu == nil {
		return nil, errors.New("spotify: response has no track")
	}
	t := tu.toTrack("", "", "")
	t.Artists = append(tu.FirstArtist.names(), tu.OtherArtists.names()...)
	t.SourceID = ref.ID
	return &Collection{Kind: KindTrack, Name: t.Title, Tracks: []track.Track{t}}, nil
}

var errStaleHash = errors.New("PersistedQueryNotFound")

// query retries once with freshly scraped hashes when the API no longer
// knows the current one.
func (c *Client) query(ctx context.Context, ref Ref, op string, vars map[string]any, out any) error {
	body, err := c.queryOnce(ctx, ref, op, vars)
	if errors.Is(err, errStaleHash) {
		if rerr := c.refreshHashes(ctx); rerr != nil {
			return fmt.Errorf("%w (refreshing hashes failed: %w)", err, rerr)
		}
		body, err = c.queryOnce(ctx, ref, op, vars)
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("spotify: %s: invalid response: %w", op, err)
	}
	return nil
}

func (c *Client) queryOnce(ctx context.Context, ref Ref, op string, vars map[string]any) ([]byte, error) {
	token, err := c.bearer(ctx, ref)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	hash := c.hashes[op]
	c.mu.Unlock()
	variables, _ := json.Marshal(vars)
	extensions, _ := json.Marshal(map[string]any{"persistedQuery": map[string]any{"version": 1, "sha256Hash": hash}})
	q := url.Values{"operationName": {op}, "variables": {string(variables)}, "extensions": {string(extensions)}}

	body, status, err := c.get(ctx, c.api+"?"+q.Encode(), map[string]string{
		"Authorization": "Bearer " + token, "app-platform": "WebPlayer", "Accept": "application/json",
	})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("spotify: %s: HTTP %d: %s", op, status, truncate(body))
	}
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("spotify: %s: invalid response: %w", op, err)
	}
	for _, e := range env.Errors {
		if strings.Contains(e.Message, "PersistedQueryNotFound") {
			return nil, fmt.Errorf("spotify: %s: %w", op, errStaleHash)
		}
	}
	if len(env.Errors) > 0 {
		return nil, fmt.Errorf("spotify: %s: %s", op, env.Errors[0].Message)
	}
	return body, nil
}

var nextDataRe = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`)

// bearer returns the anonymous token the embed page ships for its own player.
func (c *Client) bearer(ctx context.Context, ref Ref) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Before(c.expires.Add(-tokenMargin)) {
		return c.token, nil
	}
	body, status, err := c.get(ctx, fmt.Sprintf("%s/embed/%s/%s", c.web, ref.Kind, ref.ID), nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("spotify: embed page: HTTP %d", status)
	}
	m := nextDataRe.FindSubmatch(body)
	if m == nil {
		return "", errors.New("spotify: no access token: embed page has no page data")
	}
	var data struct {
		Props struct {
			PageProps struct {
				State struct {
					Settings struct {
						Session struct {
							AccessToken string `json:"accessToken"`
							ExpiresMS   int64  `json:"accessTokenExpirationTimestampMs"`
						} `json:"session"`
					} `json:"settings"`
				} `json:"state"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(m[1], &data); err != nil {
		return "", fmt.Errorf("spotify: no access token: %w", err)
	}
	s := data.Props.PageProps.State.Settings.Session
	if s.AccessToken == "" {
		return "", errors.New("spotify: no access token in the embed page")
	}
	c.token, c.expires = s.AccessToken, time.UnixMilli(s.ExpiresMS)
	return c.token, nil
}

var (
	playerScriptRe = regexp.MustCompile(`src="([^"]*/web-player\.[0-9a-f]+\.js)"`)
	hashRe         = regexp.MustCompile(`\("(fetchPlaylist|getAlbum|getTrack)","query","([0-9a-f]{64})"`)
)

func scrapeHashes(js string) map[string]string {
	out := map[string]string{}
	for _, m := range hashRe.FindAllStringSubmatch(js, -1) {
		out[m[1]] = m[2]
	}
	return out
}

func (c *Client) refreshHashes(ctx context.Context) error {
	page, status, err := c.get(ctx, c.web+"/", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("web player page: HTTP %d", status)
	}
	m := playerScriptRe.FindSubmatch(page)
	if m == nil {
		return errors.New("no web player bundle on the page")
	}
	base, err := url.Parse(c.web + "/")
	if err != nil {
		return err
	}
	ref, err := url.Parse(string(m[1]))
	if err != nil {
		return err
	}
	js, status, err := c.get(ctx, base.ResolveReference(ref).String(), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("web player bundle: HTTP %d", status)
	}
	found := scrapeHashes(string(js))
	if len(found) == 0 {
		return errors.New("no query hashes in the web player bundle")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for op, h := range found {
		c.hashes[op] = h
	}
	return nil
}

// get retries dropped connections and busy servers, then returns the last status.
func (c *Client) get(ctx context.Context, u string, headers map[string]string) ([]byte, int, error) {
	var (
		body   []byte
		status int
		err    error
		wait   time.Duration
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if serr := c.sleep(ctx, wait); serr != nil {
				return nil, 0, serr
			}
		}
		wait = time.Duration(attempt) * 500 * time.Millisecond
		var resp *http.Response
		body, status, resp, err = c.getOnce(ctx, u, headers)
		if ctx.Err() != nil || (err == nil && !httpretry.Transient(status)) {
			break
		}
		wait = httpretry.Wait(resp, wait)
	}
	return body, status, err
}

func (c *Client) getOnce(ctx context.Context, u string, headers map[string]string) ([]byte, int, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return body, resp.StatusCode, resp, err
}

func truncate(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}
