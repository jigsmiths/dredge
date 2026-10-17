package download

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/resolve"
	"github.com/jigsmiths/dredge/internal/track"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func captureLogs(t *testing.T) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// failingSource fails the download of chosen titles.
type failingSource struct {
	*fakeSource
	errs map[string]error
}

func (s *failingSource) Download(ctx context.Context, t monochrome.Track, w io.Writer) (int64, error) {
	if err := s.errs[t.Title]; err != nil {
		return 0, err
	}
	return s.fakeSource.Download(ctx, t, w)
}

func TestEveryTrackOutcomeIsLogged(t *testing.T) {
	logs := captureLogs(t)
	opt, _ := setup(t)
	opt.Attempts = 1
	src := &failingSource{
		fakeSource: &fakeSource{stocked: map[string]bool{"OK": true, "LOSSY": true, "BAD": true}},
		errs:       map[string]error{"Aac": monochrome.ErrLossyOnly, "Broken": errors.New("stream error")},
	}
	items := []resolve.Item{
		need("OK", "Barracuda", "Heart"),
		need("MISSING", "Nowhere", "Nobody"),
		need("LOSSY", "Aac", "Band"),
		need("BAD", "Broken", "Band"),
		{Status: resolve.Have, Path: "/music/x.flac", Track: track.Track{Title: "Owned", Artists: []string{"Band"}}},
		{Status: resolve.NoMatch, Reason: "no candidate", Track: track.Track{Title: "Obscure", Artists: []string{"Band"}}},
	}
	Run(context.Background(), items, src, &fakeRecorder{}, opt)

	out := logs.String()
	for _, want := range []string{
		`msg="track started" title=Barracuda artist=Heart`,
		`msg="track finished" title=Barracuda artist=Heart outcome=downloaded`,
		`msg="track finished" title=Nowhere artist=Nobody outcome=not_stocked`,
		`msg="track finished" title=Aac artist=Band outcome=lossy_only`,
		`msg="track finished" title=Broken artist=Band outcome=failed`,
		`msg="track finished" title=Owned artist=Band outcome=skipped status=have`,
		`msg="track finished" title=Obscure artist=Band outcome=skipped status=no_match`,
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("logs lack %q:\n%s", want, out)
		}
	}
}
