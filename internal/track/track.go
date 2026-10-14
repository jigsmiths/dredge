// Package track is the one shape every source and the library agree on, plus
// the normalisation that lets titles and artists from different catalogues
// be compared.
package track

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Track is a recording as a source describes it.
type Track struct {
	Title      string
	Artists    []string
	Album      string
	DurationMS int
	ISRC       string // empty until matched
	SourceID   string // the id in the source it came from, e.g. a Spotify track id
}

// Credit and remaster tags describe the release rather than the recording, so
// they are dropped. Live, remix, acoustic and radio edit tags stay: those are
// different recordings.
var (
	creditTag    = regexp.MustCompile(`(?i)[(\[]\s*(?:feat\.?|ft\.?|featuring|with)\s[^)\]]*[)\]]`)
	remasterTag  = regexp.MustCompile(`(?i)[(\[]\s*(?:\d{4}\s+)?(?:digital\s+)?remaster(?:ed)?(?:\s+\d{4})?(?:\s+version)?\s*[)\]]`)
	dashCredit   = regexp.MustCompile(`(?i)\s-\s*(?:feat\.?|ft\.?|featuring|with)\s.*$`)
	dashRemaster = regexp.MustCompile(`(?i)\s-\s*(?:\d{4}\s+)?(?:digital\s+)?remaster(?:ed)?(?:\s+\d{4})?(?:\s+version)?\s*$`)
)

// NormTitle reduces a title to lower-case words for comparison.
func NormTitle(s string) string {
	for _, re := range []*regexp.Regexp{creditTag, remasterTag, dashCredit, dashRemaster} {
		s = re.ReplaceAllString(s, " ")
	}
	return normalize(s)
}

// NormArtist reduces an artist name to lower-case words for comparison. A
// leading "The" is dropped, since catalogues disagree about it.
func NormArtist(s string) string {
	n := normalize(s)
	return strings.TrimPrefix(n, "the ")
}

// NormArtists normalises each name and drops the blank ones.
func NormArtists(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if v := NormArtist(n); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func normalize(s string) string {
	s = strings.ReplaceAll(s, "&", " and ")
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r): // accents, split off by NFD
		case r == '\'' || r == '’' || r == '‘' || r == '`':
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
