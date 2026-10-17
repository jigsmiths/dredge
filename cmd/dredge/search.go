package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/resolve"
	"github.com/jigsmiths/dredge/internal/spotify"
	"github.com/jigsmiths/dredge/internal/track"
)

const searchLimit = 15

// searchCommand lists the downloadable recordings for a query. With --get N it
// downloads result N, so the choice is always the user's.
func searchCommand(ctx context.Context, e *env, args []string) error {
	o, err := parseFlags("search", e, args)
	if err != nil {
		return err
	}
	q := strings.TrimSpace(strings.Join(o.args, " "))
	if q == "" {
		return usageError{"expected something to search for"}
	}
	d, err := e.open(&o.cfg)
	if err != nil {
		return err
	}
	defer d.close()

	cands, err := d.search.Search(ctx, q, searchLimit)
	if err != nil {
		return err
	}
	hits := match.Distinct(cands)
	if len(hits) == 0 {
		say(e.out, "no downloadable matches\n")
		return nil
	}
	for i, c := range hits {
		say(e.out, "%2d  %s - %s  (%s)  [%s]\n", i+1, strings.Join(c.Artists, ", "), c.Title, clock(c.DurationMS), c.ISRC)
	}
	if o.pick == 0 {
		say(e.out, "download one with the same words and --get N\n")
		return nil
	}
	if o.pick < 1 || o.pick > len(hits) {
		return usageError{fmt.Sprintf("--get %d is not in the list of %d", o.pick, len(hits))}
	}

	if err := requireMusicDir(o.cfg.MusicDir); err != nil {
		return err
	}
	if !o.noScan {
		if err := scanLibrary(ctx, e, d, o); err != nil {
			return err
		}
	}
	c := hits[o.pick-1]
	t := track.Track{Title: c.Title, Artists: c.Artists, ISRC: c.ISRC, DurationMS: c.DurationMS}
	t, err = resolve.Enrich(ctx, t, d.details)
	if err != nil {
		say(e.err, "no album details (%v); tagging from the search result alone\n", err)
	}
	it, err := resolve.Direct(ctx, t, d.index)
	if err != nil {
		return err
	}
	s := &session{
		d: d, o: o, layout: o.layout(), items: []resolve.Item{it},
		col: &spotify.Collection{Kind: spotify.KindTrack, Name: c.Title, Tracks: []track.Track{t}},
	}
	s.list(e)
	return s.get(ctx, e)
}

func clock(ms int) string { return fmt.Sprintf("%d:%02d", ms/60000, ms/1000%60) }
