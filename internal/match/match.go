// Package match picks the catalogue recording that a wanted track refers to.
package match

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jigsmiths/dredge/internal/track"
)

const (
	// Catalogues disagree by up to about 4s on the same recording.
	DurationToleranceMS = 5000

	// How much closer in length a recording must be to beat another outright.
	TieMarginMS = 1000
)

type Candidate struct {
	ISRC       string
	Title      string
	Artists    []string
	DurationMS int

	// the source's own handle on the recording, so a match can be fetched without searching again
	ID         string
	ReleaseID  string
	ArtworkURL string
}

// Distinct drops repeats of a recording, which a catalogue lists once per
// release, keeping the first of each ISRC.
func Distinct(cands []Candidate) []Candidate {
	seen := map[string]bool{}
	var out []Candidate
	for _, c := range cands {
		if !seen[c.ISRC] {
			seen[c.ISRC] = true
			out = append(out, c)
		}
	}
	return out
}

type Outcome int

const (
	NoMatch Outcome = iota
	Matched
	Ambiguous
)

func (o Outcome) String() string {
	switch o {
	case Matched:
		return "matched"
	case Ambiguous:
		return "ambiguous"
	}
	return "no_match"
}

type Result struct {
	Outcome Outcome
	Best    Candidate // set when Matched
	Reason  string
}

type scored struct {
	c    Candidate
	dist int // distance in length from the wanted track
}

// Pick never guesses between different recordings: the download source is
// searched by exact ISRC, so a wrong one fetches the wrong song or misses.
// Catalogues list one recording under several ISRCs, one per re-release, so
// candidates with the same title and artist and about the same length count
// as one recording; only a track of unknown length can be ambiguous.
func Pick(want track.Track, cands []Candidate) Result {
	title := track.NormTitle(want.Title)
	artists := track.NormArtists(want.Artists)

	collect := func(sameTitle func(string) bool) []scored {
		var ok []scored
		for _, c := range cands {
			if c.ISRC == "" || !sameTitle(c.Title) || !sharesArtist(artists, c.Artists) {
				continue
			}
			dist := 0
			if want.DurationMS > 0 {
				dist = DurationToleranceMS
				if c.DurationMS > 0 {
					dist = abs(c.DurationMS - want.DurationMS)
				}
				if dist > DurationToleranceMS {
					continue
				}
			}
			ok = append(ok, scored{c, dist})
		}
		return ok
	}
	ok := collect(func(s string) bool { return track.NormTitle(s) == title })
	if len(ok) == 0 { // a plain version tag, such as "- Radio Edit", is not a different recording
		ok = collect(func(s string) bool { return track.NormTitle(plainTagRe.ReplaceAllString(s, " ")) == title })
	}
	if len(ok) == 0 {
		return Result{Reason: "no candidate with the same title, artist and length"}
	}
	slices.SortStableFunc(ok, func(a, b scored) int {
		return cmp.Or(a.dist-b.dist, strings.Compare(a.c.ISRC, b.c.ISRC)) // the ISRC keeps a rerun on the same pick
	})
	best := ok[0]

	rivals := distinctISRC(ok, best.c.ISRC)
	if len(rivals) == 0 {
		return Result{Outcome: Matched, Best: best.c, Reason: "one recording"}
	}

	if want.DurationMS > 0 && rivals[0].dist-best.dist > TieMarginMS {
		return Result{Outcome: Matched, Best: best.c, Reason: fmt.Sprintf("closest length (%dms off)", best.dist)}
	}

	// about equally close in length: a remastered track wants the remastered release, a plain one the plain
	wantRemaster := remasterRe.MatchString(want.Title)
	var near, agree []scored
	for _, s := range ok {
		if s.dist-best.dist > TieMarginMS {
			continue
		}
		near = append(near, s)
		if remasterRe.MatchString(s.c.Title) == wantRemaster {
			agree = append(agree, s)
		}
	}
	pool, reason := agree, "same release tag"
	if len(pool) == 0 {
		pool, reason = near, fmt.Sprintf("closest length (%dms off)", best.dist)
	}
	if want.DurationMS == 0 && len(distinctISRC(pool, pool[0].c.ISRC)) > 0 {
		return Result{Outcome: Ambiguous, Reason: fmt.Sprintf("%d different recordings fit equally well", len(rivals)+1)}
	}
	return Result{Outcome: Matched, Best: pool[0].c, Reason: reason}
}

// Only tags that name the same recording; remixes, live and extended mixes are not here.
var plainTagRe = regexp.MustCompile(`(?i)\s*(?:-\s*|\()(?:original mix|radio edit|bonus track)\)?\s*$`)

var remasterRe = regexp.MustCompile(`(?i)\bremaster`)

func distinctISRC(cs []scored, isrc string) []scored {
	var out []scored
	for _, s := range cs {
		if s.c.ISRC != isrc {
			out = append(out, s)
		}
	}
	return out
}

func sharesArtist(want []string, got []string) bool {
	for _, g := range track.NormArtists(got) {
		if slices.Contains(want, g) {
			return true
		}
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
