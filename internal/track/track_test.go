package track

import "testing"

func TestNormTitle(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Barracuda", "barracuda"},
		{"  Spaced   Out ", "spaced out"},
		{"Don't Stop Believin'", "dont stop believin"},
		{"Don’t Stop Believin’", "dont stop believin"},
		{"Mr. Brightside", "mr brightside"},
		{"Rock & Roll", "rock and roll"},
		{"Beyoncé", "beyonce"},
		{"Jamaican (Bam Bam)", "jamaican bam bam"},

		{"Crazy On You - 2004 Remaster", "crazy on you"},
		{"Crazy On You - Remastered 2004", "crazy on you"},
		{"Crazy On You (Remastered 2004)", "crazy on you"},
		{"Crazy On You [Remastered]", "crazy on you"},
		{"Crazy On You (2011 Remaster)", "crazy on you"},
		{"Song (feat. Someone)", "song"},
		{"Song (ft. Someone)", "song"},
		{"Song [feat. Someone]", "song"},
		{"Song (with Someone)", "song"},
		{"Song - feat. Someone", "song"},

		{"Barracuda (Live)", "barracuda live"},
		{"Barracuda - Radio Edit", "barracuda radio edit"},
		{"Barracuda (Acoustic)", "barracuda acoustic"},
		{"Barracuda - Remix", "barracuda remix"},
		{"Barracuda (Live) [Remastered]", "barracuda live"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NormTitle(tt.in); got != tt.want {
				t.Errorf("NormTitle(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormArtist(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Heart", "heart"},
		{"The Beatles", "beatles"},
		{"Tyler, The Creator", "tyler the creator"},
		{"AC/DC", "ac dc"},
		{"Beyoncé", "beyonce"},
		{"Florence + The Machine", "florence the machine"},
		{"  Simon &  Garfunkel ", "simon and garfunkel"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NormArtist(tt.in); got != tt.want {
				t.Errorf("NormArtist(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormArtistsSkipsBlanks(t *testing.T) {
	got := NormArtists([]string{"Heart", " ", "The Who"})
	want := []string{"heart", "who"}
	if len(got) != len(want) {
		t.Fatalf("NormArtists = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NormArtists = %v, want %v", got, want)
		}
	}
}
