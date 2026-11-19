package slskd

import (
	"testing"

	"github.com/jigsmiths/dredge/internal/track"
)

var wedding = track.Track{Title: "Stop The Wedding!", Artists: []string{"Ashe"}, DurationMS: 198000}

func n(v int) *int { return &v }

func flac(name string, secs int) File {
	return File{Filename: name, Size: 30 << 20, Extension: "flac", Length: n(secs), BitDepth: n(16), SampleRate: n(44100)}
}

func TestPickKeepsOnlyFilesThatAreTheTrack(t *testing.T) {
	cases := []struct {
		name string
		file File
		keep bool
	}{
		{"artist folder and plain title", flac(`Music\Ashe\Stop The Wedding!\01 Stop The Wedding!.flac`, 198), true},
		{"artist in the file name", flac(`Downloads\new\Ashe - Stop The Wedding!.flac`, 198), true},
		{"number, artist and title", flac(`Music\Singles\01 - Ashe - Stop the Wedding.flac`, 197), true},
		{"24-bit", func() File {
			f := flac(`Ashe\Stop The Wedding!.flac`, 198)
			f.BitDepth, f.SampleRate = n(24), n(96000)
			return f
		}(), true},
		{"no bit depth reported", func() File {
			f := flac(`Ashe\Stop The Wedding!.flac`, 198)
			f.BitDepth, f.SampleRate = nil, nil
			return f
		}(), true},
		{"length off by more than 5s", flac(`Ashe\Stop The Wedding!.flac`, 210), false},
		{"no length reported", func() File { f := flac(`Ashe\Stop The Wedding!.flac`, 198); f.Length = nil; return f }(), false},
		{"MP3", File{Filename: `Ashe\Stop The Wedding!.mp3`, Extension: "mp3", Length: n(198)}, false},
		{"locked", func() File { f := flac(`Ashe\Stop The Wedding!.flac`, 198); f.IsLocked = true; return f }(), false},
		{"8-bit", func() File { f := flac(`Ashe\Stop The Wedding!.flac`, 198); f.BitDepth = n(8); return f }(), false},
		{"low sample rate", func() File { f := flac(`Ashe\Stop The Wedding!.flac`, 198); f.SampleRate = n(22050); return f }(), false},
		{"another artist", flac(`Karaoke\Sing2Piano\Stop The Wedding!.flac`, 198), false},
		{"a karaoke version", flac(`Ashe\Stop The Wedding! (Karaoke Version).flac`, 198), false},
		{"a live version", flac(`Ashe\Stop The Wedding! - Live.flac`, 198), false},
		{"a demo", flac(`Ashe\Stop The Wedding! (Original Piano Demo).flac`, 198), false},
		{"artist only as part of a word", flac(`Ashes\Stop The Wedding!.flac`, 198), false},
	}
	for _, c := range cases {
		got := Pick(wedding, []Response{{Username: "u", Files: []File{c.file}}})
		if (len(got) == 1) != c.keep {
			t.Errorf("%s: kept = %v, want %v", c.name, len(got) == 1, c.keep)
		}
	}
}

func TestPickAsksTheReadiestUserFirst(t *testing.T) {
	f := flac(`Ashe\Stop The Wedding!.flac`, 198)
	got := Pick(wedding, []Response{
		{Username: "queued", Files: []File{f}, QueueLength: 5, UploadSpeed: 9_000_000},
		{Username: "slow", Files: []File{f}, HasFreeUploadSlot: true, UploadSpeed: 100_000},
		{Username: "fast", Files: []File{f}, HasFreeUploadSlot: true, UploadSpeed: 5_000_000},
		{Username: "short queue", Files: []File{f}, QueueLength: 1},
	})
	var order []string
	for _, c := range got {
		order = append(order, c.Username)
	}
	want := []string{"fast", "slow", "short queue", "queued"}
	if len(order) != len(want) {
		t.Fatalf("order = %q, want %q", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %q, want %q", order, want)
		}
	}
}
