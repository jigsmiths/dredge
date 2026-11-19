package slskd

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/track"
)

// Choice is a file one user shares, with what decides who to ask first.
type Choice struct {
	Username string
	File     File
	free     bool
	queue    int64
	speed    int64
}

var trackNoRe = regexp.MustCompile(`^\s*(?:\d{1,2}[-.])?\d{1,3}(?:\s*[-.)_]\s*|\s+)`)

// Pick keeps the FLAC files that are t: a length within the match tolerance,
// the title as the file name and an artist somewhere in the path. Files that
// do not report a length are left out, as the length is the one check a
// file name cannot fake. The best placed to send it comes first.
func Pick(t track.Track, resps []Response) []Choice {
	title := track.NormTitle(t.Title)
	artists := track.NormArtists(t.Artists)
	var out []Choice
	for _, r := range resps {
		for _, f := range r.Files {
			if f.IsLocked || !isFLAC(f) || !fits(f, t.DurationMS) {
				continue
			}
			p := strings.ReplaceAll(f.Filename, `\`, "/")
			if !sameTitle(path.Base(p), title) || !hasArtist(p, artists) {
				continue
			}
			out = append(out, Choice{Username: r.Username, File: f, free: r.HasFreeUploadSlot, queue: r.QueueLength, speed: r.UploadSpeed})
		}
	}
	slices.SortStableFunc(out, func(a, b Choice) int {
		switch {
		case a.free != b.free:
			if a.free {
				return -1
			}
			return 1
		case a.queue != b.queue:
			return int(a.queue - b.queue)
		}
		return int(b.speed - a.speed)
	})
	return out
}

func isFLAC(f File) bool {
	return strings.EqualFold(strings.TrimPrefix(f.Extension, "."), "flac") ||
		strings.EqualFold(path.Ext(strings.ReplaceAll(f.Filename, `\`, "/")), ".flac")
}

func fits(f File, wantMS int) bool {
	if f.Length == nil || wantMS <= 0 {
		return false
	}
	if d := *f.Length*1000 - wantMS; d > match.DurationToleranceMS || d < -match.DurationToleranceMS {
		return false
	}
	if f.BitDepth != nil && *f.BitDepth != 16 && *f.BitDepth != 24 {
		return false
	}
	return f.SampleRate == nil || *f.SampleRate >= 44100
}

// sameTitle allows a track number and "Artist - " in front of the title.
func sameTitle(base, title string) bool {
	name := trackNoRe.ReplaceAllString(strings.TrimSuffix(base, path.Ext(base)), "")
	parts := strings.Split(name, " - ")
	for i := range parts {
		if track.NormTitle(strings.Join(parts[i:], " - ")) == title {
			return true
		}
	}
	return false
}

func hasArtist(p string, artists []string) bool {
	words := " " + track.NormArtist(p) + " "
	for _, a := range artists {
		if strings.Contains(words, " "+a+" ") {
			return true
		}
	}
	return false
}
