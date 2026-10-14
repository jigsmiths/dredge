package monochrome

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

var errCapped = errors.New("capped")

// capWriter takes the first limit bytes, then fails the download.
type capWriter struct {
	buf   bytes.Buffer
	limit int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.limit {
		return 0, errCapped
	}
	return c.buf.Write(p)
}

func TestLiveFindAndDownload(t *testing.T) {
	if os.Getenv("DREDGE_LIVE") == "" {
		t.Skip("set DREDGE_LIVE=1 to talk to Monochrome")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := New("", "", nil)

	tr, err := c.Find(ctx, "USSM10009075", "Heart", "Barracuda")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Title != "Barracuda" || tr.ID == "" {
		t.Fatalf("track = %+v", tr)
	}

	art, err := c.Artwork(ctx, tr)
	if err != nil || len(art) < 1000 {
		t.Errorf("artwork: %d bytes, %v", len(art), err)
	}

	w := &capWriter{limit: 256 << 10}
	_, err = c.Download(ctx, tr, w)
	if !errors.Is(err, errCapped) || !bytes.HasPrefix(w.buf.Bytes(), []byte("fLaC")) {
		t.Errorf("download: err=%v, wrote %d bytes", err, w.buf.Len())
	}
}

func TestLiveUnknownISRCIsNotStocked(t *testing.T) {
	if os.Getenv("DREDGE_LIVE") == "" {
		t.Skip("set DREDGE_LIVE=1 to talk to Monochrome")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := New("", "", nil).Find(ctx, "ZZZZ00000000", "Heart", "Barracuda")
	if !errors.Is(err, ErrNotStocked) {
		t.Errorf("err = %v, want ErrNotStocked", err)
	}
}
