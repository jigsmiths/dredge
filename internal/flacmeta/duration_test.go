package flacmeta

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// streamInfoFor is a STREAMINFO block for a stream of the given length at
// 44.1kHz; zero samples means the length is unknown.
func streamInfoFor(samples uint64) block {
	d := make([]byte, 34)
	binary.BigEndian.PutUint64(d[10:18], uint64(44100)<<44|uint64(1)<<41|uint64(15)<<36|samples)
	return block{blockStreamInfo, d}
}

func TestDurationMSReadsTheStreamInfo(t *testing.T) {
	in := buildFLAC(streamInfoFor(44100*180+22050), vorbis("x"))
	got, err := DurationMS(bytes.NewReader(in))
	if err != nil || got != 180500 {
		t.Fatalf("DurationMS = %d, %v; want 180500", got, err)
	}
}

func TestDurationMSIsZeroWhenTheStreamDoesNotSayHowLong(t *testing.T) {
	got, err := DurationMS(bytes.NewReader(buildFLAC(streamInfoFor(0))))
	if err != nil || got != 0 {
		t.Fatalf("DurationMS = %d, %v; want 0", got, err)
	}
}

func TestDurationMSRejectsWhatIsNotAFLACWithStreamInfoFirst(t *testing.T) {
	for name, in := range map[string][]byte{
		"not flac":         []byte("ID3\x04 and then some"),
		"empty":            nil,
		"no streaminfo":    buildFLAC(vorbis("x")),
		"short streaminfo": buildFLAC(block{blockStreamInfo, make([]byte, 10)}),
	} {
		if _, err := DurationMS(bytes.NewReader(in)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
