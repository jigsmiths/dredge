package match

import (
	"testing"

	"github.com/jigsmiths/dredge/internal/track"
)

var sweetLovin = track.Track{
	Title: "Sweet Lovin' (feat. Bryn Christopher)", Artists: []string{"Sigala", "Bryn Christopher"}, DurationMS: 202000,
}

func TestPlainVersionTagsStillMatchWhenTheLengthFits(t *testing.T) {
	for _, title := range []string{
		"Sweet Lovin' - Radio Edit",
		"Sweet Lovin' - Original Mix",
		"Sweet Lovin' (Original Mix)",
		"Sweet Lovin' - Bonus Track",
		"Sweet Lovin' - Album Version",
	} {
		r := Pick(sweetLovin, []Candidate{cand("GBCEN1501213", title, "Sigala", 202500)})
		if r.Outcome != Matched || r.Best.ISRC != "GBCEN1501213" {
			t.Errorf("%q: outcome %v (%s), want matched", title, r.Outcome, r.Reason)
		}
	}
}

func TestOtherVersionsAreStillDifferentRecordings(t *testing.T) {
	for _, c := range []Candidate{
		cand("X1", "Sweet Lovin' - Extended Mix", "Sigala", 297000),
		cand("X2", "Sweet Lovin' - Showtek Remix", "Sigala", 202500),
		cand("X3", "Sweet Lovin' - Live", "Sigala", 202500),
		cand("X4", "Sweet Lovin' - Radio Edit", "Sigala", 260000), // right tag, wrong length
	} {
		if r := Pick(sweetLovin, []Candidate{c}); r.Outcome != NoMatch {
			t.Errorf("%q: outcome %v, want no match", c.Title, r.Outcome)
		}
	}
}

func TestAnExactTitleBeatsATaggedOne(t *testing.T) {
	r := Pick(sweetLovin, []Candidate{
		cand("TAGGED", "Sweet Lovin' - Radio Edit", "Sigala", 202000),
		cand("EXACT", "Sweet Lovin'", "Sigala", 203500),
	})
	if r.Outcome != Matched || r.Best.ISRC != "EXACT" {
		t.Errorf("got %v %q, want the exact title", r.Outcome, r.Best.ISRC)
	}
}
