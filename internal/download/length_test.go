package download

import (
	"context"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/jigsmiths/dredge/internal/monochrome"
	"github.com/jigsmiths/dredge/internal/resolve"
)

// flacOfLength is a FLAC whose STREAMINFO declares ms milliseconds at 44.1kHz.
func flacOfLength(ms int) []byte {
	info := make([]byte, 34)
	samples := uint64(ms) * 44100 / 1000
	binary.BigEndian.PutUint64(info[10:18], uint64(44100)<<44|uint64(1)<<41|uint64(15)<<36|samples)
	b := append([]byte("fLaC"), 0x80, 0, 0, 34)
	b = append(b, info...)
	return append(b, audio...)
}

func serving(ms int) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := w.Write(flacOfLength(ms))
		return err
	}
}

func wanting(ms int) resolve.Item {
	it := need("I", "Barracuda", "Heart")
	it.Track.DurationMS = ms
	return it
}

func TestAFileOfTheWantedLengthIsKept(t *testing.T) {
	opt, root := setup(t)
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: serving(262000)}
	res := Run(context.Background(), []resolve.Item{wanting(261933)}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Downloaded || len(files(t, root)) != 1 {
		t.Fatalf("result = %+v, files = %v", res[0], files(t, root))
	}
}

func TestAFileOfAnotherLengthIsRejectedAndNotRetried(t *testing.T) {
	opt, root := setup(t)
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: serving(325000)}
	res := Run(context.Background(), []resolve.Item{wanting(261933)}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Failed || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "long") {
		t.Fatalf("result = %+v, want a failure about the length", res[0])
	}
	if got := files(t, root); len(got) != 0 {
		t.Errorf("files left behind: %v", got)
	}
	if src.downloads != 1 {
		t.Errorf("downloaded %d times, want 1: another try cannot fix a different recording", src.downloads)
	}
}

func TestALengthIsNotCheckedWhenNoneIsWanted(t *testing.T) {
	opt, _ := setup(t)
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: serving(325000)}
	res := Run(context.Background(), []resolve.Item{wanting(0)}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Downloaded {
		t.Fatalf("result = %+v", res[0])
	}
}

func TestAFileThatDeclaresNoLengthIsNotRejected(t *testing.T) {
	opt, _ := setup(t)
	src := &fakeSource{stocked: map[string]bool{"I": true}, body: serving(0)}
	res := Run(context.Background(), []resolve.Item{wanting(261933)}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Downloaded {
		t.Fatalf("result = %+v", res[0])
	}
}

func TestThePicksOwnLengthIsUsedWhenTheTrackHasNone(t *testing.T) {
	opt, root := setup(t)
	it := wanting(0)
	it.Found = &monochrome.Track{ID: "1", ISRC: "I", DurationMS: 261933}
	src := &fakeSource{body: serving(325000)}
	res := Run(context.Background(), []resolve.Item{it}, src, &fakeRecorder{}, opt)
	if res[0].Outcome != Failed || len(files(t, root)) != 0 {
		t.Fatalf("result = %+v, files = %v", res[0], files(t, root))
	}
}
