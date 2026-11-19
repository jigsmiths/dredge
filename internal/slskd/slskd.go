// Package slskd fetches a song from Soulseek through an slskd server: it
// searches, picks a FLAC that fits, downloads it and says where it landed.
package slskd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jigsmiths/dredge/internal/track"
)

// ErrNoFile means nobody on Soulseek shares a FLAC that fits the track.
var ErrNoFile = errors.New("slskd: no FLAC on Soulseek fits")

const (
	searchTimeout = 15 * time.Second
	startTimeout  = 10 * time.Minute // a peer's queue can be long; give up and try the next
	stallTimeout  = 2 * time.Minute  // no bytes for this long while downloading
	pollEvery     = 2 * time.Second
	tries         = 3 // files to try before giving up
	maxBody       = 32 << 20
	subdir        = "dredge" // under slskd's downloads folder, apart from other clients
)

type Client struct {
	base, key string
	dir       string // slskd's downloads folder, as this process sees it
	http      *http.Client

	sleep                     func(context.Context, time.Duration) error
	now                       func() time.Time
	poll, start, stall, query time.Duration
}

// New takes slskd's address, an API key and its downloads folder as seen from here.
func New(base, key, dir string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		base: strings.TrimRight(base, "/"), key: key, dir: dir, http: hc,
		sleep: sleep, now: time.Now, poll: pollEvery, start: startTimeout, stall: stallTimeout, query: searchTimeout,
	}
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

// Fetch downloads the best FLAC of t that Soulseek has and returns its path.
// The file sits alone in its own folder, which the caller removes when done.
// progress, if set, sees the bytes as they arrive.
func (c *Client) Fetch(ctx context.Context, t track.Track, progress func(done, total int64)) (string, error) {
	if len(t.Artists) == 0 || t.DurationMS <= 0 {
		return "", ErrNoFile
	}
	resps, err := c.search(ctx, queryFor(t))
	if err != nil {
		return "", err
	}
	choices := Pick(t, resps)
	if len(choices) == 0 {
		return "", ErrNoFile
	}
	var last error
	for _, ch := range choices[:min(len(choices), tries)] {
		path, err := c.download(ctx, ch, progress)
		if err == nil {
			return path, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		slog.Warn("soulseek download failed", "user", ch.Username, "file", ch.File.Filename, "err", err)
		last = err
	}
	return "", last
}

var wordsRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// queryFor is the artist and title as plain words: Soulseek matches words, and
// punctuation only narrows it.
func queryFor(t track.Track) string {
	return strings.TrimSpace(wordsRe.ReplaceAllString(t.Artists[0]+" "+track.NormTitle(t.Title), " "))
}

type File struct {
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	Extension  string `json:"extension"`
	Length     *int   `json:"length"` // seconds
	BitDepth   *int   `json:"bitDepth"`
	SampleRate *int   `json:"sampleRate"`
	IsLocked   bool   `json:"isLocked"`
}

type Response struct {
	Username          string `json:"username"`
	Files             []File `json:"files"`
	HasFreeUploadSlot bool   `json:"hasFreeUploadSlot"`
	QueueLength       int64  `json:"queueLength"`
	UploadSpeed       int64  `json:"uploadSpeed"`
}

type search struct {
	ID         string `json:"id"`
	IsComplete bool   `json:"isComplete"`
}

func (c *Client) search(ctx context.Context, text string) ([]Response, error) {
	var s search
	body := map[string]any{"searchText": text, "searchTimeout": c.query.Milliseconds(), "filterResponses": true}
	if err := c.call(ctx, http.MethodPost, "/api/v0/searches", body, &s); err != nil {
		return nil, fmt.Errorf("slskd: search: %w", err)
	}
	defer func() {
		_ = c.call(context.WithoutCancel(ctx), http.MethodDelete, "/api/v0/searches/"+s.ID, nil, nil)
	}()
	deadline := c.now().Add(c.query + time.Minute)
	for !s.IsComplete {
		if c.now().After(deadline) {
			return nil, errors.New("slskd: search never finished")
		}
		if err := c.sleep(ctx, c.poll); err != nil {
			return nil, err
		}
		if err := c.call(ctx, http.MethodGet, "/api/v0/searches/"+s.ID, nil, &s); err != nil {
			return nil, fmt.Errorf("slskd: search: %w", err)
		}
	}
	var out []Response
	if err := c.call(ctx, http.MethodGet, "/api/v0/searches/"+s.ID+"/responses", nil, &out); err != nil {
		return nil, fmt.Errorf("slskd: search results: %w", err)
	}
	return out, nil
}

type transfer struct {
	ID               string `json:"id"`
	State            string `json:"state"` // flags, such as "Completed, Succeeded"
	BytesTransferred int64  `json:"bytesTransferred"`
	Size             int64  `json:"size"`
	Exception        string `json:"exception"`
}

type enqueued struct {
	Batch struct {
		Transfers []transfer `json:"transfers"`
	} `json:"batch"`
	Failures []struct {
		Message string `json:"message"`
	} `json:"failures"`
}

func (c *Client) download(ctx context.Context, ch Choice, progress func(done, total int64)) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	dest := subdir + "/" + id
	var res enqueued
	err = c.call(ctx, http.MethodPost, "/api/v0/transfers/downloads/batches", map[string]any{
		"id": id, "username": ch.Username,
		"files":   []map[string]any{{"filename": ch.File.Filename, "size": ch.File.Size}},
		"options": map[string]any{"destination": dest},
	}, &res)
	switch {
	case err != nil:
		return "", fmt.Errorf("enqueue: %w", err)
	case len(res.Failures) > 0:
		return "", fmt.Errorf("enqueue: %s", res.Failures[0].Message)
	case len(res.Batch.Transfers) == 0:
		return "", errors.New("enqueue: no transfer was created")
	}
	tr := res.Batch.Transfers[0]
	userPath := "/api/v0/transfers/downloads/" + url.PathEscape(ch.Username) + "/" + tr.ID

	started, lastBytes, lastMove := false, int64(-1), c.now()
	queuedAt := c.now()
	for {
		if strings.Contains(tr.State, "InProgress") || strings.Contains(tr.State, "Succeeded") {
			started = true
		}
		if tr.BytesTransferred != lastBytes {
			lastBytes, lastMove = tr.BytesTransferred, c.now()
			if progress != nil && started {
				progress(tr.BytesTransferred, tr.Size)
			}
		}
		switch {
		case strings.Contains(tr.State, "Succeeded"):
			_ = c.call(context.WithoutCancel(ctx), http.MethodDelete, userPath+"?remove=true", nil, nil)
			return c.landed(dest)
		case strings.Contains(tr.State, "Completed"):
			_ = c.call(context.WithoutCancel(ctx), http.MethodDelete, userPath+"?remove=true", nil, nil)
			return "", fmt.Errorf("transfer ended %s %s", tr.State, tr.Exception)
		}
		var why string
		switch {
		case !started && c.now().Sub(queuedAt) > c.start:
			why = "it never started"
		case started && c.now().Sub(lastMove) > c.stall:
			why = "it stalled"
		}
		if why != "" {
			_ = c.call(context.WithoutCancel(ctx), http.MethodDelete, userPath+"?remove=true", nil, nil)
			return "", errors.New("gave up: " + why)
		}
		if err := c.sleep(ctx, c.poll); err != nil {
			_ = c.call(context.WithoutCancel(ctx), http.MethodDelete, userPath+"?remove=true", nil, nil)
			return "", err
		}
		if err := c.call(ctx, http.MethodGet, userPath, nil, &tr); err != nil {
			return "", fmt.Errorf("transfer: %w", err)
		}
	}
}

// landed is the one file slskd moved into dest. slskd cleans up the name, so
// the folder is read instead of guessing it.
func (c *Client) landed(dest string) (string, error) {
	dir := filepath.Join(c.dir, filepath.FromSlash(dest))
	es, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("finding the download: %w", err)
	}
	for _, e := range es {
		if e.Type().IsRegular() {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("finding the download: nothing in %s", dir)
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.key)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(b))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, msg)
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}
