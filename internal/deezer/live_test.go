package deezer

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jigsmiths/dredge/internal/match"
	"github.com/jigsmiths/dredge/internal/track"
)

func TestLiveSearchFindsTheAlbumRecording(t *testing.T) {
	if os.Getenv("DREDGE_LIVE") == "" {
		t.Skip("set DREDGE_LIVE=1 to talk to Deezer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cands, err := New("", nil).Search(ctx, "Heart Barracuda", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := track.Track{Title: "Barracuda", Artists: []string{"Heart"}, Album: "Little Queen", DurationMS: 261495}
	got := match.Pick(want, cands)
	if got.Outcome != match.Matched || got.Best.ISRC != "USSM10009075" {
		t.Errorf("Pick = %v %s (%s)", got.Outcome, got.Best.ISRC, got.Reason)
	}
}
