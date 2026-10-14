package spotify

import (
	"context"
	"os"
	"testing"
	"time"
)

func liveClient(t *testing.T) (*Client, context.Context) {
	t.Helper()
	if os.Getenv("DREDGE_LIVE") == "" {
		t.Skip("set DREDGE_LIVE=1 to talk to Spotify")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return New("", "", nil), ctx
}

func TestLivePlaylistOver100Tracks(t *testing.T) {
	c, ctx := liveClient(t)
	got, err := c.Fetch(ctx, Ref{KindPlaylist, "37i9dQZF1DWXRqgorJj26U"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tracks)+got.Skipped <= 100 {
		t.Errorf("got %d tracks and %d skipped, want more than 100", len(got.Tracks), got.Skipped)
	}
	if got.Tracks[0].Title == "" || len(got.Tracks[0].Artists) == 0 || got.Tracks[0].DurationMS == 0 {
		t.Errorf("first track = %+v", got.Tracks[0])
	}
}

func TestLiveAlbum(t *testing.T) {
	c, ctx := liveClient(t)
	got, err := c.Fetch(ctx, Ref{KindAlbum, "1LaeNhiUpL3X6N0LcFvuDF"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Little Queen" || len(got.Tracks) != 10 {
		t.Errorf("album = %q with %d tracks", got.Name, len(got.Tracks))
	}
}

func TestLiveTrack(t *testing.T) {
	c, ctx := liveClient(t)
	got, err := c.Fetch(ctx, Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tracks[0].Title != "Jamaican (Bam Bam)" || len(got.Tracks[0].Artists) != 2 {
		t.Errorf("track = %+v", got.Tracks[0])
	}
}

func TestLiveStaleHashRecovers(t *testing.T) {
	c, ctx := liveClient(t)
	c.hashes["getTrack"] = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := c.Fetch(ctx, Ref{KindTrack, "7e4zDInS6tA2jwzphvs2Ay"}); err != nil {
		t.Fatalf("a stale hash should be re-scraped: %v", err)
	}
}
