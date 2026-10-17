package monochrome

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

// cutThenServe aborts the first request after cut bytes, then honours Range.
func cutThenServe(body []byte, cut int, ranges *[]string) func(http.ResponseWriter, *http.Request) {
	first := true
	return func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		*ranges = append(*ranges, rng)
		if rng == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			if first {
				first = false
				_, _ = w.Write(body[:cut])
				w.(http.Flusher).Flush()
				panic(http.ErrAbortHandler)
			}
			_, _ = w.Write(body)
			return
		}
		var from int
		if _, err := fmt.Sscanf(rng, "bytes=%d-", &from); err != nil {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(body)-1, len(body)))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)-from))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[from:])
	}
}

func TestDownloadResumesAfterAConnectionDrop(t *testing.T) {
	f := newFake(t)
	var ranges []string
	f.track = cutThenServe(flacBytes, 600, &ranges)
	var buf bytes.Buffer
	n, err := f.client().Download(context.Background(), Track{ID: "1"}, &buf)
	if err != nil || n != int64(len(flacBytes)) || !bytes.Equal(buf.Bytes(), flacBytes) {
		t.Fatalf("n=%d err=%v equal=%v", n, err, bytes.Equal(buf.Bytes(), flacBytes))
	}
	if len(ranges) != 2 || ranges[1] != "bytes=600-" {
		t.Errorf("ranges = %q, want a resume from byte 600", ranges)
	}
}

func TestDownloadFailsWhenTheServerIgnoresRange(t *testing.T) {
	f := newFake(t)
	first := true
	f.track = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(flacBytes)))
		if first {
			first = false
			_, _ = w.Write(flacBytes[:600])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		_, _ = w.Write(flacBytes) // 200 from the start, ignoring Range
	}
	var buf bytes.Buffer
	_, err := f.client().Download(context.Background(), Track{ID: "1"}, &buf)
	if err == nil {
		t.Fatalf("want an error when a resume restarts from zero, got %d bytes", buf.Len())
	}
}
