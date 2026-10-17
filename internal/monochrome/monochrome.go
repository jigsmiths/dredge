// Package monochrome finds tracks in Monochrome's catalogue by ISRC and
// downloads them as FLAC. Its tracks API is unofficial and has moved domains
// before, so the address is read from the web client rather than hard-coded.
package monochrome

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jigsmiths/dredge/internal/match"
)

const (
	defaultWeb   = "https://monochrome.st"
	discoveryTTL = 6 * time.Hour
	maxAttempts  = 3
	maxResumes   = 5
	maxPage      = 16 << 20
	maxArtwork   = 8 << 20
	stallTimeout = time.Minute

	requestTimeout = 30 * time.Second
	userAgent      = "Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0"
)

// ErrNotStocked means the catalogue answered and does not have the recording.
var ErrNotStocked = errors.New("monochrome: not stocked")

// ErrLossyOnly means the catalogue serves the recording as AAC, not FLAC.
var ErrLossyOnly = errors.New("monochrome: only available as lossy AAC")

type Track struct {
	ID         string
	Title      string
	Artists    []string
	ISRC       string
	DurationMS int
	ReleaseID  string
	ArtworkURL string
}

type Client struct {
	web      string
	override string
	http     *http.Client
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
	stall    time.Duration
	timeout  time.Duration // for each request that is not a download

	mu      sync.Mutex
	api     string
	checked time.Time
}

// New takes the web client URL and an optional fixed API address, which skips
// discovery. Empty or nil values use the real ones.
func New(web, apiOverride string, hc *http.Client) *Client {
	if web == "" {
		web = defaultWeb
	}
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{
		web: web, override: strings.TrimRight(apiOverride, "/"), http: hc,
		now: time.Now, sleep: sleep, stall: stallTimeout, timeout: requestTimeout,
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

var (
	metaRefreshRe = regexp.MustCompile(`(?i)<meta[^>]+http-equiv=["']?refresh["']?[^>]+content=["'][^"']*?url=([^"'\s>]+)`)
	bundleRe      = regexp.MustCompile(`<script[^>]+src=["']([^"']*assets/index-[^"']+\.js)`)
	clientDefault = regexp.MustCompile(`constructor\(([\w$]+)=([\w$]+)\)\{this\.baseUrl=`)
)

// apiBase is where the tracks API lives now. If rediscovery fails, the last
// known address is kept: a broken web client is likelier than a move.
func (c *Client) apiBase(ctx context.Context) (string, error) {
	if c.override != "" {
		return c.override, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api != "" && c.now().Sub(c.checked) < discoveryTTL {
		return c.api, nil
	}
	base, err := c.discover(ctx)
	if err != nil {
		if c.api != "" {
			c.checked = c.now()
			return c.api, nil
		}
		return "", err
	}
	c.api, c.checked = base, c.now()
	return base, nil
}

func (c *Client) discover(ctx context.Context) (string, error) {
	page, pageURL := "", c.web
	for range 3 {
		body, err := c.fetchText(ctx, pageURL)
		if err != nil {
			return "", fmt.Errorf("monochrome: web client: %w", err)
		}
		m := metaRefreshRe.FindStringSubmatch(body[:min(len(body), 4096)])
		if m == nil {
			page = body
			break
		}
		next, err := resolve(pageURL, m[1])
		if err != nil {
			return "", err
		}
		pageURL = next
	}
	if page == "" {
		return "", errors.New("monochrome: web client redirected too many times")
	}
	m := bundleRe.FindStringSubmatch(page)
	if m == nil {
		return "", errors.New("monochrome: client bundle not found on the web page")
	}
	bundleURL, err := resolve(pageURL, m[1])
	if err != nil {
		return "", err
	}
	js, err := c.fetchText(ctx, bundleURL)
	if err != nil {
		return "", fmt.Errorf("monochrome: client bundle: %w", err)
	}
	// minified names change every build: find the variable, then its value
	d := clientDefault.FindStringSubmatch(js)
	if d == nil {
		return "", errors.New("monochrome: tracks API address not found in the client bundle")
	}
	v := regexp.MustCompile(`(?:^|[^\w$])` + regexp.QuoteMeta(d[2]) + `="(https?://[^"\\]+)"`).FindStringSubmatch(js)
	if v == nil {
		return "", errors.New("monochrome: tracks API address not found in the client bundle")
	}
	return strings.TrimRight(v[1], "/"), nil
}

func resolve(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}

func (c *Client) fetchText(ctx context.Context, u string) (string, error) {
	resp, err := c.getRetry(ctx, u)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPage))
	return string(b), err
}

// getRetry retries the gateway errors the API throws between good answers.
func (c *Client) getRetry(ctx context.Context, u string) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := c.sleep(ctx, time.Duration(attempt-1)*200*time.Millisecond); err != nil {
				return nil, err
			}
		}
		rctx, cancel := context.WithTimeout(ctx, c.timeout)
		req, err := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json, text/html, */*")
		resp, err := c.http.Do(req)
		if err != nil {
			cancel()
			return nil, err
		}
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
		switch resp.StatusCode {
		case http.StatusOK:
			return resp, nil
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		default:
			_ = resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	}
	return nil, lastErr
}

// cancelOnClose keeps a request's deadline running until its body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnClose) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

// flexString reads an id that may arrive as a string or a number.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	*f = flexString(strings.Trim(string(b), `"`))
	return nil
}

type rawTrack struct {
	ID          flexString `json:"id"`
	TrackID     flexString `json:"trackId"`
	Title       string     `json:"title"`
	ArtistNames []string   `json:"artistNames"`
	ISRC        string     `json:"isrc"`
	Playable    *bool      `json:"playable"`
	Duration    int        `json:"duration"`
	ReleaseID   flexString `json:"releaseId"`
	Artwork     string     `json:"artwork"`
}

// id is what /track/{id} wants: trackId in listings, id in searches. Empty
// when the entry cannot be downloaded.
func (r rawTrack) id() string {
	id := string(r.TrackID)
	if id == "" {
		id = string(r.ID)
	}
	if !digitsRe.MatchString(id) || (r.Playable != nil && !*r.Playable) {
		return ""
	}
	return id
}

func (c *Client) search(ctx context.Context, base, q string, limit int) ([]rawTrack, error) {
	resp, err := c.getRetry(ctx, base+"/search/tracks?"+url.Values{"q": {q}, "limit": {strconv.Itoa(limit)}}.Encode())
	if err != nil {
		return nil, fmt.Errorf("monochrome: search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var sr struct {
		Tracks []rawTrack `json:"tracks"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPage)).Decode(&sr); err != nil {
		return nil, fmt.Errorf("monochrome: search: invalid response: %w", err)
	}
	return sr.Tracks, nil
}

// Search returns what the catalogue lists for a free-text query as match
// candidates. Only downloadable entries with an ISRC are included, so a match
// is also a promise that the track can be fetched.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]match.Candidate, error) {
	base, err := c.apiBase(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := c.search(ctx, base, query, limit)
	if err != nil {
		return nil, err
	}
	var out []match.Candidate
	for _, t := range raw {
		if t.id() == "" || strings.TrimSpace(t.ISRC) == "" {
			continue
		}
		out = append(out, match.Candidate{
			ISRC: strings.ToUpper(strings.TrimSpace(t.ISRC)), Title: t.Title, Artists: t.ArtistNames, DurationMS: t.Duration,
			ID: t.id(), ReleaseID: string(t.ReleaseID), ArtworkURL: t.Artwork,
		})
	}
	return out, nil
}

// TrackFrom is the downloadable track a search candidate stands for.
func TrackFrom(c match.Candidate) Track {
	return Track{
		ID: c.ID, Title: c.Title, Artists: c.Artists, ISRC: c.ISRC, DurationMS: c.DurationMS,
		ReleaseID: c.ReleaseID, ArtworkURL: c.ArtworkURL,
	}
}

var (
	digitsRe = regexp.MustCompile(`^\d+$`)
	tidyRe   = regexp.MustCompile(`[\W_]+`)
)

func queries(artist, title string) []string {
	tidy := func(s string) string { return strings.TrimSpace(tidyRe.ReplaceAllString(s, " ")) }
	var out []string
	for _, q := range []string{tidy(artist + " " + title), tidy(title)} {
		if q == "" {
			continue
		}
		if !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, q) }) {
			out = append(out, q)
		}
	}
	return out
}

// Find returns the catalogue entry with exactly this ISRC. The API has no ISRC
// lookup, so it searches by artist and title and keeps only the exact match: a
// close title from another pressing is how the live version gets downloaded.
// Transport trouble is an error, not ErrNotStocked.
func (c *Client) Find(ctx context.Context, isrc, artist, title string) (Track, error) {
	isrc = strings.ToUpper(strings.TrimSpace(isrc))
	if isrc == "" {
		return Track{}, ErrNotStocked
	}
	base, err := c.apiBase(ctx)
	if err != nil {
		return Track{}, err
	}
	for _, q := range queries(artist, title) {
		raw, err := c.search(ctx, base, q, 25)
		if err != nil {
			return Track{}, err
		}
		for _, t := range raw {
			if id := t.id(); id != "" && strings.ToUpper(strings.TrimSpace(t.ISRC)) == isrc {
				return Track{
					ID: id, Title: t.Title, Artists: t.ArtistNames, ISRC: isrc, DurationMS: t.Duration,
					ReleaseID: string(t.ReleaseID), ArtworkURL: t.Artwork,
				}, nil
			}
		}
	}
	return Track{}, ErrNotStocked
}

type progressKey struct{}

// WithProgress makes Download on ctx report bytes received so far and the
// total size, which is -1 when the server does not say.
func WithProgress(ctx context.Context, f func(done, total int64)) context.Context {
	return context.WithValue(ctx, progressKey{}, f)
}

// ReportProgress calls the function set by WithProgress, if any.
func ReportProgress(ctx context.Context, done, total int64) {
	if f, ok := ctx.Value(progressKey{}).(func(done, total int64)); ok {
		f(done, total)
	}
}

type countReader struct {
	r           io.Reader
	done, total int64
	f           func(done, total int64)
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	c.f(c.done, c.total)
	return n, err
}

// Download streams the FLAC to w and returns the bytes written. Nothing is
// written unless the body starts like a FLAC file.
func (c *Client) Download(ctx context.Context, t Track, w io.Writer) (int64, error) {
	base, err := c.apiBase(ctx)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	u := base + "/track/" + url.PathEscape(t.ID)
	resp, err := c.open(ctx, u, 0)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("monochrome: download: HTTP %d", resp.StatusCode)
	}

	// a server that dribbles bytes never trips a per-read timeout
	timer := time.AfterFunc(c.stall, func() { cancel(errors.New("monochrome: download stalled")) })
	defer timer.Stop()
	body := &stallReader{r: resp.Body, timer: timer, d: c.stall}

	br := bufio.NewReader(body)
	if magic, err := br.Peek(4); err != nil || !bytes.Equal(magic, []byte("fLaC")) {
		if head, _ := br.Peek(8); len(head) == 8 && bytes.Equal(head[4:], []byte("ftyp")) {
			return 0, ErrLossyOnly
		}
		return 0, causeOr(ctx, errors.New("monochrome: download is not a FLAC file"))
	}
	total := resp.ContentLength
	progress, _ := ctx.Value(progressKey{}).(func(done, total int64))
	var n int64
	for resumes := 0; ; resumes++ {
		rd := &errReader{r: br}
		var src io.Reader = rd
		if progress != nil {
			src = &countReader{r: rd, done: n, total: total, f: progress}
		}
		m, err := io.Copy(w, src)
		n += m
		if err == nil {
			break
		}
		// only a failed read is worth resuming, not a write the tagger refused
		if rd.err == nil || ctx.Err() != nil || total < 0 || resumes >= maxResumes {
			return n, causeOr(ctx, err)
		}
		slog.Warn("download interrupted, resuming", "track", t.ID, "at", n, "of", total, "err", err)
		next, rerr := c.open(ctx, u, n)
		if rerr == nil && !resumedAt(next, n) {
			_ = next.Body.Close()
			rerr = fmt.Errorf("monochrome: download: server did not resume at byte %d (HTTP %d)", n, next.StatusCode)
		}
		if rerr != nil {
			return n, fmt.Errorf("%w (resume failed: %w)", err, rerr)
		}
		_ = resp.Body.Close()
		resp = next
		body.r = resp.Body
		br = bufio.NewReader(body)
	}
	if total >= 0 && n != total {
		return n, fmt.Errorf("monochrome: download ended after %d of %d bytes", n, total)
	}
	return n, nil
}

// open starts a download at byte from, or the beginning if it is 0.
func (c *Client) open(ctx context.Context, u string, from int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if from > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", from))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if from == 0 && resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("monochrome: download: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// resumedAt reports whether resp is a partial response that starts at byte from.
func resumedAt(resp *http.Response, from int64) bool {
	return resp.StatusCode == http.StatusPartialContent &&
		strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", from))
}

// errReader remembers whether a failure came from reading.
type errReader struct {
	r   io.Reader
	err error
}

func (e *errReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err != nil && err != io.EOF {
		e.err = err
	}
	return n, err
}

func causeOr(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

type stallReader struct {
	r     io.Reader
	timer *time.Timer
	d     time.Duration
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(s.d)
	}
	return n, err
}

// Artwork returns the cover image, or nil if the track has none.
func (c *Client) Artwork(ctx context.Context, t Track) ([]byte, error) {
	if t.ArtworkURL == "" {
		return nil, nil
	}
	resp, err := c.getRetry(ctx, t.ArtworkURL)
	if err != nil {
		return nil, fmt.Errorf("monochrome: artwork: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(io.LimitReader(resp.Body, maxArtwork))
}
