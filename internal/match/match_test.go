package match

import (
	"testing"

	"github.com/jigsmiths/dredge/internal/track"
)

var barracuda = track.Track{
	Title:      "Barracuda",
	Artists:    []string{"Heart"},
	Album:      "Little Queen",
	DurationMS: 261495,
}

func cand(isrc, title, artist, album string, ms int) Candidate {
	return Candidate{ISRC: isrc, Title: title, Artists: []string{artist}, Album: album, DurationMS: ms}
}

func TestPick(t *testing.T) {
	tests := []struct {
		name     string
		want     track.Track
		cands    []Candidate
		outcome  Outcome
		wantISRC string
	}{
		{
			name: "two recordings, duration picks the album version",
			want: barracuda,
			cands: []Candidate{
				cand("USSM17700674", "Barracuda", "Heart", "Greatest Hits", 208000),
				cand("USSM10009075", "Barracuda", "Heart", "Little Queen", 264000),
			},
			outcome:  Matched,
			wantISRC: "USSM10009075",
		},
		{
			name:    "no candidates",
			want:    barracuda,
			outcome: NoMatch,
		},
		{
			name:    "cover by another artist",
			want:    barracuda,
			cands:   []Candidate{cand("TCAEL1944947", "Barracuda", "Seven Kingdoms", "Covers", 262000)},
			outcome: NoMatch,
		},
		{
			name:    "live version is a different recording",
			want:    barracuda,
			cands:   []Candidate{cand("USXX00000001", "Barracuda (Live)", "Heart", "Live", 262000)},
			outcome: NoMatch,
		},
		{
			name:     "remaster tag is ignored",
			want:     barracuda,
			cands:    []Candidate{cand("USSM10009075", "Barracuda - 2004 Remaster", "Heart", "Little Queen", 262000)},
			outcome:  Matched,
			wantISRC: "USSM10009075",
		},
		{
			name:     "duration exactly on the tolerance edge still matches",
			want:     barracuda,
			cands:    []Candidate{cand("USSM10009075", "Barracuda", "Heart", "", 261495+DurationToleranceMS)},
			outcome:  Matched,
			wantISRC: "USSM10009075",
		},
		{
			name:    "duration beyond tolerance",
			want:    barracuda,
			cands:   []Candidate{cand("USSM10009075", "Barracuda", "Heart", "", 261495+DurationToleranceMS+1)},
			outcome: NoMatch,
		},
		{
			name: "closer duration wins by a clear margin",
			want: barracuda,
			cands: []Candidate{
				cand("AAAA00000001", "Barracuda", "Heart", "", 261800),
				cand("BBBB00000002", "Barracuda", "Heart", "", 264400),
			},
			outcome:  Matched,
			wantISRC: "AAAA00000001",
		},
		{
			name: "album breaks a duration tie",
			want: barracuda,
			cands: []Candidate{
				cand("AAAA00000001", "Barracuda", "Heart", "Greatest Hits", 262000),
				cand("BBBB00000002", "Barracuda", "Heart", "Little Queen", 262000),
			},
			outcome:  Matched,
			wantISRC: "BBBB00000002",
		},
		{
			name: "releases of the same recording: the first of equals is taken",
			want: barracuda,
			cands: []Candidate{
				cand("AAAA00000001", "Barracuda", "Heart", "Greatest Hits", 262000),
				cand("BBBB00000002", "Barracuda", "Heart", "Classics", 262000),
			},
			outcome:  Matched,
			wantISRC: "AAAA00000001",
		},
		{
			name: "the closer of two near-equal releases wins even inside the margin",
			want: barracuda,
			cands: []Candidate{
				cand("AAAA00000001", "Barracuda", "Heart", "Greatest Hits", 262400),
				cand("BBBB00000002", "Barracuda", "Heart", "Classics", 261900),
			},
			outcome:  Matched,
			wantISRC: "BBBB00000002",
		},
		{
			name: "a remastered wanted track prefers the remastered release",
			want: track.Track{Title: "One Way Or Another - Remastered 2001", Artists: []string{"Blondie"}, DurationMS: 212000},
			cands: []Candidate{
				cand("AAAA00000001", "One Way Or Another", "Blondie", "", 211000),
				cand("BBBB00000002", "One Way Or Another (Remastered 2001)", "Blondie", "", 214000),
			},
			outcome:  Matched,
			wantISRC: "BBBB00000002",
		},
		{
			name: "a plain wanted track prefers the plain release",
			want: track.Track{Title: "One Way Or Another", Artists: []string{"Blondie"}, DurationMS: 212000},
			cands: []Candidate{
				cand("AAAA00000001", "One Way Or Another", "Blondie", "", 211000),
				cand("BBBB00000002", "One Way Or Another (Remastered 2001)", "Blondie", "", 214000),
			},
			outcome:  Matched,
			wantISRC: "AAAA00000001",
		},
		{
			name: "two equally good remasters: the first is taken",
			want: track.Track{Title: "Song - Remastered", Artists: []string{"A"}, DurationMS: 200000},
			cands: []Candidate{
				cand("AAAA00000001", "Song (Remastered 2001)", "A", "", 200500),
				cand("BBBB00000002", "Song (Remastered 2011)", "A", "", 200500),
			},
			outcome:  Matched,
			wantISRC: "AAAA00000001",
		},
		{
			name: "same ISRC listed twice is one recording",
			want: barracuda,
			cands: []Candidate{
				cand("USSM10009075", "Barracuda", "Heart", "Little Queen", 264000),
				cand("USSM10009075", "Barracuda", "Heart", "Greatest Hits", 264000),
			},
			outcome:  Matched,
			wantISRC: "USSM10009075",
		},
		{
			name:    "candidate without an ISRC is useless",
			want:    barracuda,
			cands:   []Candidate{cand("", "Barracuda", "Heart", "Little Queen", 262000)},
			outcome: NoMatch,
		},
		{
			name: "unknown wanted duration does not filter",
			want: track.Track{Title: "Barracuda", Artists: []string{"Heart"}},
			cands: []Candidate{
				cand("USSM10009075", "Barracuda", "Heart", "", 264000),
			},
			outcome:  Matched,
			wantISRC: "USSM10009075",
		},
		{
			name: "unknown wanted duration with two recordings cannot choose",
			want: track.Track{Title: "Barracuda", Artists: []string{"Heart"}},
			cands: []Candidate{
				cand("AAAA00000001", "Barracuda", "Heart", "", 264000),
				cand("BBBB00000002", "Barracuda", "Heart", "", 208000),
			},
			outcome: Ambiguous,
		},
		{
			name: "any shared artist counts",
			want: track.Track{
				Title: "Jamaican (Bam Bam)", Artists: []string{"HUGEL", "SOLTO (FR)"}, DurationMS: 156393,
			},
			cands:    []Candidate{cand("FR1234567890", "Jamaican (Bam Bam)", "HUGEL", "", 156000)},
			outcome:  Matched,
			wantISRC: "FR1234567890",
		},
		{
			name:     "leading The and case are ignored in artists",
			want:     track.Track{Title: "My Generation", Artists: []string{"The Who"}, DurationMS: 198000},
			cands:    []Candidate{cand("GB1234567890", "My Generation", "WHO", "", 198000)},
			outcome:  Matched,
			wantISRC: "GB1234567890",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Pick(tt.want, tt.cands)
			if got.Outcome != tt.outcome {
				t.Fatalf("outcome = %v (%s), want %v", got.Outcome, got.Reason, tt.outcome)
			}
			if tt.outcome == Matched && got.Best.ISRC != tt.wantISRC {
				t.Errorf("best ISRC = %s, want %s", got.Best.ISRC, tt.wantISRC)
			}
			if got.Reason == "" {
				t.Error("every result should say why")
			}
		})
	}
}

func TestOutcomeString(t *testing.T) {
	for o, want := range map[Outcome]string{Matched: "matched", NoMatch: "no_match", Ambiguous: "ambiguous"} {
		if got := o.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", o, got, want)
		}
	}
}
