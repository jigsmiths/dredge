package library

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigsmiths/dredge/internal/db"
	"github.com/jigsmiths/dredge/internal/flacmeta"
	"github.com/jigsmiths/dredge/internal/track"
)

func TestPath(t *testing.T) {
	root := filepath.FromSlash("/music")
	tests := []struct {
		name string
		in   track.Track
		want string
	}{
		{"album track", track.Track{Title: "Barracuda", Artists: []string{"Heart"}, AlbumArtist: "Heart", Album: "Little Queen", TrackNo: 1, DiscNo: 1},
			"/music/Heart/Little Queen/01 Barracuda.flac"},
		{"second disc is prefixed", track.Track{Title: "Song", AlbumArtist: "A", Album: "B", TrackNo: 3, DiscNo: 2},
			"/music/A/B/2-03 Song.flac"},
		{"no track number", track.Track{Title: "Song", AlbumArtist: "A", Album: "B"}, "/music/A/B/Song.flac"},
		{"falls back to the first artist", track.Track{Title: "Song", Artists: []string{"Solo", "Guest"}, Album: "B", TrackNo: 2},
			"/music/Solo/B/02 Song.flac"},
		{"no artist", track.Track{Title: "Song", Album: "B"}, "/music/Unknown Artist/B/Song.flac"},
		{"no album", track.Track{Title: "Song", AlbumArtist: "A"}, "/music/A/Unknown Album/Song.flac"},
		{"unsafe characters", track.Track{Title: `Who's Next? <live>: "x"|y`, AlbumArtist: "AC/DC", Album: `a\b`},
			"/music/AC_DC/a_b/Who's Next_ _live__ _x__y.flac"},
		{"accents and non-latin text stay", track.Track{Title: "Été 夏", AlbumArtist: "Beyoncé", Album: "B"},
			"/music/Beyoncé/B/Été 夏.flac"},
		{"trailing dots and spaces go", track.Track{Title: "Song...  ", AlbumArtist: "A. ", Album: "B."}, "/music/A/B/Song.flac"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := Path(root, tt.in), filepath.FromSlash(tt.want); got != want {
				t.Errorf("Path = %q, want %q", got, want)
			}
		})
	}
}

func TestPathNeverEscapesTheRoot(t *testing.T) {
	root := filepath.FromSlash("/music")
	for _, evil := range []string{"..", "../..", ".", "", "../../etc/passwd", "/etc", `..\..`} {
		p := Path(root, track.Track{Title: evil, AlbumArtist: evil, Album: evil})
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			t.Errorf("title/artist/album %q gave %q, which leaves the root", evil, p)
		}
	}
}

func TestPathTruncatesLongNames(t *testing.T) {
	long := strings.Repeat("あ", 300)
	p := Path("/music", track.Track{Title: long, AlbumArtist: "A", Album: "B"})
	if n := len([]rune(strings.TrimSuffix(filepath.Base(p), ".flac"))); n > 100 {
		t.Errorf("file name is %d characters, want at most 100", n)
	}
}

func newIndex(t *testing.T) *Index {
	t.Helper()
	conn, err := db.Connect(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewIndex(conn)
}

// writeFLAC makes a minimal tagged FLAC file.
func writeFLAC(t *testing.T, path string, tags flacmeta.Tags) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := append([]byte("fLaC"), 0x80, 0, 0, 34)
	raw = append(raw, bytes.Repeat([]byte{1}, 34)...)
	raw = append(raw, "AUDIO"...)
	var out bytes.Buffer
	if err := flacmeta.Rewrite(bytes.NewReader(raw), &out, tags); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeMP3 makes a file with only an ID3v2.3 tag and a little fake audio.
func writeMP3(t *testing.T, path string, frames map[string]string) {
	t.Helper()
	var body []byte
	for id, text := range frames {
		payload := append([]byte{0}, text...)
		body = append(body, id...)
		body = binary.BigEndian.AppendUint32(body, uint32(len(payload)))
		body = append(body, 0, 0)
		body = append(body, payload...)
	}
	n := len(body)
	hdr := []byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append(hdr, body...), 0xff, 0xfb, 0x90, 0x00), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAddThenFindByISRCAndByMeta(t *testing.T) {
	x := newIndex(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "a.flac")
	writeFLAC(t, p, flacmeta.Tags{Title: "x"})

	if err := x.Add(ctx, Entry{Path: p, ISRC: "USSM10009075", Title: "Barracuda", Artists: []string{"Heart"}}); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := x.ByISRC(ctx, " ussm10009075 "); err != nil || !ok || got != p {
		t.Errorf("ByISRC = %q %v %v", got, ok, err)
	}
	if got, ok, err := x.ByMeta(ctx, "Barracuda - 2004 Remaster", []string{"The Heart"}); err != nil || !ok || got != p {
		t.Errorf("ByMeta = %q %v %v", got, ok, err)
	}
	if _, ok, _ := x.ByISRC(ctx, "USXX00000000"); ok {
		t.Error("found an ISRC that was never added")
	}
	if _, ok, _ := x.ByISRC(ctx, ""); ok {
		t.Error("an empty ISRC matched something")
	}
}

func TestAddReplacesAnEntryForTheSamePath(t *testing.T) {
	x := newIndex(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "a.flac")
	writeFLAC(t, p, flacmeta.Tags{Title: "x"})
	_ = x.Add(ctx, Entry{Path: p, ISRC: "OLD", Title: "Old", Artists: []string{"A"}})
	_ = x.Add(ctx, Entry{Path: p, ISRC: "NEW", Title: "New", Artists: []string{"A"}})
	if _, ok, _ := x.ByISRC(ctx, "OLD"); ok {
		t.Error("the old entry survived")
	}
	if _, ok, _ := x.ByISRC(ctx, "NEW"); !ok {
		t.Error("the new entry is missing")
	}
}

func TestByMetaNeedsTheSameVersionAndAnArtistInCommon(t *testing.T) {
	x := newIndex(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "a.flac")
	writeFLAC(t, p, flacmeta.Tags{Title: "x"})
	_ = x.Add(ctx, Entry{Path: p, Title: "Time", Artists: []string{"Pink Floyd"}})

	for _, tt := range []struct {
		title   string
		artists []string
		want    bool
	}{
		{"Time", []string{"Pink Floyd"}, true},
		{"time", []string{"PINK FLOYD", "Guest"}, true},
		{"Time (Live)", []string{"Pink Floyd"}, false},
		{"Time", []string{"Pink"}, false},
		{"Time", []string{"Hans Zimmer"}, false},
		{"Time", nil, false},
		{"Other", []string{"Pink Floyd"}, false},
	} {
		if _, ok, _ := x.ByMeta(ctx, tt.title, tt.artists); ok != tt.want {
			t.Errorf("ByMeta(%q, %v) = %v, want %v", tt.title, tt.artists, ok, tt.want)
		}
	}
}

func TestScanIndexesFLACAndMP3(t *testing.T) {
	x := newIndex(t)
	ctx := context.Background()
	root := t.TempDir()
	writeFLAC(t, filepath.Join(root, "Heart", "Little Queen", "01 Barracuda.flac"), flacmeta.Tags{
		Title: "Barracuda", Artists: []string{"Heart", "Someone Else"}, ISRC: "USSM10009075",
	})
	writeMP3(t, filepath.Join(root, "Muse", "Uprising.mp3"), map[string]string{
		"TIT2": "Uprising", "TPE1": "Muse", "TSRC": "GBAHT0900123",
	})
	writeMP3(t, filepath.Join(root, "Mixed", "Song.mp3"), map[string]string{
		"TIT2": "Jamaican (Bam Bam)", "TPE1": "HUGEL feat. SOLTO (FR)",
	})
	if err := os.WriteFile(filepath.Join(root, "cover.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := x.Scan(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 3 || st.Indexed != 3 || st.Unreadable != 0 {
		t.Errorf("stats = %+v, want 3 audio files indexed", st)
	}
	if _, ok, _ := x.ByISRC(ctx, "USSM10009075"); !ok {
		t.Error("FLAC ISRC not indexed")
	}
	if _, ok, _ := x.ByISRC(ctx, "GBAHT0900123"); !ok {
		t.Error("MP3 ISRC (TSRC) not indexed")
	}
	if _, ok, _ := x.ByMeta(ctx, "Barracuda", []string{"Heart"}); !ok {
		t.Error("the first of two FLAC artists is not findable")
	}
	if _, ok, _ := x.ByMeta(ctx, "Barracuda", []string{"Someone Else"}); !ok {
		t.Error("the second of two FLAC artists is not findable")
	}
	if _, ok, _ := x.ByMeta(ctx, "Jamaican (Bam Bam)", []string{"SOLTO (FR)"}); !ok {
		t.Error("a featured artist in an MP3 artist string is not findable")
	}
	if _, ok, _ := x.ByMeta(ctx, "Jamaican (Bam Bam)", []string{"HUGEL"}); !ok {
		t.Error("the main artist in an MP3 artist string is not findable")
	}
}

func TestScanIsIncremental(t *testing.T) {
	x := newIndex(t)
	ctx := context.Background()
	root := t.TempDir()
	a := filepath.Join(root, "a.flac")
	b := filepath.Join(root, "b.flac")
	writeFLAC(t, a, flacmeta.Tags{Title: "A", Artists: []string{"X"}, ISRC: "AAA"})
	writeFLAC(t, b, flacmeta.Tags{Title: "B", Artists: []string{"X"}, ISRC: "BBB"})
	if _, err := x.Scan(ctx, root); err != nil {
		t.Fatal(err)
	}

	st, err := x.Scan(ctx, root)
	if err != nil || st.Unchanged != 2 || st.Indexed != 0 {
		t.Fatalf("rescan = %+v, %v; want both files unchanged", st, err)
	}

	writeFLAC(t, a, flacmeta.Tags{Title: "A", Artists: []string{"X"}, ISRC: "CHANGED", Album: "longer, so the size differs"})
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	st, err = x.Scan(ctx, root)
	if err != nil || st.Indexed != 1 || st.Removed != 1 {
		t.Fatalf("after edit and delete = %+v, %v", st, err)
	}
	if _, ok, _ := x.ByISRC(ctx, "AAA"); ok {
		t.Error("the old ISRC of a changed file is still indexed")
	}
	if _, ok, _ := x.ByISRC(ctx, "CHANGED"); !ok {
		t.Error("the new ISRC is missing")
	}
	if _, ok, _ := x.ByISRC(ctx, "BBB"); ok {
		t.Error("a deleted file is still indexed")
	}
}

func TestScanOnlyRemovesEntriesUnderItsRoot(t *testing.T) {
	x := newIndex(t)
	ctx := context.Background()
	rootA, rootB := t.TempDir(), t.TempDir()
	writeFLAC(t, filepath.Join(rootB, "b.flac"), flacmeta.Tags{Title: "B", Artists: []string{"X"}, ISRC: "BBB"})
	if _, err := x.Scan(ctx, rootB); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Scan(ctx, rootA); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := x.ByISRC(ctx, "BBB"); !ok {
		t.Error("scanning another root dropped this one's entries")
	}
}

func TestScanSurvivesUnreadableFiles(t *testing.T) {
	x := newIndex(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.flac"), []byte("not really flac"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.mp3"), []byte("not really mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFLAC(t, filepath.Join(root, "ok.flac"), flacmeta.Tags{Title: "Fine", Artists: []string{"X"}, ISRC: "OK"})
	st, err := x.Scan(ctx, root)
	if err != nil || st.Unreadable != 2 || st.Indexed != 1 {
		t.Fatalf("stats = %+v, %v; want 2 unreadable and 1 indexed", st, err)
	}
	if _, ok, _ := x.ByISRC(ctx, "OK"); !ok {
		t.Error("a bad neighbour stopped the good file being indexed")
	}
}

func TestScanStopsWhenCancelled(t *testing.T) {
	x := newIndex(t)
	root := t.TempDir()
	writeFLAC(t, filepath.Join(root, "a.flac"), flacmeta.Tags{Title: "A", Artists: []string{"X"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := x.Scan(ctx, root); err == nil {
		t.Error("want the context error")
	}
}

func TestScanOfAMissingRootIsAnError(t *testing.T) {
	x := newIndex(t)
	if _, err := x.Scan(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("want an error for a root that does not exist")
	}
}

func TestAddOfAMissingFileIsAnError(t *testing.T) {
	x := newIndex(t)
	err := x.Add(context.Background(), Entry{Path: filepath.Join(t.TempDir(), "gone.flac"), Title: "x"})
	if err == nil {
		t.Error("want an error for a file that does not exist")
	}
}

func TestLookupIgnoresFilesThatHaveSinceGone(t *testing.T) {
	x := newIndex(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "a.flac")
	writeFLAC(t, p, flacmeta.Tags{Title: "x"})
	_ = x.Add(ctx, Entry{Path: p, ISRC: "GONE", Title: "Song", Artists: []string{"A"}})
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := x.ByISRC(ctx, "GONE"); ok {
		t.Error("ByISRC found a file that was deleted")
	}
	if _, ok, _ := x.ByMeta(ctx, "Song", []string{"A"}); ok {
		t.Error("ByMeta found a file that was deleted")
	}
}
