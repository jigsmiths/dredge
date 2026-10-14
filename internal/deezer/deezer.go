// Package deezer searches Deezer for recordings and their ISRCs. Spotify does
// not expose ISRCs; Deezer's search returns them with every result.
package deezer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/jigsmiths/dredge/internal/match"
)

const (
	defaultBase = "https://api.deezer.com"
	// Deezer allows 50 requests per 5 seconds per IP.
	defaultInterval = 100 * time.Millisecond
	maxAttempts     = 3
	quotaCode       = 4
	maxBody         = 4 << 20
)

type Client struct {
	base string
	http *http.Client

	interval time.Duration
	sleep    func(context.Context, time.Duration) error

	mu   sync.Mutex
	next time.Time
}

// New takes the API base URL and HTTP client; empty or nil values use the real ones.
func New(base string, hc *http.Client) *Client {
	if base == "" {
		base = defaultBase
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{base: base, http: hc, interval: defaultInterval, sleep: sleep}
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

type response struct {
	Data []struct {
		Title    string `json:"title"`
		Duration int    `json:"duration"` // seconds
		ISRC     string `json:"isrc"`
		Artist   struct {
			Name string `json:"name"`
		} `json:"artist"`
		Album struct {
			Title string `json:"title"`
		} `json:"album"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// Search returns up to limit recordings for a free-text query. Deezer's
// field syntax (artist:"x") returns nothing for plain titles, so it is not used.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]match.Candidate, error) {
	u := fmt.Sprintf("%s/search/track?%s", c.base, url.Values{"q": {query}, "limit": {strconv.Itoa(limit)}}.Encode())

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := c.sleep(ctx, time.Duration(attempt-1)*500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		if err := c.pace(ctx); err != nil {
			return nil, err
		}
		resp, retry, err := c.get(ctx, u)
		if err == nil {
			return candidates(resp), nil
		}
		if !retry {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// pace spaces requests out so the quota is not hit in the first place.
func (c *Client) pace(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	wait := c.next.Sub(now)
	if wait < 0 {
		wait = 0
	}
	c.next = now.Add(wait + c.interval)
	c.mu.Unlock()
	if wait == 0 {
		return nil
	}
	return c.sleep(ctx, wait)
}

// get reports whether a failure is worth retrying.
func (c *Client) get(ctx context.Context, u string) (*response, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, false, err
	}
	hr, err := c.http.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = hr.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(hr.Body, maxBody))
	if err != nil {
		return nil, true, err
	}
	if hr.StatusCode >= 500 {
		return nil, true, fmt.Errorf("deezer: HTTP %d", hr.StatusCode)
	}
	var resp response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, false, fmt.Errorf("deezer: invalid response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error.Code == quotaCode, fmt.Errorf("deezer: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}
	return &resp, false, nil
}

func candidates(r *response) []match.Candidate {
	out := make([]match.Candidate, 0, len(r.Data))
	for _, d := range r.Data {
		out = append(out, match.Candidate{
			ISRC:       d.ISRC,
			Title:      d.Title,
			Artists:    []string{d.Artist.Name},
			Album:      d.Album.Title,
			DurationMS: d.Duration * 1000,
		})
	}
	return out
}
