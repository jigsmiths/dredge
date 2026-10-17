package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jigsmiths/dredge/internal/config"
	"github.com/jigsmiths/dredge/internal/db"
	"github.com/jigsmiths/dredge/internal/flacmeta"
	"github.com/jigsmiths/dredge/internal/library"
	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/resolve"
	"github.com/jigsmiths/dredge/internal/spotify"
	"github.com/jigsmiths/dredge/internal/track"
)

const (
	playlistID = "37i9dQZF1DWXRqgorJj26U"
	albumID    = "1LaeNhiUpL3X6N0LcFvuDF"
	trackID    = "7e4zDInS6tA2jwzphvs2Ay"
)

func link(kind, id string) string { return "https://open.spotify.com/" + kind + "/" + id }

type fakeSpotify struct {
	collections map[string]*spotify.Collection
	err         error
}

func (f *fakeSpotify) Fetch(_ context.Context, ref spotify.Ref) (*spotify.Collection, error) {
	if f.err != nil {
		return nil, f.err
	}
	if c, ok := f.collections[ref.ID]; ok {
		return c, nil
	}
	return nil, errors.New("not found")
}

type fakeSearch struct{ results map[string][]match.Candidate }

func (s *fakeSearch) Search(_ context.Context, q string, _ int) ([]match.Candidate, error) {
	return s.results[q], nil
}

type fakeSource struct {
	stocked map[string]bool
	failFor map[string]error
	stall   bool // downloads wait until the context ends
}

func (s *fakeSource) Find(_ context.Context, isrc, _, title string) (monochrome.Track, error) {
	if !s.stocked[isrc] {
		return monochrome.Track{}, monochrome.ErrNotStocked
	}
	return monochrome.Track{ID: isrc, ISRC: isrc, Title: title}, nil
}

func (s *fakeSource) Download(ctx context.Context, t monochrome.Track, w io.Writer) (int64, error) {
	if s.stall {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	if err := s.failFor[t.ISRC]; err != nil {
		return 0, err
	}
	b := append([]byte("fLaC"), 0x80, 0, 0, 34)
	b = append(b, make([]byte, 34)...) // a STREAMINFO that declares no length
	b = append(b, "AUDIO"...)
	_, err := w.Write(b)
	return int64(len(b)), err
}

func (s *fakeSource) Artwork(context.Context, monochrome.Track) ([]byte, error) { return nil, nil }

type rig struct {
	t       *testing.T
	root    string
	spot    *fakeSpotify
	search  *fakeSearch
	src     *fakeSource
	details resolve.Detailer
	out     bytes.Buffer
	errw    bytes.Buffer
	e       *env
}

func cand(isrc, title, artist string, ms int) match.Candidate {
	return match.Candidate{ISRC: isrc, Title: title, Artists: []string{artist}, DurationMS: ms}
}

func tr(title, artist string, ms int) track.Track {
	return track.Track{Title: title, Artists: []string{artist}, Album: "Little Queen", AlbumArtist: artist, TrackNo: 1, DiscNo: 1, DurationMS: ms}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, root: t.TempDir()}
	r.spot = &fakeSpotify{collections: map[string]*spotify.Collection{
		playlistID: {Kind: spotify.KindPlaylist, Name: "Roadtrip", Tracks: []track.Track{
			tr("Barracuda", "Heart", 261495), tr("Uprising", "Muse", 304840), tr("Unknown", "Nobody", 1000),
		}},
		albumID: {Kind: spotify.KindAlbum, Name: "Little Queen", Tracks: []track.Track{tr("Barracuda", "Heart", 261495)}},
		trackID: {Kind: spotify.KindTrack, Name: "Barracuda", Tracks: []track.Track{tr("Barracuda", "Heart", 261495)}},
	}}
	r.search = &fakeSearch{results: map[string][]match.Candidate{
		"Heart barracuda": {cand("USSM10009075", "Barracuda", "Heart", 262000)},
	}}
	r.src = &fakeSource{stocked: map[string]bool{"USSM10009075": true}}

	conn, err := db.Connect(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	idx := library.NewIndex(conn)

	r.e = &env{
		out: &r.out, err: &r.errw,
		cfg: &config.Config{MusicDir: r.root, SinglesDir: "Singles", Workers: 2, DBPath: "unused"},
		open: func(*config.Config) (*deps, error) {
			return &deps{spotify: r.spot, search: r.search, source: r.src, details: r.details, index: idx, close: func() {}}, nil
		},
	}
	return r
}

func (r *rig) run(args ...string) error {
	return r.runCtx(context.Background(), args...)
}

func (r *rig) runCtx(ctx context.Context, args ...string) error {
	r.out.Reset()
	r.errw.Reset()
	return run(ctx, r.e, args)
}

// have puts a tagged FLAC in the library, as an earlier download would.
func (r *rig) have(rel string, tags flacmeta.Tags) string {
	r.t.Helper()
	p := filepath.Join(r.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	raw := append([]byte("fLaC"), 0x80, 0, 0, 34)
	raw = append(raw, bytes.Repeat([]byte{1}, 34)...)
	raw = append(raw, "AUDIO"...)
	var buf bytes.Buffer
	if err := flacmeta.Rewrite(bytes.NewReader(raw), &buf, tags); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *rig) cfgWorkers(n int) { r.e.cfg.Workers = n }

func (r *rig) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(rel)))
	return err == nil
}

func TestResolveListsEachTrackWithItsStatus(t *testing.T) {
	r := newRig(t)
	r.have("Singles/Muse/Uprising.flac", flacmeta.Tags{Title: "Uprising", Artists: []string{"Muse"}})
	if err := r.run("resolve", link("playlist", playlistID)); err != nil {
		t.Fatal(err)
	}
	out := r.out.String()
	for _, want := range []string{
		"Roadtrip", "3 tracks",
		"need", "Heart - Barracuda", "USSM10009075",
		"have", "Muse - Uprising", "Singles/Muse/Uprising.flac",
		"no_match", "Nobody - Unknown",
		"1 need", "1 have", "1 no match",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if r.exists("Singles/Heart/Barracuda.flac") {
		t.Error("resolve downloaded something")
	}
}

func TestResolveScansTheLibraryFirstUnlessToldNot(t *testing.T) {
	r := newRig(t)
	r.have("Singles/Muse/Uprising.flac", flacmeta.Tags{Title: "Uprising", Artists: []string{"Muse"}})
	if err := r.run("resolve", "--no-scan", link("playlist", playlistID)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.out.String(), "Singles/Muse/Uprising.flac") {
		t.Error("found a library file without scanning it")
	}
}

func TestGetDownloadsFilesAndWritesThePlaylist(t *testing.T) {
	r := newRig(t)
	r.have("Singles/Muse/Uprising.flac", flacmeta.Tags{Title: "Uprising", Artists: []string{"Muse"}})
	if err := r.run("get", link("playlist", playlistID)); err != nil {
		t.Fatalf("get: %v\n%s", err, r.errw.String())
	}
	if !r.exists("Singles/Heart/Barracuda.flac") {
		t.Fatal("the needed track was not downloaded")
	}
	m3u := filepath.Join(r.root, "Singles", "Roadtrip (Spotify "+playlistID+").m3u")
	b, err := os.ReadFile(m3u)
	if err != nil {
		t.Fatalf("no playlist written: %v", err)
	}
	if want := "#EXTM3U\nHeart/Barracuda.flac\nMuse/Uprising.flac\n"; string(b) != want {
		t.Errorf("playlist = %q, want %q", b, want)
	}
	out := r.out.String()
	for _, want := range []string{"downloaded", "Heart - Barracuda", "1 downloaded", "Roadtrip (Spotify"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestGetIsIdempotent(t *testing.T) {
	r := newRig(t)
	url := link("playlist", playlistID)
	if err := r.run("get", url); err != nil {
		t.Fatal(err)
	}
	if err := r.run("get", url); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.out.String(), "downloaded") && !strings.Contains(r.out.String(), "0 downloaded") {
		t.Errorf("a second run downloaded again:\n%s", r.out.String())
	}
}

func TestGetOfAnAlbumFilesItUnderArtistAndAlbumWithoutAPlaylist(t *testing.T) {
	r := newRig(t)
	if err := r.run("get", link("album", albumID)); err != nil {
		t.Fatal(err)
	}
	if !r.exists("Heart/Little Queen/01 Barracuda.flac") {
		t.Error("album track not filed as Artist/Album/NN Title")
	}
	if matches, _ := filepath.Glob(filepath.Join(r.root, "Singles", "*.m3u")); len(matches) != 0 {
		t.Errorf("an album wrote a playlist: %v", matches)
	}
}

func TestGetOfASingleTrackGoesToSingles(t *testing.T) {
	r := newRig(t)
	if err := r.run("get", link("track", trackID)); err != nil {
		t.Fatal(err)
	}
	if !r.exists("Singles/Heart/Barracuda.flac") {
		t.Error("track not filed under Singles")
	}
	if matches, _ := filepath.Glob(filepath.Join(r.root, "Singles", "*.m3u")); len(matches) != 0 {
		t.Errorf("a track wrote a playlist: %v", matches)
	}
}

func TestGetReportsFailuresButFinishesTheRest(t *testing.T) {
	r := newRig(t)
	r.search.results["Muse uprising"] = []match.Candidate{cand("GBAHT0900123", "Uprising", "Muse", 305000)}
	r.src.stocked["GBAHT0900123"] = true
	r.src.failFor = map[string]error{"GBAHT0900123": errors.New("connection reset")}
	err := r.run("get", "--attempts", "1", link("playlist", playlistID))
	if err == nil || !strings.Contains(err.Error(), "1 track failed") {
		t.Fatalf("err = %v, want one saying 1 track failed", err)
	}
	if !r.exists("Singles/Heart/Barracuda.flac") {
		t.Error("the good track was not downloaded after the other failed")
	}
	if !strings.Contains(r.out.String(), "failed") || !strings.Contains(r.out.String(), "connection reset") {
		t.Errorf("output does not say what failed:\n%s", r.out.String())
	}
	if matches, _ := filepath.Glob(filepath.Join(r.root, "Singles", ".dredge-*")); len(matches) != 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}

func TestInterruptingGetReportsItOnceInsteadOfFailingEveryTrack(t *testing.T) {
	r := newRig(t)
	r.cfgWorkers(1)
	r.search.results["Muse uprising"] = []match.Candidate{cand("GBAHT0900123", "Uprising", "Muse", 305000)}
	r.src.stocked["GBAHT0900123"] = true
	r.src.stall = true
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err := r.runCtx(ctx, "get", link("playlist", playlistID))
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("err = %v, want one saying the run was interrupted", err)
	}
	out := r.out.String()
	if strings.Contains(out, "context canceled") || strings.Contains(out, "failed ") {
		t.Errorf("an interruption was reported as failures:\n%s", out)
	}
	if !strings.Contains(out, "interrupted") {
		t.Errorf("output does not say it was interrupted:\n%s", out)
	}
	if matches, _ := filepath.Glob(filepath.Join(r.root, "Singles", ".dredge-*")); len(matches) != 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}

func TestGetSaysWhenATrackIsNotOnMonochrome(t *testing.T) {
	r := newRig(t)
	r.src.stocked = map[string]bool{}
	if err := r.run("get", link("track", trackID)); err != nil {
		t.Fatalf("a track the catalogue lacks is not a failure: %v", err)
	}
	if !strings.Contains(r.out.String(), "not on Monochrome") {
		t.Errorf("output:\n%s", r.out.String())
	}
}

func TestMusicAndSinglesFlagsChooseWhereThingsGo(t *testing.T) {
	r := newRig(t)
	other := t.TempDir()
	if err := r.run("get", "--music", other, "--singles-dir", "Loose", link("track", trackID)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, "Loose", "Heart", "Barracuda.flac")); err != nil {
		t.Errorf("not in the chosen place: %v", err)
	}
	if r.exists("Singles/Heart/Barracuda.flac") {
		t.Error("also wrote to the configured music dir")
	}
}

func TestFlagsMayFollowTheLink(t *testing.T) {
	r := newRig(t)
	if err := r.run("resolve", link("track", trackID), "--no-scan"); err != nil {
		t.Fatalf("flag after the link: %v", err)
	}
}

func TestScanReportsWhatItFound(t *testing.T) {
	r := newRig(t)
	r.have("Singles/Muse/Uprising.flac", flacmeta.Tags{Title: "Uprising", Artists: []string{"Muse"}})
	if err := r.run("scan"); err != nil {
		t.Fatal(err)
	}
	if out := r.out.String(); !strings.Contains(out, "1 file") || !strings.Contains(out, "1 new") {
		t.Errorf("output:\n%s", out)
	}
}

func TestUsageErrors(t *testing.T) {
	r := newRig(t)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no command":        {nil, "usage"},
		"unknown command":   {[]string{"frobnicate"}, "unknown command"},
		"get without link":  {[]string{"get"}, "link"},
		"resolve no link":   {[]string{"resolve"}, "link"},
		"two links":         {[]string{"get", "a", "b"}, "one link"},
		"not a spotify url": {[]string{"get", "Heart - Barracuda"}, "not a spotify link"},
		"artist link":       {[]string{"get", "https://open.spotify.com/artist/5PlfkPxwCpRRWQJBxCa0By"}, "not supported"},
		"bad workers":       {[]string{"get", "--workers", "0", link("track", trackID)}, "workers"},
		"bad attempts":      {[]string{"get", "--attempts", "0", link("track", trackID)}, "attempts"},
		"unknown flag":      {[]string{"get", "--nope", link("track", trackID)}, "nope"},
	} {
		t.Run(name, func(t *testing.T) {
			err := r.run(tc.args...)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestASpotifyErrorIsReturned(t *testing.T) {
	r := newRig(t)
	r.spot.err = errors.New("spotify: HTTP 429")
	err := r.run("resolve", link("track", trackID))
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("err = %v", err)
	}
}

func TestPlaylistItemsThatAreNotTracksAreMentioned(t *testing.T) {
	r := newRig(t)
	r.spot.collections[playlistID].Skipped = 2
	if err := r.run("resolve", link("playlist", playlistID)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "2 items") {
		t.Errorf("output does not mention the skipped items:\n%s", r.out.String())
	}
}

func TestVersion(t *testing.T) {
	r := newRig(t)
	if err := r.run("version"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.out.String(), "dredge ") {
		t.Errorf("output = %q", r.out.String())
	}
}
