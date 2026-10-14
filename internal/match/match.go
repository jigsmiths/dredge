// Package match picks the catalogue recording that a wanted track refers to.
package match

import (
	"fmt"
	"regexp"
	"slices"

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
	Album      string
	DurationMS int
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
func Pick(want track.Track, cands []Candidate) Result {
	title := track.NormTitle(want.Title)
	artists := track.NormArtists(want.Artists)

	var ok []scored
	for _, c := range cands {
		if c.ISRC == "" || track.NormTitle(c.Title) != title || !sharesArtist(artists, c.Artists) {
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
	if len(ok) == 0 {
		return Result{Reason: "no candidate with the same title, artist and length"}
	}
	slices.SortStableFunc(ok, func(a, b scored) int { return a.dist - b.dist })
	best := ok[0]

	rivals := distinctISRC(ok, best.c.ISRC)
	if len(rivals) == 0 {
		return Result{Outcome: Matched, Best: best.c, Reason: "one recording"}
	}

	if want.DurationMS > 0 && rivals[0].dist-best.dist > TieMarginMS {
		return Result{Outcome: Matched, Best: best.c, Reason: fmt.Sprintf("closest length (%dms off)", best.dist)}
	}

	// about equally close in length: the album decides
	album := track.NormTitle(want.Album)
	if album != "" {
		var byAlbum []scored
		for _, s := range ok {
			if s.dist-best.dist <= TieMarginMS && track.NormTitle(s.c.Album) == album {
				byAlbum = append(byAlbum, s)
			}
		}
		if len(byAlbum) > 0 && len(distinctISRC(byAlbum, byAlbum[0].c.ISRC)) == 0 {
			return Result{Outcome: Matched, Best: byAlbum[0].c, Reason: "same album"}
		}
	}
	// still tied: a remastered track wants the remastered release, a plain one the plain
	wantRemaster := remasterRe.MatchString(want.Title)
	var agree []scored
	for _, s := range ok {
		if s.dist-best.dist <= TieMarginMS && remasterRe.MatchString(s.c.Title) == wantRemaster {
			agree = append(agree, s)
		}
	}
	if len(agree) > 0 && len(distinctISRC(agree, agree[0].c.ISRC)) == 0 {
		return Result{Outcome: Matched, Best: agree[0].c, Reason: "same release tag"}
	}
	if want.DurationMS > 0 {
		// same title, artist and length within a second: releases of one recording
		return Result{Outcome: Matched, Best: best.c, Reason: fmt.Sprintf("%d releases fit equally well; took the closest", len(rivals)+1)}
	}
	return Result{Outcome: Ambiguous, Reason: fmt.Sprintf("%d different recordings fit equally well", len(rivals)+1)}
}

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
