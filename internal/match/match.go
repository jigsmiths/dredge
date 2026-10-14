// Package match picks the catalogue recording that a wanted track refers to.
package match

import (
	"fmt"
	"slices"

	"github.com/jigsmiths/dredge/internal/track"
)

const (
	// DurationToleranceMS is how far a candidate's length may be from the
	// wanted one. Catalogues round and trim differently: Spotify and Deezer
	// disagree by about 2.5s on the same recording.
	DurationToleranceMS = 3000

	// TieMarginMS is how much closer in length a candidate must be to beat
	// another recording outright.
	TieMarginMS = 1000
)

// Candidate is a recording from a catalogue that carries ISRCs.
type Candidate struct {
	ISRC       string
	Title      string
	Artists    []string
	Album      string
	DurationMS int
}

// Outcome says whether a single recording was found.
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

// Result is the outcome of Pick, with the reason for it.
type Result struct {
	Outcome Outcome
	Best    Candidate // set when Matched
	Reason  string
}

type scored struct {
	c    Candidate
	dist int // distance in length from the wanted track
}

// Pick returns the one recording that is the wanted track, or says why it
// cannot. It never guesses between different recordings: the exact ISRC is
// what the download source is searched by, so a wrong one is a wasted miss at
// best and the wrong song at worst.
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

	// Recordings that are about equally close: the album decides.
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
	return Result{Outcome: Ambiguous, Reason: fmt.Sprintf("%d different recordings fit equally well", len(rivals)+1)}
}

// distinctISRC returns the candidates whose ISRC differs from isrc.
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
