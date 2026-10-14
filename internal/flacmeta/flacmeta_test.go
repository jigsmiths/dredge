package flacmeta

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhowden/tag"
)

type block struct {
	typ  byte
	data []byte
}

var streamInfo = block{blockStreamInfo, bytes.Repeat([]byte{0xA5}, 34)}

const frames = "FRAMES-ARE-OPAQUE-AUDIO-DATA"

func buildFLAC(blocks ...block) []byte {
	out := []byte("fLaC")
	for i, b := range blocks {
		h := b.typ
		if i == len(blocks)-1 {
			h |= 0x80
		}
		out = append(out, h, byte(len(b.data)>>16), byte(len(b.data)>>8), byte(len(b.data)))
		out = append(out, b.data...)
	}
	return append(out, frames...)
}

func vorbis(vendor string, comments ...string) block {
	d := binary.LittleEndian.AppendUint32(nil, uint32(len(vendor)))
	d = append(d, vendor...)
	d = binary.LittleEndian.AppendUint32(d, uint32(len(comments)))
	for _, c := range comments {
		d = binary.LittleEndian.AppendUint32(d, uint32(len(c)))
		d = append(d, c...)
	}
	return block{blockVorbisComment, d}
}

var full = Tags{
	Title: "Barracuda", Artists: []string{"Heart", "Someone Else"}, Album: "Little Queen",
	AlbumArtist: "Heart", TrackNo: 1, DiscNo: 1, Date: "1977-05-14", ISRC: "USSM10009075",
}

func rewrite(t *testing.T, in []byte, tags Tags) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := Rewrite(bytes.NewReader(in), &out, tags); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// metaBlocks lists the block types and the last flags of a FLAC stream.
func metaBlocks(t *testing.T, b []byte) (types []byte, lasts []bool, rest []byte) {
	t.Helper()
	if !bytes.HasPrefix(b, []byte("fLaC")) {
		t.Fatal("no fLaC marker")
	}
	r := b[4:]
	for {
		h := r[0]
		n := int(r[1])<<16 | int(r[2])<<8 | int(r[3])
		types, lasts = append(types, h&0x7f), append(lasts, h&0x80 != 0)
		r = r[4+n:]
		if h&0x80 != 0 {
			return types, lasts, r
		}
	}
}

func TestRewriteWritesTheTagsAnotherReaderSees(t *testing.T) {
	got := rewrite(t, buildFLAC(streamInfo), full)
	m, err := tag.ReadFrom(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	if m.Title() != "Barracuda" || m.Album() != "Little Queen" || m.AlbumArtist() != "Heart" || m.Year() != 1977 {
		t.Errorf("title=%q album=%q albumartist=%q year=%d", m.Title(), m.Album(), m.AlbumArtist(), m.Year())
	}
	if n, _ := m.Track(); n != 1 {
		t.Errorf("track = %d", n)
	}
	if n, _ := m.Disc(); n != 1 {
		t.Errorf("disc = %d", n)
	}
	raw := m.Raw()
	if raw["isrc"] != "USSM10009075" && raw["ISRC"] != "USSM10009075" {
		t.Errorf("ISRC missing from %v", raw)
	}
}

func TestReadReturnsEverythingRewriteWrote(t *testing.T) {
	got, err := Read(bytes.NewReader(rewrite(t, buildFLAC(streamInfo), full)))
	if err != nil {
		t.Fatal(err)
	}
	want := full
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Read = %+v, want %+v", got, want)
	}
}

func TestReadKeepsEveryArtist(t *testing.T) {
	in := buildFLAC(streamInfo, vorbis("v", "ARTIST=A", "ARTIST=B", "ARTIST=C"))
	got, err := Read(bytes.NewReader(in))
	if err != nil || fmt.Sprint(got.Artists) != "[A B C]" {
		t.Fatalf("artists = %v, %v", got.Artists, err)
	}
}

func TestReadAcceptsLowerCaseKeysAndTrackOfTotal(t *testing.T) {
	in := buildFLAC(streamInfo, vorbis("v", "title=Song", "tracknumber=3/12", "discnumber=2/2", "Isrc=GB123"))
	got, err := Read(bytes.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Song" || got.TrackNo != 3 || got.DiscNo != 2 || got.ISRC != "GB123" {
		t.Errorf("got %+v", got)
	}
}

func TestReadSkipsBigBlocksAndStopsBeforeTheAudio(t *testing.T) {
	big := block{blockPicture, bytes.Repeat([]byte{1}, 5<<20)}
	in := buildFLAC(streamInfo, big, vorbis("v", "TITLE=After the picture"))
	r := bytes.NewReader(in)
	got, err := Read(r)
	if err != nil || got.Title != "After the picture" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if r.Len() != len(frames) {
		t.Errorf("%d bytes left unread, want just the %d of audio", r.Len(), len(frames))
	}
}

func TestReadWithoutAVorbisBlockIsEmptyNotAnError(t *testing.T) {
	got, err := Read(bytes.NewReader(buildFLAC(streamInfo)))
	if err != nil || got.Title != "" || len(got.Artists) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestReadRejectsNonFLACAndTruncatedFiles(t *testing.T) {
	if _, err := Read(strings.NewReader("ID3\x04")); err == nil {
		t.Error("want an error for a non-FLAC file")
	}
	if _, err := Read(bytes.NewReader(buildFLAC(streamInfo)[:20])); err == nil {
		t.Error("want an error for a truncated file")
	}
}

func TestRewriteWritesEachArtistAsItsOwnComment(t *testing.T) {
	got := rewrite(t, buildFLAC(streamInfo), full)
	if n := bytes.Count(got, []byte("ARTIST=")) - bytes.Count(got, []byte("ALBUMARTIST=")); n != 2 {
		t.Errorf("found %d ARTIST comments, want 2", n)
	}
}

func TestRewriteKeepsStreamInfoAndFramesUntouched(t *testing.T) {
	got := rewrite(t, buildFLAC(streamInfo), full)
	types, _, rest := metaBlocks(t, got)
	if types[0] != blockStreamInfo {
		t.Errorf("first block type = %d, want STREAMINFO", types[0])
	}
	if !bytes.Contains(got, streamInfo.data) {
		t.Error("STREAMINFO changed")
	}
	if string(rest) != frames {
		t.Errorf("frames = %q, want them unchanged", rest)
	}
}

func TestRewriteReplacesOldTagsPictureAndPadding(t *testing.T) {
	old := buildFLAC(
		streamInfo,
		vorbis("old", "TITLE=Stale", "COMMENT=keep? no"),
		block{blockPadding, make([]byte, 100)},
		block{blockPicture, []byte("stale picture")},
	)
	got := rewrite(t, old, full)
	if bytes.Contains(got, []byte("Stale")) || bytes.Contains(got, []byte("stale picture")) {
		t.Error("old tag or picture survived")
	}
	types, _, _ := metaBlocks(t, got)
	count := map[byte]int{}
	for _, ty := range types {
		count[ty]++
	}
	if count[blockVorbisComment] != 1 || count[blockPadding] != 0 || count[blockPicture] != 0 {
		t.Errorf("block counts = %v", count)
	}
}

func TestRewriteKeepsOtherBlocks(t *testing.T) {
	seek := block{blockSeekTable, bytes.Repeat([]byte{7}, 36)}
	got := rewrite(t, buildFLAC(streamInfo, seek, vorbis("x")), full)
	types, _, _ := metaBlocks(t, got)
	if !bytes.Contains(got, seek.data) || types[1] != blockSeekTable {
		t.Errorf("types = %v, seek table lost or moved", types)
	}
}

func TestRewriteSetsTheLastFlagOnlyOnTheFinalBlock(t *testing.T) {
	for _, cover := range [][]byte{nil, []byte("\xff\xd8\xffjpeg")} {
		tags := full
		tags.Cover = cover
		_, lasts, _ := metaBlocks(t, rewrite(t, buildFLAC(streamInfo, vorbis("x")), tags))
		for i, l := range lasts {
			if l != (i == len(lasts)-1) {
				t.Errorf("cover=%v: last flags = %v", cover != nil, lasts)
			}
		}
	}
}

func TestRewriteEmbedsTheCover(t *testing.T) {
	for name, tc := range map[string]struct {
		data []byte
		mime string
	}{
		"jpeg": {[]byte("\xff\xd8\xff\xe0jpegdata"), "image/jpeg"},
		"png":  {[]byte("\x89PNG\r\n\x1a\npngdata"), "image/png"},
	} {
		t.Run(name, func(t *testing.T) {
			tags := full
			tags.Cover = tc.data
			m, err := tag.ReadFrom(bytes.NewReader(rewrite(t, buildFLAC(streamInfo), tags)))
			if err != nil {
				t.Fatal(err)
			}
			p := m.Picture()
			if p == nil || p.MIMEType != tc.mime || !bytes.Equal(p.Data, tc.data) {
				t.Errorf("picture = %+v", p)
			}
		})
	}
}

func TestRewriteSkipsACoverItCannotIdentify(t *testing.T) {
	tags := full
	tags.Cover = []byte("<html>not an image</html>")
	got := rewrite(t, buildFLAC(streamInfo), tags)
	types, _, _ := metaBlocks(t, got)
	for _, ty := range types {
		if ty == blockPicture {
			t.Error("embedded a picture that is not a JPEG or PNG")
		}
	}
}

func TestRewriteSkipsEmptyFields(t *testing.T) {
	got := rewrite(t, buildFLAC(streamInfo), Tags{Title: "Only", Artists: []string{"A", " "}})
	for _, key := range []string{"ALBUM=", "TRACKNUMBER=", "DISCNUMBER=", "DATE=", "ISRC=", "ALBUMARTIST="} {
		if bytes.Contains(got, []byte(key)) {
			t.Errorf("wrote %s for an empty field", key)
		}
	}
	if bytes.Count(got, []byte("ARTIST=")) != 1 {
		t.Error("a blank artist was written")
	}
}

func TestRewriteRejectsNonFLAC(t *testing.T) {
	var out bytes.Buffer
	if err := Rewrite(strings.NewReader("ID3\x04 not flac at all"), &out, full); err == nil {
		t.Fatal("want an error")
	}
	if out.Len() != 0 {
		t.Errorf("wrote %d bytes of a rejected file", out.Len())
	}
}

func TestRewriteRejectsTruncatedMetadata(t *testing.T) {
	in := buildFLAC(streamInfo)[:20]
	if err := Rewrite(bytes.NewReader(in), &bytes.Buffer{}, full); err == nil {
		t.Fatal("want an error for a file cut inside its metadata")
	}
}

func TestRewriteRejectsAFileWithoutStreamInfo(t *testing.T) {
	in := buildFLAC(block{blockSeekTable, make([]byte, 18)})
	if err := Rewrite(bytes.NewReader(in), &bytes.Buffer{}, full); err == nil {
		t.Fatal("want an error when STREAMINFO is missing")
	}
}

func TestRetagReplacesTheFileInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.flac")
	if err := os.WriteFile(path, buildFLAC(streamInfo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Retag(path, full); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte("TITLE=Barracuda")) || !strings.HasSuffix(string(b), frames) {
		t.Error("file was not retagged with its audio intact")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files left in the directory, want only the tagged one", len(entries))
	}
}

func TestRetagLeavesTheOriginalOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.flac")
	orig := []byte("not a flac file")
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Retag(path, full); err == nil {
		t.Fatal("want an error")
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, orig) {
		t.Error("the original file was changed")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files left behind, want 1", len(entries))
	}
}
