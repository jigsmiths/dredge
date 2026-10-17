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

// Credit, remaster, album-version and soundtrack tags describe the release; live, remix,
// acoustic and radio edit tags change the recording, so they stay.
var (
	creditTag    = regexp.MustCompile(`(?i)[(\[]\s*(?:feat\.?|ft\.?|featuring|with)\s[^)\]]*[)\]]`)
	dashCredit   = regexp.MustCompile(`(?i)\s-\s*(?:feat\.?|ft\.?|featuring|with)\s.*$`)
	dashAlbum    = regexp.MustCompile(`(?i)\s-\s*(?:original\s+)?album\s+version\s*$`)
	fromTag      = regexp.MustCompile(`(?i)[(\[]\s*from\s+["“][^)\]]*[)\]]`)
	dashFrom     = regexp.MustCompile(`(?i)\s-\s*from\s+["“].*$`)
	remasterTag  = regexp.MustCompile(`(?i)[(\[]\s*(?:\d{4}\s+)?(?:[a-z]+\s+){0,2}(?:digital\s+)?remaster(?:ed)?(?:\s+\d{4})?(?:\s+version)?\s*[)\]]`)
	dashRemaster = regexp.MustCompile(`(?i)\s-\s*(?:\d{4}\s*-?\s*)?(?:[a-z]+\s+){0,2}(?:digital\s+)?remaster(?:ed)?(?:\s*-?\s*\d{4})?(?:\s+version)?\s*$`)

	// the remaster patterns allow a few words ("2015 Paisley Park Remaster"),
	// which must not swallow a word that marks a different recording
	differentRecording = regexp.MustCompile(`(?i)\b(?:live|remix|mix|edit|acoustic|demo|instrumental|karaoke|cover)\b`)
)

func stripRelease(re *regexp.Regexp, s string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		if differentRecording.MatchString(m) {
			return m
		}
		return " "
	})
}

func NormTitle(s string) string {
	for _, re := range []*regexp.Regexp{creditTag, dashCredit, dashAlbum, fromTag, dashFrom} {
		s = re.ReplaceAllString(s, " ")
	}
	for _, re := range []*regexp.Regexp{remasterTag, dashRemaster} {
		s = stripRelease(re, s)
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
