// Package track is the shared track shape and the normalisation used to
// compare titles and artists across catalogues.
package track

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Track struct {
	Title       string
	Artists     []string
	Album       string
	AlbumArtist string
	Date        string // YYYY-MM-DD, or less
	DurationMS  int
	DiscNo      int
	TrackNo     int
	ISRC        string // empty until matched
	SourceID    string // e.g. a Spotify track id
}

// Credit and remaster tags describe the release; live, remix, acoustic and
// radio edit tags change the recording, so they stay.
var (
	creditTag    = regexp.MustCompile(`(?i)[(\[]\s*(?:feat\.?|ft\.?|featuring|with)\s[^)\]]*[)\]]`)
	remasterTag  = regexp.MustCompile(`(?i)[(\[]\s*(?:\d{4}\s+)?(?:digital\s+)?remaster(?:ed)?(?:\s+\d{4})?(?:\s+version)?\s*[)\]]`)
	dashCredit   = regexp.MustCompile(`(?i)\s-\s*(?:feat\.?|ft\.?|featuring|with)\s.*$`)
	dashRemaster = regexp.MustCompile(`(?i)\s-\s*(?:\d{4}\s+)?(?:digital\s+)?remaster(?:ed)?(?:\s+\d{4})?(?:\s+version)?\s*$`)
)

func NormTitle(s string) string {
	for _, re := range []*regexp.Regexp{creditTag, remasterTag, dashCredit, dashRemaster} {
		s = re.ReplaceAllString(s, " ")
	}
	return normalize(s)
}

// NormArtist drops a leading "The": catalogues disagree on it.
func NormArtist(s string) string {
	n := normalize(s)
	return strings.TrimPrefix(n, "the ")
}

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
