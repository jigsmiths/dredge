package monochrome

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Details is what the catalogue knows about the release a recording is on.
type Details struct {
	Album       string
	AlbumArtist string
	Date        string // as precise as the catalogue has it, usually YYYY-MM-DD
	TrackNo     int    // zero when the release does not list the track
	DiscNo      int
}

// Details finds the recording by ISRC, as Find does, and reads its release.
// A search result carries no album or track number, so a track picked from
// search results gets them here. ErrNotStocked if the ISRC is not listed.
func (c *Client) Details(ctx context.Context, isrc, artist, title string) (Details, error) {
	t, err := c.Find(ctx, isrc, artist, title)
	if err != nil {
		return Details{}, err
	}
	if t.ReleaseID == "" {
		return Details{}, fmt.Errorf("monochrome: no release listed for %s", t.ISRC)
	}
	base, err := c.apiBase(ctx)
	if err != nil {
		return Details{}, err
	}
	resp, err := c.getRetry(ctx, base+"/releases/"+url.PathEscape(t.ReleaseID))
	if err != nil {
		return Details{}, fmt.Errorf("monochrome: release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var rel struct {
		Title       string `json:"title"`
		ReleaseDate string `json:"releaseDate"`
		Artists     []struct {
			Name string `json:"name"`
		} `json:"artists"`
		Tracks []struct {
			ISRC        string `json:"isrc"`
			DiscNumber  int    `json:"discNumber"`
			TrackNumber int    `json:"trackNumber"`
		} `json:"tracks"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPage)).Decode(&rel); err != nil {
		return Details{}, fmt.Errorf("monochrome: release: invalid response: %w", err)
	}

	d := Details{Album: rel.Title, Date: dateOnly(rel.ReleaseDate)}
	var names []string
	for _, a := range rel.Artists {
		if n := strings.TrimSpace(a.Name); n != "" {
			names = append(names, n)
		}
	}
	d.AlbumArtist = strings.Join(names, ", ")
	for _, rt := range rel.Tracks {
		if strings.EqualFold(strings.TrimSpace(rt.ISRC), t.ISRC) {
			d.TrackNo, d.DiscNo = rt.TrackNumber, rt.DiscNumber
			break
		}
	}
	return d, nil
}

func dateOnly(s string) string {
	if before, _, ok := strings.Cut(s, "T"); ok {
		return before
	}
	return s
}
