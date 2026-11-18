// Package deezer looks up a song's details (length, ISRC, album) in Deezer's
// public catalogue. It needs no account and downloads nothing.
package deezer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jigsmiths/dredge/internal/httpretry"
	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/track"
)

const (
	defaultAPI     = "https://api.deezer.com"
	maxAttempts    = 3
	maxPage        = 4 << 20
	requestTimeout = 15 * time.Second
	searchLimit    = 25
)

type Client struct {
	api     string
	http    *http.Client
	sleep   func(context.Context, time.Duration) error
	timeout time.Duration
}

// New takes the API address, empty for the real one, and an optional client.
func New(api string, hc *http.Client) *Client {
	if api == "" {
		api = defaultAPI
	}
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{api: strings.TrimRight(api, "/"), http: hc, sleep: sleep, timeout: requestTimeout}
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

type result struct {
	Title    string `json:"title"`
	ISRC     string `json:"isrc"`
	Duration int    `json:"duration"` // seconds
	Artist   struct {
		Name string `json:"name"`
	} `json:"artist"`
	Album struct {
		Title string `json:"title"`
	} `json:"album"`
}

type page struct {
	Data  []result `json:"data"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// Lookup finds the one recording of t in the catalogue and returns t with
// Deezer's title, artist, album, length and ISRC. ok is false when there is
// no such recording or several fit equally well.
func (c *Client) Lookup(ctx context.Context, t track.Track) (track.Track, bool, error) {
	if len(t.Artists) == 0 {
		return t, false, nil
	}
	rs, err := c.search(ctx, t.Artists[0]+" "+track.NormTitle(t.Title))
	if err != nil {
		return t, false, err
	}
	cands := make([]match.Candidate, 0, len(rs))
	albums := map[string]string{}
	for _, r := range rs {
		isrc := strings.ToUpper(strings.TrimSpace(r.ISRC))
		if isrc == "" {
			continue
		}
		cands = append(cands, match.Candidate{ISRC: isrc, Title: r.Title, Artists: []string{r.Artist.Name}, DurationMS: r.Duration * 1000})
		albums[isrc] = r.Album.Title
	}
	m := match.Pick(t, cands)
	if m.Outcome != match.Matched {
		return t, false, nil
	}
	b := m.Best
	t.Title, t.Artists, t.Album, t.DurationMS, t.ISRC = b.Title, b.Artists, albums[b.ISRC], b.DurationMS, b.ISRC
	return t, true, nil
}

func (c *Client) search(ctx context.Context, q string) ([]result, error) {
	u := fmt.Sprintf("%s/search?q=%s&limit=%d", c.api, url.QueryEscape(q), searchLimit)
	var lastErr error
	var wait time.Duration
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
		}
		wait = time.Duration(attempt) * 200 * time.Millisecond
		p, status, resp, err := c.get(ctx, u)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, err
		case err != nil:
			lastErr = err
		case status == http.StatusOK && p.Error == nil:
			return p.Data, nil
		case status == http.StatusOK && p.Error.Code == 4: // quota exceeded: Deezer allows 50 requests per 5s
			lastErr = fmt.Errorf("deezer: %s", p.Error.Message)
		case status == http.StatusOK:
			return nil, fmt.Errorf("deezer: %s", p.Error.Message)
		case httpretry.Transient(status):
			wait = httpretry.Wait(resp, wait)
			lastErr = fmt.Errorf("deezer: HTTP %d", status)
		default:
			return nil, fmt.Errorf("deezer: HTTP %d", status)
		}
	}
	return nil, lastErr
}

func (c *Client) get(ctx context.Context, u string) (page, int, *http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return page{}, 0, nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return page{}, 0, nil, fmt.Errorf("deezer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var p page
	if resp.StatusCode == http.StatusOK {
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxPage))
		if err != nil {
			return page{}, 0, nil, fmt.Errorf("deezer: %w", err)
		}
		if err := json.Unmarshal(b, &p); err != nil {
			return page{}, 0, nil, fmt.Errorf("deezer: reading search results: %w", err)
		}
	}
	return p, resp.StatusCode, resp, nil
}
